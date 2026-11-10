package status

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"vole/internal/core/procs"
)

// fakeProcs 每次 Watch 都从 lists 里取一批推出去；ctx 取消时返回。
type fakeProcs struct {
	mu       sync.Mutex
	calls    int
	canceled int
	lists    chan []procs.Proc
}

func (f *fakeProcs) Watch(ctx context.Context, _ time.Duration, _ procs.Options, emit func([]procs.Proc)) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			f.mu.Lock()
			f.canceled++
			f.mu.Unlock()
			return ctx.Err()
		case l := <-f.lists:
			emit(l)
		}
	}
}

func (f *fakeProcs) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.canceled
}

// recorder 记录菜单操作触发的系统动作。
type recorder struct {
	mu        sync.Mutex
	copied    []string
	revealed  []string
	confirms  []string // 标题 + 说明
	alerts    []string
	confirmOK bool
	done      chan struct{} // 每个后台流程结束时发一次
}

var rec *recorder

func testActions(h *harness) actions {
	rec = &recorder{confirmOK: true, done: make(chan struct{}, 8)}
	return actions{
		fileIcon: func(string) ([]byte, error) { return nil, errors.New("测试里不取图标") },
		reveal:   func(p string) { rec.mu.Lock(); rec.revealed = append(rec.revealed, p); rec.mu.Unlock() },
		copyText: func(s string) { rec.mu.Lock(); rec.copied = append(rec.copied, s); rec.mu.Unlock() },
		confirm: func(title, detail, ok string, warn bool) bool {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			rec.confirms = append(rec.confirms, title+"｜"+detail)
			return rec.confirmOK
		},
		alert: func(title, detail string) {
			rec.mu.Lock()
			rec.alerts = append(rec.alerts, title+"｜"+detail)
			rec.mu.Unlock()
		},
		explain: func(_ context.Context, p procs.Proc) (procs.Explanation, error) {
			return procs.Explanation{
				Chain: []procs.Ancestor{{PID: 300, Name: "DeepSeek Harness"}, {PID: 1, Name: "launchd"}},
				Job:   &procs.LaunchJob{Label: "com.example.agent", Domain: "gui/501", PID: 300, Plist: "/Library/LaunchAgents/com.example.agent.plist"},
			}, nil
		},
		terminate: procs.Terminate, // 用真实实现：只拿测试自己起的 sleep 来结束
		spawn: func(fn func()) {
			go func() { fn(); rec.done <- struct{}{} }()
		},
	}
}

func (h *harness) pushProcs(list []procs.Proc) {
	h.t.Helper()
	select {
	case h.ps.lists <- list:
	case <-time.After(3 * time.Second):
		h.t.Fatal("进程采集没有在 Watch")
	}
	h.settle(1)
	h.drain() // 图标加载等其他回调也排在主线程队列里，一并处理掉
}

// drain 处理完主线程队列里已有的回调（等到 100ms 内没有新的为止）。
func (h *harness) drain() {
	for {
		select {
		case fn := <-h.main:
			fn()
		case <-time.After(100 * time.Millisecond):
			h.tt.Frame()
			return
		}
	}
}

func waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-rec.done:
	case <-time.After(5 * time.Second):
		t.Fatal("后台流程没有结束")
	}
}

var started = time.Date(2026, 11, 7, 5, 13, 50, 0, time.Local)

// sampleProcs 造 n 个进程：前几个是有代表性的（app 主进程、app 的 Helper、root 进程、高占用），其余填充。
func sampleProcs(n int) []procs.Proc {
	me := os.Getuid()
	list := []procs.Proc{
		{PID: 74115, PPID: 1, UID: me, User: "alice", Name: "analyze", Path: "/Applications/Vole.app/Contents/Resources/analyze", CPU: 126.5, MemBytes: 17e6, Started: started},
		{PID: 245, PPID: 1, UID: 0, User: "root", Name: "syspolicyd", Path: "/usr/libexec/syspolicyd", CPU: 93.6, MemBytes: 39e6, Started: started},
		{PID: 93944, PPID: 1, UID: me, User: "alice", Name: "replayd", Path: "/usr/libexec/replayd", CPU: 44, MemBytes: 11e6, Started: started},
		{PID: 152, PPID: 1, UID: 88, User: "_windowserver", Name: "WindowServer", Path: "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer", CPU: 32.2, MemBytes: 157e6, Started: started},
		{PID: 11428, PPID: 300, UID: me, User: "alice", Name: "DeepSeek Harness Helper (Renderer)",
			Path: "/Applications/DeepSeek Harness.app/Contents/Frameworks/DeepSeek Harness Helper (Renderer).app/Contents/MacOS/DeepSeek Harness Helper (Renderer)", CPU: 29.1, MemBytes: 293e6, Started: started},
		{PID: 64297, PPID: 1, UID: me, User: "alice", Name: "ChatGPT", Path: "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT", CPU: 11.2, MemBytes: 358e6, Started: started},
	}
	for i := len(list); i < n; i++ {
		list = append(list, procs.Proc{PID: 20000 + i, PPID: 1, UID: me, User: "alice", Name: fmt.Sprintf("worker%02d", i), Path: fmt.Sprintf("/usr/bin/worker%02d", i), CPU: float64(n-i) / 10, MemBytes: uint64(i) * 1e6, Started: started})
	}
	return list
}

func names(ps []procs.Proc) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestProcTableFiftyRowsSortAndPin(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{snap: fullFrame()}, 1)
	h.pushProcs(sampleProcs(50))
	h.want("名称（50）", "DeepSeek Harness", "DeepSeek Harness Helper (Renderer)", "ChatGPT", "126.5")
	if got := len(h.m.sortedProcs()); got != 50 {
		t.Fatalf("应有 50 行：%d", got)
	}

	// 排序：默认 CPU 降序；点名称 → 按显示名升序
	if o := names(h.m.sortedProcs()); o[0] != "analyze" || o[49] != "worker49" {
		t.Fatalf("默认按 CPU 降序：%v", o[:3])
	}
	if err := h.tt.Click("按名称排序"); err != nil {
		t.Fatal(err)
	}
	// 名称按显示名：analyze 在 Vole.app 里，显示为「Vole」，排在 ChatGPT、DeepSeek Harness 之后
	if o := names(h.m.sortedProcs()); o[0] != "ChatGPT" || o[1] != "DeepSeek Harness Helper (Renderer)" {
		t.Fatalf("名称升序：%v", o[:3])
	}
	if err := h.tt.Click("按CPU排序"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.Click("按CPU排序"); err != nil { // 回到 CPU 降序
		t.Fatal(err)
	}

	// 固定 CPU 最低的 worker49：始终在最上面，不受排序影响；采集器也会一直带上它
	if err := h.tt.RightClick("进程 worker49"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("固定行"); err != nil {
		t.Fatalf("%v；菜单：%q", err, h.tt.Menu())
	}
	h.tt.Frame()
	if o := names(h.m.sortedProcs()); o[0] != "worker49" {
		t.Fatalf("固定行应在最上面：%v", o[:3])
	}
	if !h.m.pt.pinSet.Has(20049) {
		t.Fatal("固定的进程应告诉采集器带上")
	}
	if err := h.tt.Click("按内存排序"); err != nil {
		t.Fatal(err)
	}
	if o := names(h.m.sortedProcs()); o[0] != "worker49" {
		t.Fatalf("换排序后固定行仍应在最上面：%v", o[:3])
	}
	h.snapshot("procs-pinned")

	// 再点一次取消固定
	if err := h.tt.RightClick("进程 worker49"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("取消固定"); err != nil {
		t.Fatalf("%v；菜单：%q", err, h.tt.Menu())
	}
	if h.m.pinned(sampleProcs(50)[49]) || h.m.pt.pinSet.Has(20049) {
		t.Fatal("应已取消固定")
	}
}

func TestProcLevelsNeedSustainedCPU(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{snap: fullFrame()}, 1)
	list := sampleProcs(10)
	for i := 0; i < sustain-1; i++ {
		h.pushProcs(list)
	}
	if h.m.levelOf(list[0]) != levelNone {
		t.Fatal("还没持续够，不应标色")
	}
	h.pushProcs(list)
	if h.m.levelOf(list[0]) != levelHot || h.m.levelOf(list[1]) != levelHot || h.m.levelOf(list[2]) != levelWarn || h.m.levelOf(list[3]) != levelNone {
		t.Fatalf("分档不对：%v %v %v %v", h.m.levelOf(list[0]), h.m.levelOf(list[1]), h.m.levelOf(list[2]), h.m.levelOf(list[3]))
	}
	h.snapshot("procs-table")
	// 降下来一次就清零
	calm := sampleProcs(10)
	calm[0].CPU = 5
	h.pushProcs(calm)
	if h.m.levelOf(calm[0]) != levelNone {
		t.Fatal("降下来后应清掉标色")
	}
}

func TestProcMenuActions(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{snap: fullFrame()}, 1)
	h.pushProcs(sampleProcs(10))

	choose := func(row, item string) {
		t.Helper()
		if err := h.tt.RightClick("进程 " + row); err != nil {
			t.Fatal(err)
		}
		if err := h.tt.ChooseMenuItem(item); err != nil {
			t.Fatalf("%v；菜单：%q", err, h.tt.Menu())
		}
		h.tt.Frame()
	}
	helper := "DeepSeek Harness Helper (Renderer)"

	// 菜单项和顺序
	if err := h.tt.RightClick("进程 " + helper); err != nil {
		t.Fatal(err)
	}
	want := []string{"固定行", "-", "为什么在运行？", "在 Finder 中显示", "-", "复制进程名称", "复制 PID", "-", "终止", "强制退出"}
	if got := h.tt.Menu(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("菜单不对：%q", got)
	}
	h.tt.Key(0, 0) // 关掉菜单（下一次 RightClick 会重新打开）

	choose(helper, "复制进程名称")
	choose(helper, "复制 PID")
	choose(helper, "在 Finder 中显示")
	rec.mu.Lock()
	if strings.Join(rec.copied, ",") != helper+",11428" || rec.revealed[0] != "/Applications/DeepSeek Harness.app" {
		t.Fatalf("复制 / Finder 不对：%v %v", rec.copied, rec.revealed)
	}
	rec.mu.Unlock()

	// 为什么在运行？
	choose(helper, "为什么在运行？")
	waitDone(t)
	rec.mu.Lock()
	info := rec.alerts[0]
	rec.mu.Unlock()
	for _, s := range []string{"「DeepSeek Harness」为什么在运行", "可执行文件：/Applications/DeepSeek Harness.app", "启动时间：2026-11-07 05:13:50",
		"所属用户：alice（UID", "父进程链：" + helper + " (11428) → DeepSeek Harness (300) → launchd (1)",
		"launchd 任务：com.example.agent（gui/501 域；本进程由该任务的进程 300 派生）", "plist：/Library/LaunchAgents/com.example.agent.plist"} {
		if !strings.Contains(info, s) {
			t.Fatalf("说明里缺 %q：\n%s", s, info)
		}
	}

	// 别人的 / 保护名单里的进程：终止、强制退出置灰并写明原因
	if err := h.tt.RightClick("进程 syspolicyd"); err != nil {
		t.Fatal(err)
	}
	menu := strings.Join(h.tt.Menu(), "|")
	if !strings.Contains(menu, "终止（需要管理员权限）") || !strings.Contains(menu, "强制退出（需要管理员权限）") {
		t.Fatalf("root 进程应置灰：%s", menu)
	}
	if err := h.tt.ChooseMenuItem("终止（需要管理员权限）"); err == nil {
		t.Fatal("置灰的项不应能选")
	}
	h.tt.Key(0, 0)
	if err := h.tt.RightClick("进程 WindowServer"); err != nil {
		t.Fatal(err)
	}
	if menu := strings.Join(h.tt.Menu(), "|"); !strings.Contains(menu, "终止（结束会让整个图形界面重启") {
		t.Fatalf("WindowServer 应置灰并写明原因：%s", menu)
	}
	h.snapshot("procs-menu")
}

// TestKillRealSleep 用自己起的 sleep 实测：确认框文字、终止（SIGTERM）、强制退出（SIGKILL）、取消、已退出的失败提示。
func TestKillRealSleep(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{snap: fullFrame()}, 1)

	start := func() (*exec.Cmd, procs.Proc) {
		cmd := exec.Command("/bin/sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		p, err := procs.Lookup(context.Background(), cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		return cmd, p
	}
	sigOf := func(cmd *exec.Cmd) syscall.Signal {
		err := cmd.Wait()
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("sleep 应被信号结束：%v", err)
		}
		return ee.Sys().(syscall.WaitStatus).Signal()
	}
	kill := func(p procs.Proc, item string) {
		t.Helper()
		h.pushProcs(append(sampleProcs(5), p))
		if err := h.tt.RightClick(fmt.Sprintf("进程 %s", p.Name)); err != nil {
			t.Fatal(err)
		}
		if err := h.tt.ChooseMenuItem(item); err != nil {
			t.Fatalf("%v；菜单：%q", err, h.tt.Menu())
		}
		waitDone(t)
	}

	// 取消：不结束
	rec.mu.Lock()
	rec.confirmOK = false
	rec.mu.Unlock()
	cmd, p := start()
	kill(p, "终止")
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("点了取消却结束了进程")
	}
	rec.mu.Lock()
	rec.confirmOK = true
	rec.mu.Unlock()

	// 终止
	kill(p, "终止")
	if sig := sigOf(cmd); sig != syscall.SIGTERM {
		t.Fatalf("终止应发 SIGTERM：%v", sig)
	}
	// 强制退出
	cmd2, p2 := start()
	kill(p2, "强制退出")
	if sig := sigOf(cmd2); sig != syscall.SIGKILL {
		t.Fatalf("强制退出应发 SIGKILL：%v", sig)
	}

	rec.mu.Lock()
	term, force := rec.confirms[1], rec.confirms[2]
	rec.mu.Unlock()
	if !strings.Contains(term, fmt.Sprintf("终止「sleep」（PID %d）？", p.PID)) || !strings.Contains(term, "SIGTERM") {
		t.Fatalf("终止确认框：%s", term)
	}
	if !strings.Contains(force, fmt.Sprintf("强制退出「sleep」（PID %d）？", p2.PID)) || !strings.Contains(force, "未保存的数据会丢失") {
		t.Fatalf("强制退出确认框：%s", force)
	}

	// 已经退出的进程再结束：提示原因
	kill(p2, "强制退出")
	rec.mu.Lock()
	last := rec.alerts[len(rec.alerts)-1]
	rec.mu.Unlock()
	if !strings.Contains(last, "无法结束「sleep」") || !strings.Contains(last, "进程已经退出了") {
		t.Fatalf("失败提示：%s", last)
	}
}

func TestProcsStopWhenHidden(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.pushProcs(sampleProcs(5))
	h.m.OnDeactivate()
	deadline := time.Now().Add(3 * time.Second)
	for _, c := h.ps.counts(); c < 1; _, c = h.ps.counts() {
		if time.Now().After(deadline) {
			t.Fatal("切走后进程采集应停止")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.m.OnActivate()
	deadline = time.Now().Add(3 * time.Second)
	for calls, _ := h.ps.counts(); calls < 2; calls, _ = h.ps.counts() {
		if time.Now().After(deadline) {
			t.Fatal("切回来应重新采集")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
