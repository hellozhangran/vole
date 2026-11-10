// Package analyze 是「分析」tab：根视图逐项扫描精选顶层目录，点击下钻，面包屑返回。
// 状态只在主线程读写；扫描经 task.Runner 在后台跑，结果回主线程。
package analyze

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	core "vole/internal/core/analyze"
	"vole/internal/core/sysenv"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/task"
)

// maxParallel 是根视图同时跑的 analyze 进程数上限。
const maxParallel = 2

type jobState int

const (
	jobQueued   jobState = iota // 根视图排队等并发名额
	jobRunning                  // analyze 进程在跑
	jobDone                     // 结果已在 reports 里
	jobFailed                   // 出错
	jobCanceled                 // 用户取消
)

// job 是一个路径的扫描状态。
type job struct {
	state   jobState
	started time.Time // 进入 running 的时间
	err     error
}

// root 是根视图「整个磁盘」里的一项。
type root struct{ name, path string }

// Module 是分析模块。
type Module struct {
	src   core.Source
	env   sysenv.Info
	theme kit.Theme
	tasks *task.Runner
	roots []root

	reports map[string]*core.Report // 内存里已完成的结果：返回上级 / 再次进入都不重扫
	jobs    map[string]*job
	stack   []string // 下钻路径；空 = 根视图
	started bool     // 首次激活后才开扫

	showLarge bool // 目录视图：大文件列表 / treemap
	disk      sysenv.Disk
	diskOK    bool

	// 以下可在测试中替换
	now          func() time.Time
	reveal       func(path string)
	openSettings func()
	diskUsage    func(string) (sysenv.Disk, error)
}

var _ module.Module = (*Module)(nil)

// New 创建分析模块。根视图列 /Applications、~、/Library、/opt/homebrew（存在才列）。
func New(src core.Source, env sysenv.Info) *Module {
	var roots []root
	for _, p := range []string{"/Applications", env.Home, "/Library", "/opt/homebrew"} {
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			roots = append(roots, root{name: strings.TrimPrefix(p, "/"), path: p})
		}
	}
	return newModule(src, env, roots)
}

func newModule(src core.Source, env sysenv.Info, roots []root) *Module {
	return &Module{
		src: src, env: env, theme: kit.Earth(), roots: roots,
		reports: map[string]*core.Report{}, jobs: map[string]*job{},
		now:    time.Now,
		reveal: func(p string) { mygo.Shell.ShowItemInFolder(p) },
		openSettings: func() {
			_ = mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")
		},
		diskUsage: sysenv.DiskUsage,
	}
}

func (m *Module) ID() string         { return "analyze" }
func (m *Module) Title() string      { return "分析" }
func (m *Module) Theme() kit.Theme   { return m.theme }
func (m *Module) Init(h module.Host) { m.tasks = h.Tasks() }

// OnDeactivate 切走时扫描照常在后台跑，状态保留。
func (m *Module) OnDeactivate() {}

// OnActivate 第一次切到分析 tab 时开始扫根视图；之后切回来保持原状态。
func (m *Module) OnActivate() {
	m.refreshDisk()
	if m.started {
		return
	}
	m.started = true
	m.queueRoots()
}

// ── 调度 ──

func (m *Module) isRoot(path string) bool {
	for _, r := range m.roots {
		if r.path == path {
			return true
		}
	}
	return false
}

// queueRoots 把所有根项排队并按并发上限启动。
func (m *Module) queueRoots() {
	for _, r := range m.roots {
		m.jobs[r.path] = &job{state: jobQueued}
	}
	m.pump()
}

// pump 在并发上限内启动排队中的根项。
func (m *Module) pump() {
	running := 0
	for _, r := range m.roots {
		if j := m.jobs[r.path]; j != nil && j.state == jobRunning {
			running++
		}
	}
	for _, r := range m.roots {
		if running >= maxParallel {
			return
		}
		if j := m.jobs[r.path]; j != nil && j.state == jobQueued {
			m.start(r.path)
			running++
		}
	}
}

// start 立即为 path 启动一次扫描（不受并发上限约束，用户主动下钻/刷新时用）。
func (m *Module) start(path string) {
	m.jobs[path] = &job{state: jobRunning, started: m.now()}
	src := m.src
	task.Go(m.tasks, "scan:"+path,
		func(ctx context.Context) (core.Report, error) { return src.Scan(ctx, core.Request{Path: path}) },
		func(r core.Report, err error) { m.finish(path, r, err) })
}

// finish 在主线程收下扫描结果。
func (m *Module) finish(path string, r core.Report, err error) {
	j := m.jobs[path]
	if j == nil {
		return
	}
	switch {
	case errors.Is(err, context.Canceled):
		j.state = jobCanceled
	case err != nil:
		j.state, j.err = jobFailed, err
	default:
		j.state = jobDone
		m.reports[path] = &r
	}
	m.refreshDisk()
	m.pump()
}

// cancel 取消 path 的扫描（排队中的也算），结果会被丢弃。
func (m *Module) cancel(path string) {
	j := m.jobs[path]
	if j == nil || (j.state != jobRunning && j.state != jobQueued) {
		return
	}
	m.tasks.Cancel("scan:" + path)
	j.state = jobCanceled
}

// ensure 保证 path 有结果或正在扫：内存里有就直接用，不重扫。
func (m *Module) ensure(path string) {
	if m.reports[path] != nil {
		return
	}
	if j := m.jobs[path]; j != nil && (j.state == jobRunning || j.state == jobQueued) {
		if j.state == jobQueued {
			m.start(path) // 用户点进了排队中的根项：插队
		}
		return
	}
	m.start(path)
}

// dropStale 取消已经不在导航栈上的非根扫描，避免后台堆积 analyze 进程。
func (m *Module) dropStale() {
	onStack := map[string]bool{}
	for _, p := range m.stack {
		onStack[p] = true
	}
	for p, j := range m.jobs {
		if !m.isRoot(p) && !onStack[p] && j.state == jobRunning {
			m.tasks.Cancel("scan:" + p)
			delete(m.jobs, p) // 下次进入重新扫
		}
	}
}

// ── 导航与操作（由视图调用） ──

func (m *Module) current() string {
	if len(m.stack) == 0 {
		return ""
	}
	return m.stack[len(m.stack)-1]
}

// drill 下钻进 path。
func (m *Module) drill(path string) {
	m.stack = append(m.stack, path)
	m.showLarge = false
	m.ensure(path)
	m.dropStale()
}

// goTo 回到导航栈第 depth 层（0 = 根视图），用内存里的结果，不重扫。
func (m *Module) goTo(depth int) {
	if depth < 0 || depth >= len(m.stack)+1 {
		return
	}
	m.stack = m.stack[:depth]
	m.showLarge = false
	m.dropStale()
	if p := m.current(); p != "" {
		m.ensure(p)
	}
}

// refresh 重扫当前视图。
func (m *Module) refresh() {
	if p := m.current(); p != "" {
		m.cancel(p)
		delete(m.reports, p)
		m.start(p)
		return
	}
	for _, r := range m.roots {
		m.cancel(r.path)
		delete(m.reports, r.path)
	}
	m.queueRoots()
}

// cancelCurrent 取消当前视图里所有在跑/排队的扫描。
func (m *Module) cancelCurrent() {
	if p := m.current(); p != "" {
		m.cancel(p)
		return
	}
	for _, r := range m.roots {
		m.cancel(r.path)
	}
}

// brokenErr 返回「扫描器本身不可用」的错误（任一扫描遇到即算），否则 nil。
func (m *Module) brokenErr() error {
	for _, j := range m.jobs {
		if j.state == jobFailed && errors.Is(j.err, core.ErrScannerUnavailable) {
			return j.err
		}
	}
	return nil
}

func (m *Module) refreshDisk() {
	if m.diskUsage == nil {
		return
	}
	d, err := m.diskUsage("/")
	m.disk, m.diskOK = d, err == nil
}

// displayName 是路径在面包屑和列表里的名字：根项用根名（如 Users/alice），其余取最后一段。
func (m *Module) displayName(path string) string {
	for _, r := range m.roots {
		if r.path == path {
			return r.name
		}
	}
	return filepath.Base(path)
}
