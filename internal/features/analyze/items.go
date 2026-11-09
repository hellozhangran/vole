package analyze

import (
	"sort"
	"time"

	core "vole/internal/core/analyze"
)

// item 是当前视图里的一行 / 一块，侧栏列表和 treemap 共用。
type item struct {
	name, path string
	size       int64
	isDir      bool
	status     core.Status // 空 = 未知（还在算或失败）
	pending    bool        // 计算中
	started    time.Time   // 计算开始时间（pending 时有效；排队中为零）
	failed     string      // 失败原因
	canceled   bool
	cleanable  bool
	lastAccess time.Time
}

// drillable 只有目录能点进去。
func (it item) drillable() bool { return it.isDir }

// viewState 汇总当前视图要画的东西。
type viewState struct {
	root    bool   // 根视图「整个磁盘」
	items   []item // 按大小降序
	report  *core.Report
	job     *job  // 目录视图：当前路径的扫描状态
	pending int   // 根视图：计算中 + 排队中的项数
	done    int   // 根视图：已完成项数
	total   int64 // 已知大小之和
}

// state 从模块状态算出当前视图。
func (m *Module) state() viewState {
	if p := m.current(); p != "" {
		v := viewState{report: m.reports[p], job: m.jobs[p]}
		if v.report != nil {
			v.total = v.report.TotalSize
			for _, e := range v.report.Entries {
				v.items = append(v.items, item{
					name: e.Name, path: e.Path, size: e.Size, isDir: e.IsDir, status: e.Status,
					cleanable: e.Cleanable, lastAccess: e.LastAccess,
				})
			}
		}
		sortItems(v.items)
		return v
	}

	v := viewState{root: true}
	for _, r := range m.roots {
		it := item{name: r.name, path: r.path, isDir: true}
		if rep := m.reports[r.path]; rep != nil {
			it.size, it.status = rep.TotalSize, rep.Status
			v.total += rep.TotalSize
			v.done++
		}
		if j := m.jobs[r.path]; j != nil {
			switch j.state {
			case jobQueued, jobRunning:
				it.pending, it.started = true, j.started
				v.pending++
			case jobFailed:
				it.failed = j.err.Error()
			case jobCanceled:
				it.canceled = true
			}
		}
		v.items = append(v.items, it)
	}
	sortItems(v.items)
	return v
}

// sortItems 已知大小的大项在前；还在算的排在已知项之后，保持原有顺序。
func sortItems(items []item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if (a.status == "") != (b.status == "") {
			return a.status != ""
		}
		return a.size > b.size
	})
}

// anyRunning 当前是否有计算中的项（决定是否每秒刷新已用时、侧栏是奔跑田鼠还是静止插画）。
func (v viewState) anyRunning() bool {
	if v.root {
		return v.pending > 0
	}
	return v.job != nil && (v.job.state == jobRunning || v.job.state == jobQueued) && v.report == nil
}
