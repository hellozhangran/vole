// Package status 是「状态」tab：只读的实时系统面板（8 张指标卡片 + 进程表）。
// 只在 tab 可见时运行采集进程：切走就停，切回来再启动。状态只在主线程读写。
package status

import (
	"context"
	"errors"
	"time"

	"vole/internal/core/procs"
	core "vole/internal/core/status"
	"vole/internal/i18n"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/task"
)

const (
	interval     = time.Second     // 采集间隔
	maxRestarts  = 3               // 连续这么多次没收到帧就退出，就停止自动重启，改为提示 + 手动重试
	restartDelay = 2 * time.Second // 意外退出后隔多久自动重启
	historyLen   = 60              // 内存曲线保留的点数（约 1 分钟）
)

// sortCol 是进程表的排序列。
type sortCol int

const (
	byCPU sortCol = iota
	byName
	byPID
	byMem
)

// Module 是状态模块。
type Module struct {
	src     core.Source  // 卡片数据（Mole status）
	procSrc procs.Source // 进程表数据（Vole 自己用 ps 采）
	theme   kit.Theme
	tasks   *task.Runner
	pt      procTable
	act     actions

	active   bool           // tab 可见
	running  bool           // 采集进程在跑
	snap     *core.Snapshot // 最近一帧
	full     bool           // 收到过带慢指标（硬件/电池/进程）的完整帧
	memHist  []float64      // 内存占用 % 历史
	failures int            // 连续「没收到帧就退出」的次数
	err      error          // 不可用，或多次重启失败后的错误；非空时不再自动重启
	lastExit error          // 最近一次意外退出（自动重启中时提示用）

	sortBy sortCol
	desc   bool
}

var _ module.Module = (*Module)(nil)

// New 创建状态模块：src 给卡片数据，procSrc 给进程表。
func New(src core.Source, procSrc procs.Source) *Module {
	return &Module{src: src, procSrc: procSrc, theme: kit.Olive(), desc: true, act: defaultActions()}
}

func (m *Module) ID() string         { return "status" }
func (m *Module) Title() string      { return i18n.T("状态") }
func (m *Module) Theme() kit.Theme   { return m.theme }
func (m *Module) Init(h module.Host) { m.tasks = h.Tasks() }

// OnActivate 切到状态 tab：启动采集（出过不可恢复的错误时等用户点重试）。
func (m *Module) OnActivate() {
	m.active = true
	if !m.running && m.err == nil {
		m.start()
	}
	if !m.pt.running {
		m.startProcs()
	}
}

// OnDeactivate 切走：停掉采集进程和待定的自动重启，保留最后一帧，切回来先显示旧数据。
func (m *Module) OnDeactivate() {
	m.active = false
	m.tasks.Cancel("watch")
	m.tasks.Cancel("restart")
	m.running = false
	m.stopProcs()
}

// start 启动一次采集进程。
func (m *Module) start() {
	m.running = true
	src := m.src
	task.Stream(m.tasks, "watch",
		func(ctx context.Context, emit func(core.Snapshot)) error { return src.Watch(ctx, interval, emit) },
		m.onFrame, m.onExit)
}

// onFrame 在主线程收下一帧。
func (m *Module) onFrame(s core.Snapshot) {
	m.snap = &s
	if s.Model != "" {
		m.full = true
	}
	m.failures, m.lastExit = 0, nil
	m.memHist = append(m.memHist, s.Memory.UsedPercent)
	if len(m.memHist) > historyLen {
		m.memHist = m.memHist[len(m.memHist)-historyLen:]
	}
}

// onExit 采集进程意外退出（主动取消不会走到这里）：组件不可用直接报错；否则隔一会儿自动重启，连续失败太多次就停下等用户重试。
func (m *Module) onExit(err error) {
	m.running = false
	if errors.Is(err, core.ErrUnavailable) {
		m.err = err
		return
	}
	m.lastExit = err
	m.failures++
	if m.failures >= maxRestarts {
		m.err = err
		return
	}
	task.Go(m.tasks, "restart",
		func(ctx context.Context) (struct{}, error) {
			select {
			case <-time.After(restartDelay):
			case <-ctx.Done():
			}
			return struct{}{}, ctx.Err()
		},
		func(_ struct{}, err error) {
			if err == nil && m.active && !m.running && m.err == nil {
				m.start()
			}
		})
}

// retry 用户点「重试」。
func (m *Module) retry() {
	m.err, m.lastExit, m.failures = nil, nil, 0
	if m.active && !m.running {
		m.start()
	}
}

// setSort 点表头：同一列切换升降序；换列时名称默认升序、其他默认降序。
func (m *Module) setSort(col sortCol) {
	if m.sortBy == col {
		m.desc = !m.desc
		return
	}
	m.sortBy, m.desc = col, col != byName
}
