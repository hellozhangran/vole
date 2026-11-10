// SPDX-License-Identifier: GPL-3.0-only

// Package apps 是「软件」tab：已装 app 列表（排序、搜索、多选）→ 展开关联文件逐项勾选 → 确认 → 卸载中 → 完成页。
// 只做卸载。卸载只发生在用户在确认框里点了「移除」之后，一律移到废纸篓。状态只在主线程读写。
package apps

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	core "vole/internal/core/apps"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/task"
)

// Backend 是卸载的数据源。界面测试用假的，绝不在本机卸载。
type Backend interface {
	List(ctx context.Context) ([]core.App, error)
	Files(ctx context.Context, app core.App, installed []core.App) []core.File
	// Run 按清单卸载；quit 为 true 时运行中的 app 先退出。ctx 取消后当前这个 app 做完就停。
	Run(ctx context.Context, plans []core.Plan, quit bool, progress func(done, total int, o core.Outcome)) *core.Report
	TrashDir() string
	LogDir() string
}

// NewBackend 是本机的真实数据源（core/apps）。
func NewBackend() Backend {
	env, err := core.DefaultEnv()
	if err != nil {
		return errBackend{err}
	}
	return coreBackend{env}
}

type coreBackend struct{ env *core.Env }

func (b coreBackend) List(ctx context.Context) ([]core.App, error) {
	return core.List(ctx, b.env, core.ListOptions{Sizes: true})
}
func (b coreBackend) Files(ctx context.Context, a core.App, installed []core.App) []core.File {
	return core.Files(ctx, b.env, a, installed)
}
func (b coreBackend) Run(ctx context.Context, plans []core.Plan, quit bool, progress func(int, int, core.Outcome)) *core.Report {
	return core.Run(ctx, b.env, plans, core.RunOptions{Quit: quit, Progress: progress})
}
func (b coreBackend) TrashDir() string { return filepath.Join(b.env.Home, ".Trash") }
func (b coreBackend) LogDir() string   { return b.env.LogDir }

type errBackend struct{ err error }

func (b errBackend) List(context.Context) ([]core.App, error)                { return nil, b.err }
func (b errBackend) Files(context.Context, core.App, []core.App) []core.File { return nil }
func (b errBackend) Run(context.Context, []core.Plan, bool, func(int, int, core.Outcome)) *core.Report {
	return &core.Report{}
}
func (b errBackend) TrashDir() string { return "" }
func (b errBackend) LogDir() string   { return "" }

type phase int

const (
	phaseLoading phase = iota
	phaseList
	phaseRunning
	phaseDone
)

// sortKey 是列表的排序方式。
type sortKey int

const (
	sortName sortKey = iota
	sortSize
	sortUsed
	sortInstalled
)

// filesState 是一个选中 app 的关联文件。
type filesState struct {
	loading bool
	files   []core.File
	sel     map[string]bool // 文件路径 → 勾选
}

// Module 是软件模块。
type Module struct {
	be    Backend
	theme kit.Theme
	tasks *task.Runner

	phase   phase
	loadErr error
	apps    []core.App
	loaded  bool

	sort  sortKey
	asc   bool
	query string

	sel      map[string]bool        // app 路径 → 勾选
	files    map[string]*filesState // app 路径 → 关联文件
	expanded map[string]bool        // app 路径 → 关联文件展开
	icons    map[string]*ui.Bitmap  // app 路径 → 图标（nil = 正在取或取不到）
	confirm  bool

	plan       []core.Plan
	runDone    int
	runCancel  context.CancelFunc
	cancelling bool
	report     *core.Report

	home     string // 家目录，路径里显示成 ~
	now      func() time.Time
	reveal   func(path string)
	openPath func(path string)
	fileIcon func(path string) ([]byte, error)
}

var (
	_ module.Module   = (*Module)(nil)
	_ module.FullPage = (*Module)(nil)
)

// New 创建软件模块。
func New(be Backend) *Module {
	return &Module{be: be, theme: kit.Wine(), sort: sortSize,
		sel: map[string]bool{}, files: map[string]*filesState{}, expanded: map[string]bool{}, icons: map[string]*ui.Bitmap{},
		home:     userHome(),
		now:      time.Now,
		reveal:   mygo.Shell.ShowItemInFolder,
		openPath: func(p string) { _ = mygo.Shell.OpenPath(p) },
		fileIcon: func(p string) ([]byte, error) { return mygo.Shell.FileIcon(p, 64) },
	}
}

func (m *Module) ID() string         { return "apps" }
func (m *Module) Title() string      { return "软件" }
func (m *Module) Theme() kit.Theme   { return m.theme }
func (m *Module) Init(h module.Host) { m.tasks = h.Tasks() }

// FullPage：卸载中、完成页整页铺满（同清理 tab）；列表保留顶部 tab 条（同 Mole 截图 13）。
func (m *Module) FullPage() bool { return m.phase == phaseRunning || m.phase == phaseDone }

// OnActivate：第一次切到这个 tab 时读列表；之后保留状态，点刷新才重读。
func (m *Module) OnActivate() {
	if !m.loaded && m.phase == phaseLoading {
		m.refresh()
	}
}

func (m *Module) OnDeactivate() {}

// ── 列表 ──

func (m *Module) refresh() {
	m.phase, m.loadErr, m.loaded = phaseLoading, nil, true
	be := m.be
	task.Go(m.tasks, "list", func(ctx context.Context) ([]core.App, error) { return be.List(ctx) },
		func(list []core.App, err error) {
			m.phase = phaseList
			if err != nil && len(list) == 0 {
				m.loadErr = err
				return
			}
			m.apps = list
			// 勾选和关联文件只保留还在的 app
			keep := map[string]bool{}
			for _, a := range list {
				keep[a.Path] = true
			}
			for p := range m.sel {
				if !keep[p] {
					delete(m.sel, p)
					delete(m.files, p)
				}
			}
		})
}

// visible 是排序、搜索后的列表。
func (m *Module) visible() []core.App {
	var out []core.App
	q := strings.ToLower(strings.TrimSpace(m.query))
	for _, a := range m.apps {
		if q == "" || strings.Contains(strings.ToLower(a.Name), q) || strings.Contains(strings.ToLower(a.BundleID), q) ||
			strings.Contains(strings.ToLower(filepath.Base(a.Path)), q) {
			out = append(out, a)
		}
	}
	less := func(i, j int) bool {
		a, b := out[i], out[j]
		switch m.sort {
		case sortSize:
			return a.Size < b.Size
		case sortUsed:
			return a.LastUsed.Before(b.LastUsed)
		case sortInstalled:
			return a.Installed.Before(b.Installed)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if m.asc {
			return less(i, j)
		}
		return less(j, i)
	})
	return out
}

// setSort：点同一个排序再点一次切换升降序；换排序时名称默认升序，其余默认降序。
func (m *Module) setSort(k sortKey) {
	if m.sort == k {
		m.asc = !m.asc
		return
	}
	m.sort, m.asc = k, k == sortName
}

func (m *Module) total() int64 {
	var n int64
	for _, a := range m.apps {
		n += a.Size
	}
	return n
}

// selectable：能勾（没被挡住）。
func selectable(a core.App) bool { return a.Blocked == "" }

// toggle 勾选 / 取消一个 app；勾上时去找它的关联文件（缓存和日志默认勾，个人数据默认不勾）。
func (m *Module) toggle(a core.App) {
	if !selectable(a) {
		return
	}
	if m.sel[a.Path] {
		delete(m.sel, a.Path)
		m.expanded[a.Path] = false
		return
	}
	m.sel[a.Path] = true
	if _, ok := m.files[a.Path]; ok {
		return
	}
	fs := &filesState{loading: true, sel: map[string]bool{}}
	m.files[a.Path] = fs
	be, installed := m.be, append([]core.App(nil), m.apps...)
	task.Go(m.tasks, "files:"+a.Path, func(ctx context.Context) ([]core.File, error) { return be.Files(ctx, a, installed), nil },
		func(files []core.File, err error) {
			fs.loading, fs.files = false, files
			for _, f := range files {
				if f.Default && f.Kind != core.KindSystem {
					fs.sel[f.Path] = true
				}
			}
		})
}

func (m *Module) clearSelection() {
	m.sel = map[string]bool{}
	m.expanded = map[string]bool{}
}

// selected 是勾选的 app（按列表顺序）。
func (m *Module) selected() []core.App {
	var out []core.App
	for _, a := range m.apps {
		if m.sel[a.Path] && selectable(a) {
			out = append(out, a)
		}
	}
	return out
}

// pickedFiles 是一个 app 勾选的关联文件（系统级不算）。
func (m *Module) pickedFiles(a core.App) []core.File {
	fs := m.files[a.Path]
	if fs == nil {
		return nil
	}
	var out []core.File
	for _, f := range fs.files {
		if f.Kind != core.KindSystem && fs.sel[f.Path] {
			out = append(out, f)
		}
	}
	return out
}

func sizeOf(files []core.File) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return n
}

// selectedSize 是勾选的 app 本体 + 勾选的关联文件的大小。
func (m *Module) selectedSize() int64 {
	var n int64
	for _, a := range m.selected() {
		n += a.Size + sizeOf(m.pickedFiles(a))
	}
	return n
}

// filesLoading：还有选中的 app 在找关联文件（这时不让点移除）。
func (m *Module) filesLoading() bool {
	for _, a := range m.selected() {
		if fs := m.files[a.Path]; fs == nil || fs.loading {
			return true
		}
	}
	return false
}

// ── 卸载 ──

type runEvent struct {
	done   int
	report *core.Report
}

func (m *Module) startRun() {
	var plan []core.Plan
	quit := false
	for _, a := range m.selected() {
		plan = append(plan, core.Plan{App: a, Files: m.pickedFiles(a)})
		quit = quit || a.Running
	}
	if len(plan) == 0 {
		return
	}
	m.confirm, m.phase = false, phaseRunning
	m.plan, m.runDone, m.cancelling, m.report = plan, 0, false, nil
	userCtx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	be := m.be
	task.Stream(m.tasks, "run", func(ctx context.Context, emit func(runEvent)) error {
		rctx, stop := context.WithCancel(userCtx)
		defer stop()
		unhook := context.AfterFunc(ctx, stop)
		defer unhook()
		rep := be.Run(rctx, plan, quit, func(done, total int, o core.Outcome) { emit(runEvent{done: done}) })
		emit(runEvent{report: rep})
		return nil
	}, m.onRunEvent, nil)
}

func (m *Module) onRunEvent(e runEvent) {
	if e.report == nil {
		m.runDone = e.done
		return
	}
	m.report, m.phase, m.runCancel = e.report, phaseDone, nil
}

func (m *Module) cancelRun() {
	if m.runCancel != nil && !m.cancelling {
		m.cancelling = true
		m.runCancel()
	}
}

// finish 从完成页回到列表，并重读列表（卸掉的 app 不在了）。
func (m *Module) finish() {
	m.report, m.plan = nil, nil
	m.clearSelection()
	m.files = map[string]*filesState{}
	m.refresh()
}

// ── 图标 ──

// icon 返回 app 图标；第一次用到时在后台取（FileIcon 要排到主线程，同状态 tab 的做法）。
func (m *Module) icon(path string) *ui.Bitmap {
	if b, tried := m.icons[path]; tried {
		return b
	}
	m.icons[path] = nil
	get := m.fileIcon
	task.Go(m.tasks, "icon:"+path, func(ctx context.Context) (*ui.Bitmap, error) {
		type result struct {
			b   *ui.Bitmap
			err error
		}
		ch := make(chan result, 1)
		go func() {
			data, err := get(path)
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
			m.icons[path] = b
		}
	})
	return nil
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}
