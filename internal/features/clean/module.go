// SPDX-License-Identifier: GPL-3.0-only

// Package clean 是「清理」tab：首屏 → 扫描中 → 结果清单（整页、逐项勾选）→ 确认 → 清理中 → 完成。
// 删除只发生在用户在确认框里点了「永久清理」之后，且只针对勾选的项。状态只在主线程读写。
package clean

import (
	"context"
	"time"

	"github.com/egoist/mygo"

	core "vole/internal/core/clean"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/task"
)

// Backend 是清理的数据源。界面测试用假的，绝不在本机真删。
type Backend interface {
	// Scan 只读扫描；progress 会从多个 goroutine 调用。
	Scan(ctx context.Context, progress func(category string, done bool)) (*core.ScanResult, error)
	// Run 按清单清理；ctx 取消后当前这一项做完就停。
	Run(ctx context.Context, items []core.Item, progress func(done, total int, o core.Outcome)) *core.RunReport
	LastClean() (at time.Time, freed int64, ok bool)
	LogDir() string
}

// NewBackend 是本机的真实数据源（core/clean）。
func NewBackend() Backend {
	env, err := core.DefaultEnv()
	if err != nil {
		return errBackend{err}
	}
	return coreBackend{env}
}

type coreBackend struct{ env *core.Env }

func (b coreBackend) Scan(ctx context.Context, progress func(string, bool)) (*core.ScanResult, error) {
	return core.ScanWith(ctx, b.env, core.ScanOptions{Progress: progress})
}

func (b coreBackend) Run(ctx context.Context, items []core.Item, progress func(int, int, core.Outcome)) *core.RunReport {
	return core.Run(ctx, b.env, items, core.RunOptions{Progress: progress})
}

func (b coreBackend) LastClean() (time.Time, int64, bool) { return core.LastClean(b.env) }
func (b coreBackend) LogDir() string                      { return b.env.LogDir }

type errBackend struct{ err error }

func (b errBackend) Scan(context.Context, func(string, bool)) (*core.ScanResult, error) {
	return nil, b.err
}
func (b errBackend) Run(context.Context, []core.Item, func(int, int, core.Outcome)) *core.RunReport {
	return &core.RunReport{}
}
func (b errBackend) LastClean() (time.Time, int64, bool) { return time.Time{}, 0, false }
func (b errBackend) LogDir() string                      { return "" }

type phase int

const (
	phaseHome phase = iota
	phaseScanning
	phaseResults
	phaseCleaning
	phaseDone
)

// Module 是清理模块。
type Module struct {
	be    Backend
	theme kit.Theme
	tasks *task.Runner

	phase   phase
	running []string // 扫描中：正在扫的类别
	scanErr error

	res      *core.ScanResult
	sel      map[string]bool // 项 ID → 是否勾选
	expanded map[string]bool // 类别 ID → 是否展开
	confirm  bool            // 确认框是否打开

	plan       []core.Item // 正在清理的清单
	runDone    int
	runFreed   int64
	runCancel  context.CancelFunc
	cancelling bool
	report     *core.RunReport

	lastAt    time.Time
	lastFreed int64
	lastOK    bool

	reveal       func(path string) // 在 Finder 中显示
	openPath     func(path string) // 在 Finder 中打开目录
	openSettings func()            // 打开「完全磁盘访问」设置
}

var (
	_ module.Module   = (*Module)(nil)
	_ module.FullPage = (*Module)(nil)
)

// New 创建清理模块。
func New(be Backend) *Module {
	return &Module{be: be, theme: kit.Ocean(), sel: map[string]bool{}, expanded: map[string]bool{},
		reveal:   mygo.Shell.ShowItemInFolder,
		openPath: func(p string) { _ = mygo.Shell.OpenPath(p) },
		openSettings: func() {
			_ = mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")
		},
	}
}

func (m *Module) ID() string         { return "clean" }
func (m *Module) Title() string      { return "清理" }
func (m *Module) Theme() kit.Theme   { return m.theme }
func (m *Module) Init(h module.Host) { m.tasks = h.Tasks() }

// FullPage：结果、清理中、完成页整页铺满，隐藏顶部 tab 条（参照 Mole）。
func (m *Module) FullPage() bool {
	return m.phase == phaseResults || m.phase == phaseCleaning || m.phase == phaseDone
}

// OnActivate 回到首屏时刷新「上次清理」；其他阶段保持原状。
func (m *Module) OnActivate() {
	if m.phase == phaseHome {
		m.refreshLast()
	}
}

// OnDeactivate：切走时扫描照常在后台跑，结果保留。
func (m *Module) OnDeactivate() {}

func (m *Module) refreshLast() { m.lastAt, m.lastFreed, m.lastOK = m.be.LastClean() }

// ── 扫描 ──

type scanEvent struct {
	title  string
	done   bool
	result *core.ScanResult
	err    error
	final  bool
}

func (m *Module) startScan() {
	m.phase, m.running, m.scanErr = phaseScanning, nil, nil
	be := m.be
	task.Stream(m.tasks, "scan", func(ctx context.Context, emit func(scanEvent)) error {
		res, err := be.Scan(ctx, func(title string, done bool) { emit(scanEvent{title: title, done: done}) })
		emit(scanEvent{result: res, err: err, final: true})
		return nil
	}, m.onScanEvent, nil)
}

func (m *Module) onScanEvent(e scanEvent) {
	if !e.final {
		if e.done {
			m.running = remove(m.running, e.title)
		} else if !contains(m.running, e.title) {
			m.running = append(m.running, e.title)
		}
		return
	}
	if e.result == nil {
		m.phase, m.scanErr = phaseHome, e.err
		return
	}
	m.res = e.result
	m.expanded = map[string]bool{}
	m.recommend()
	m.phase = phaseResults
}

// cancelScan 取消扫描，回到首屏。
func (m *Module) cancelScan() {
	m.tasks.Cancel("scan")
	m.phase, m.running = phaseHome, nil
}

// closeResults 关掉结果页回首屏（结果丢弃，下次重新扫描）。
func (m *Module) closeResults() {
	m.phase, m.res, m.confirm = phaseHome, nil, false
	m.refreshLast()
}

// ── 勾选 ──

// selectable 是能勾选的项（没被挡住）。
func selectable(it core.Item) bool { return it.Blocked == "" }

// recommend 恢复默认勾选（plan 3.8.9 的分档）。
func (m *Module) recommend() {
	m.sel = map[string]bool{}
	for _, it := range m.items() {
		if selectable(it) && it.Default {
			m.sel[it.ID] = true
		}
	}
}

func (m *Module) selectAll() {
	for _, it := range m.items() {
		if selectable(it) {
			m.sel[it.ID] = true
		}
	}
}

func (m *Module) clearAll() { m.sel = map[string]bool{} }

// toggleCategory：全选了就全部取消，否则全部选上。
func (m *Module) toggleCategory(c core.CategoryResult) { m.toggleItems(c.Items) }

func (m *Module) categoryState(c core.CategoryResult) kit.CheckState { return m.itemsState(c.Items) }

// toggleItems：一组项全选了就全部取消，否则全部选上（被挡住的不动）。
func (m *Module) toggleItems(items []core.Item) {
	all := m.itemsState(items) == kit.Checked
	for _, it := range items {
		if selectable(it) {
			m.sel[it.ID] = !all
		}
	}
}

func (m *Module) itemsState(items []core.Item) kit.CheckState {
	n, picked := 0, 0
	for _, it := range items {
		if selectable(it) {
			n++
			if m.sel[it.ID] {
				picked++
			}
		}
	}
	switch {
	case picked == 0:
		return kit.Unchecked
	case picked == n:
		return kit.Checked
	}
	return kit.Mixed
}

func (m *Module) items() []core.Item {
	if m.res == nil {
		return nil
	}
	return m.res.Items()
}

// selected 是勾选的项（按类别顺序）。
func (m *Module) selected() []core.Item {
	var out []core.Item
	for _, it := range m.items() {
		if selectable(it) && m.sel[it.ID] {
			out = append(out, it)
		}
	}
	return out
}

func sizeOf(items []core.Item) int64 {
	var n int64
	for _, it := range items {
		n += it.Size
	}
	return n
}

// ── 清理 ──

type runEvent struct {
	done   int
	o      core.Outcome
	report *core.RunReport
}

// startRun 在用户确认后开始清理勾选的项。
func (m *Module) startRun() {
	plan := m.selected()
	if len(plan) == 0 {
		return
	}
	m.confirm, m.phase = false, phaseCleaning
	m.plan, m.runDone, m.runFreed, m.cancelling, m.report = plan, 0, 0, false, nil
	userCtx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	be := m.be
	task.Stream(m.tasks, "run", func(ctx context.Context, emit func(runEvent)) error {
		// 用户取消和应用退出都会让清理在当前这一项做完后停下；最终结果一定送回界面
		rctx, stop := context.WithCancel(userCtx)
		defer stop()
		unhook := context.AfterFunc(ctx, stop)
		defer unhook()
		rep := be.Run(rctx, plan, func(done, total int, o core.Outcome) { emit(runEvent{done: done, o: o}) })
		emit(runEvent{report: rep})
		return nil
	}, m.onRunEvent, nil)
}

func (m *Module) onRunEvent(e runEvent) {
	if e.report == nil {
		m.runDone = e.done
		m.runFreed += e.o.Freed
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

// finish 从完成页回到首屏。
func (m *Module) finish() {
	m.phase, m.res, m.report, m.plan = phaseHome, nil, nil, nil
	m.refreshLast()
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func remove(ss []string, s string) []string {
	var out []string
	for _, x := range ss {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
