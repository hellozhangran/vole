// Package module 是功能模块与外壳之间的唯一契约。
// 顶部每个 tab = 一个 Module；新增功能 = 新增一个模块并在 app 里注册一行。
package module

import (
	"github.com/egoist/mygo/ui"

	"vole/internal/kit"
	"vole/internal/task"
)

// Host 是外壳提供给模块的能力。
type Host interface {
	// Tasks 返回该模块专属的后台任务管理器，结果自动回到主线程；外壳退出时统一 Close。
	Tasks() *task.Runner
}

// FullPage 是模块可选实现的接口：返回 true 时外壳不画顶部 tab 条，整个窗口交给模块
// （模块自己给左上角红绿灯留位置，并提供拖动窗口的区域）。
type FullPage interface {
	FullPage() bool
}

// Module 是一个顶部 tab 的功能模块。除 Init 外的方法都在主线程调用。
type Module interface {
	ID() string         // 稳定标识："clean" | "apps" | "optimize" | "analyze" | "status"
	Title() string      // tab 文案
	Theme() kit.Theme   // 该 tab 的背景与强调色
	Init(h Host)        // 注册后调用一次（主线程）
	OnActivate()        // 每次切到该 tab 时调用
	OnDeactivate()      // 每次切走该 tab 时调用（如停掉只在可见时才跑的进程）
	View(c *ui.Context) // 主区渲染（tab 条下方的整块区域）
}
