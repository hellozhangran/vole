package status

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	ui2 "vole/internal/features/statusui"
	"vole/internal/i18n"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vole/internal/core/procs"
	"vole/internal/task"
)

const (
	procInterval = 2 * time.Second // 进程表采样间隔
	procTop      = 50              // 取 CPU 前多少个
	// 持续高 CPU 的标色：连续 sustain 次采样（约 6 秒）都超过阈值才标，避免瞬时尖峰闪烁。
	// 单位是单核百分比：warnCPU 约等于持续吃掉小半个核，hotCPU 接近吃满一个核。
	warnCPU = 40.0
	hotCPU  = 80.0
	sustain = 3
)

// level 是一行的高占用程度。
type level int

const (
	levelNone level = iota
	levelWarn
	levelHot
)

// pinKey 用 PID + 启动时间标识一个进程，PID 被系统复用时不会认错。
type pinKey struct {
	pid     int
	started time.Time
}

func keyOf(p procs.Proc) pinKey { return pinKey{p.PID, p.Started} }

// procTable 是进程表的状态，只在主线程读写（pinSet 例外，它会被采集 goroutine 读）。
type procTable struct {
	list      []procs.Proc
	err       error
	running   bool
	pins      map[pinKey]bool
	pinSet    procs.PIDSet // 告诉采集器：固定的进程即使不在前 50 也要带上
	warn, hot map[pinKey]int
	icons     map[string]*ui.Bitmap // .app 路径 → 图标；值为 nil 表示取不到
}

// actions 是会碰到系统的操作，测试里替换。confirm / alert / showInfo 会阻塞，只在后台 goroutine 里调用。
type actions struct {
	fileIcon  func(path string) ([]byte, error)
	reveal    func(path string)
	copyText  func(text string)
	confirm   func(title, detail, ok string, warn bool) bool
	alert     func(title, detail string)
	explain   func(ctx context.Context, p procs.Proc) (procs.Explanation, error)
	terminate func(ctx context.Context, p procs.Proc, force bool) error
	spawn     func(fn func())
}

func defaultActions() actions {
	return actions{
		fileIcon: func(path string) ([]byte, error) { return mygo.Shell.FileIcon(path, 64) },
		reveal:   mygo.Shell.ShowItemInFolder,
		copyText: mygo.Clipboard.WriteText,
		confirm: func(title, detail, ok string, warn bool) bool {
			typ := mygo.MessageInfo
			if warn {
				typ = mygo.MessageWarning
			}
			res, err := mygo.Dialog.Message(mygo.MessageOptions{Type: typ, Message: title, Detail: detail, Buttons: []string{ok, i18n.T("取消")}, CancelButton: 1})
			return err == nil && res.Button == 0
		},
		alert: func(title, detail string) {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{Message: title, Detail: detail})
		},
		explain: func(ctx context.Context, p procs.Proc) (procs.Explanation, error) {
			return procs.Explain(ctx, p, os.Getuid())
		},
		terminate: procs.Terminate,
		spawn:     func(fn func()) { go fn() },
	}
}

// startProcs 启动进程表采集（只在 tab 可见时）。
func (m *Module) startProcs() {
	m.pt.running = true
	src, pins := m.procSrc, &m.pt.pinSet
	task.Stream(m.tasks, "procs",
		func(ctx context.Context, emit func([]procs.Proc)) error {
			return src.Watch(ctx, procInterval, procs.Options{Top: procTop, Extra: pins}, emit)
		},
		m.onProcs,
		func(err error) {
			// ps 出错（极少见）：显示原因，可见时 2 秒后重试
			m.pt.running, m.pt.err = false, err
			task.Go(m.tasks, "procs-restart", func(ctx context.Context) (struct{}, error) {
				select {
				case <-time.After(restartDelay):
				case <-ctx.Done():
				}
				return struct{}{}, ctx.Err()
			}, func(_ struct{}, err error) {
				if err == nil && m.active && !m.pt.running {
					m.startProcs()
				}
			})
		})
}

func (m *Module) stopProcs() {
	m.tasks.Cancel("procs")
	m.tasks.Cancel("procs-restart")
	m.pt.running = false
}

// onProcs 在主线程收下一次采样：更新持续高占用的计数，清掉已退出进程的固定标记。
func (m *Module) onProcs(list []procs.Proc) {
	t := &m.pt
	t.list, t.err = list, nil
	alive := map[pinKey]bool{}
	warn, hot := map[pinKey]int{}, map[pinKey]int{}
	for _, p := range list {
		k := keyOf(p)
		alive[k] = true
		if p.CPU >= warnCPU {
			warn[k] = t.warn[k] + 1
		}
		if p.CPU >= hotCPU {
			hot[k] = t.hot[k] + 1
		}
	}
	t.warn, t.hot = warn, hot
	for k := range t.pins {
		if !alive[k] { // 固定的进程总会被带上；不在列表里说明已经退出
			delete(t.pins, k)
			t.pinSet.Set(k.pid, false)
		}
	}
}

func (m *Module) levelOf(p procs.Proc) level {
	k := keyOf(p)
	switch {
	case m.pt.hot[k] >= sustain:
		return levelHot
	case m.pt.warn[k] >= sustain:
		return levelWarn
	}
	return levelNone
}

func (m *Module) pinned(p procs.Proc) bool { return m.pt.pins[keyOf(p)] }

// togglePin 固定 / 取消固定一行；只在本次运行内有效。
func (m *Module) togglePin(p procs.Proc) {
	if m.pt.pins == nil {
		m.pt.pins = map[pinKey]bool{}
	}
	k := keyOf(p)
	on := !m.pt.pins[k]
	if on {
		m.pt.pins[k] = true
	} else {
		delete(m.pt.pins, k)
	}
	m.pt.pinSet.Set(p.PID, on)
}

// displayName：.app 里的进程显示 app 名，进程自己的名字作为副名称（和 app 名相同时不重复）。
func displayName(p procs.Proc) (name, sub string) {
	app := procs.AppName(procs.AppBundle(p.Path))
	if app == "" {
		return p.Name, ""
	}
	if app == p.Name {
		return app, ""
	}
	return app, p.Name
}

// revealTarget 在 Finder 中显示的目标：.app 里的进程定位到 .app，其余定位到可执行文件。
func revealTarget(p procs.Proc) string {
	if b := procs.AppBundle(p.Path); b != "" {
		return b
	}
	return p.Path
}

// icon 返回 .app 的图标，第一次用到时在后台取；取不到或不是 app 返回 nil（用通用图标）。
func (m *Module) icon(p procs.Proc) *ui.Bitmap {
	bundle := procs.AppBundle(p.Path)
	if bundle == "" {
		return nil
	}
	if m.pt.icons == nil {
		m.pt.icons = map[string]*ui.Bitmap{}
	}
	if b, tried := m.pt.icons[bundle]; tried {
		return b
	}
	m.pt.icons[bundle] = nil // 标记为正在取，失败就一直用通用图标
	get := m.act.fileIcon
	task.Go(m.tasks, "icon:"+bundle, func(ctx context.Context) (*ui.Bitmap, error) {
		// FileIcon 要排到主线程执行；退出时 Runner.Close 在主线程上等后台任务，
		// 所以在内层 goroutine 里取，外层同时听取消，取消时立即返回，避免互相等待卡死退出。
		type result struct {
			b   *ui.Bitmap
			err error
		}
		ch := make(chan result, 1)
		go func() {
			data, err := get(bundle)
			if err != nil {
				ch <- result{nil, err}
				return
			}
			b, err := ui.DecodeBitmap(data)
			ch <- result{b, err}
		}()
		select {
		case r := <-ch:
			return r.b, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, func(b *ui.Bitmap, err error) {
		if err == nil {
			m.pt.icons[bundle] = b
		}
	})
	return nil
}

// ── 菜单 ──

// procMenu 是每行「…」和右键共用的菜单，项目和顺序参照 Mole。
func (m *Module) procMenu(menu *ui.Menu, p procs.Proc) {
	pin := i18n.T("固定行")
	if m.pinned(p) {
		pin = i18n.T("取消固定")
	}
	if menu.Item(pin).Chosen() {
		m.togglePin(p)
	}
	menu.Separator()
	if menu.Item(i18n.T("为什么在运行？")).Chosen() {
		m.explainProc(p)
	}
	target := revealTarget(p)
	if menu.Item(i18n.T("在 Finder 中显示")).Disabled(target == "").Chosen() {
		m.act.reveal(target)
	}
	menu.Separator()
	if menu.Item(i18n.T("复制进程名称")).Chosen() {
		m.act.copyText(p.Name)
	}
	if menu.Item(i18n.T("复制 PID")).Chosen() {
		m.act.copyText(strconv.Itoa(p.PID))
	}
	menu.Separator()
	// 不能结束的进程：置灰，并把原因写在菜单项里（系统菜单项不支持悬停提示）
	reason := procs.CanTerminate(p)
	term, kill := i18n.T("终止"), i18n.T("强制退出")
	if reason != "" {
		term += i18n.T("（") + reason + i18n.T("）")
		kill += i18n.T("（") + reason + i18n.T("）")
	}
	if menu.Item(term).Disabled(reason != "").Chosen() {
		m.askKill(p, false)
	}
	if menu.Item(kill).Disabled(reason != "").Chosen() {
		m.askKill(p, true)
	}
}

// askKill 先弹确认框，确认后结束进程；失败弹出原因。整个流程在后台 goroutine，不卡界面。
func (m *Module) askKill(p procs.Proc, force bool) {
	name, _ := displayName(p)
	who := fmt.Sprintf(i18n.T("「%s」（PID %d）"), p.Name, p.PID)
	if name != p.Name {
		who = fmt.Sprintf(i18n.T("「%s」的「%s」（PID %d）"), name, p.Name, p.PID)
	}
	title, ok := i18n.Tf("终止%s？", who), i18n.T("终止")
	detail := i18n.T("会向进程发送终止信号（SIGTERM），它通常会先保存状态再退出。")
	if force {
		title, ok = i18n.Tf("强制退出%s？", who), i18n.T("强制退出")
		detail = i18n.T("会立即结束进程（SIGKILL），它来不及保存，未保存的数据会丢失。")
	}
	a := m.act
	a.spawn(func() {
		if !a.confirm(title, detail, ok, force) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.terminate(ctx, p, force); err != nil {
			a.alert(i18n.T("无法结束")+who, killReason(err))
		}
	})
}

// killReason 把结束失败的错误转成给用户看的一句话。
func killReason(err error) string {
	switch {
	case errors.Is(err, procs.ErrGone):
		return i18n.T("进程已经退出了。")
	case errors.Is(err, procs.ErrNotOwner):
		return i18n.T("这是其他用户或系统的进程，需要管理员权限才能结束。")
	case errors.Is(err, procs.ErrPermission):
		return i18n.T("权限不够，系统拒绝了这个操作。")
	case errors.Is(err, procs.ErrProtected):
		return err.Error()
	}
	return err.Error()
}

// explainProc 查「为什么在运行」并弹出说明；查不到的项不显示。
func (m *Module) explainProc(p procs.Proc) {
	a := m.act
	a.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ex, err := a.explain(ctx, p)
		name, _ := displayName(p)
		title := fmt.Sprintf(i18n.T("「%s」为什么在运行"), name)
		if err != nil {
			a.alert(title, i18n.T("查询失败：")+err.Error())
			return
		}
		a.alert(title, explainText(p, ex, time.Now()))
	})
}

// explainText 拼出说明文字。
func explainText(p procs.Proc, ex procs.Explanation, now time.Time) string {
	var lines []string
	add := func(label, v string) {
		if v != "" {
			lines = append(lines, label+i18n.T("：")+v)
		}
	}
	add(i18n.T("可执行文件"), p.Path)
	if !p.Started.IsZero() {
		add(i18n.T("启动时间"), p.Started.Format("2006-01-02 15:04:05")+i18n.T("（已运行 ")+ui2.UptimeText(now.Sub(p.Started))+i18n.T("）"))
	}
	user := p.User
	if user != "" {
		user += fmt.Sprintf(i18n.T("（UID %d）"), p.UID)
	} else {
		user = fmt.Sprintf("UID %d", p.UID)
	}
	add(i18n.T("所属用户"), user)
	if len(ex.Chain) > 0 {
		chain := []string{fmt.Sprintf("%s (%d)", p.Name, p.PID)}
		for _, a := range ex.Chain {
			chain = append(chain, fmt.Sprintf("%s (%d)", a.Name, a.PID))
		}
		add(i18n.T("父进程链"), strings.Join(chain, " → "))
	}
	if j := ex.Job; j != nil {
		via := ""
		if j.PID != p.PID {
			via = fmt.Sprintf(i18n.T("；本进程由该任务的进程 %d 派生"), j.PID)
		}
		add(i18n.T("launchd 任务"), fmt.Sprintf(i18n.T("%s（%s 域%s）"), j.Label, j.Domain, via))
		add("plist", j.Plist)
		if j.Plist == "" && j.SubmittedBy != "" {
			add(i18n.T("提交者"), j.SubmittedBy+i18n.T("（运行时提交，没有 plist 文件）"))
		}
	}
	return strings.Join(lines, "\n")
}
