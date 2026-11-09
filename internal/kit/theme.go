// Package kit 是 Vole 的主题与通用组件（依赖 mygo）。不持任何业务状态。
package kit

import "github.com/egoist/mygo/ui"

// Theme 是一个 tab 的配色：背景渐变、文字层级、半透明面与 treemap 色板。
type Theme struct {
	BgTop, BgBottom          ui.Color   // 背景渐变：顶部偏主题色，往下变暗
	Accent                   ui.Color   // 强调色：计算中、状态点
	OK                       ui.Color   // 版本徽章的绿点
	Text, Muted, Faint       ui.Color   // 文字三级
	Surface, Hover, Selected ui.Color   // 半透明白面
	TabBar                   ui.Color   // 顶部胶囊 tab 条底色
	Blocks                   []ui.Color // treemap 块色，按序循环
}

// base 是所有主题共用的文字与面层级（深色背景上的白色半透明层）。
func base(top, bottom ui.Color) Theme {
	return Theme{
		BgTop: top, BgBottom: bottom,
		Accent:   ui.Hex("#f0a04b"),
		OK:       ui.Hex("#34d399"),
		Text:     ui.Hex("#f5f1ec"),
		Muted:    ui.RGBA(245, 241, 236, 0.62),
		Faint:    ui.RGBA(245, 241, 236, 0.38),
		Surface:  ui.RGBA(255, 255, 255, 0.07),
		Hover:    ui.RGBA(255, 255, 255, 0.06),
		Selected: ui.RGBA(255, 255, 255, 0.10),
		TabBar:   ui.RGBA(255, 255, 255, 0.07),
	}
}

// Earth 暖棕：分析 tab。块色是大地色系：沙黄、赭、砖红、棕。
func Earth() Theme {
	t := base(ui.Hex("#45291b"), ui.Hex("#1b181b"))
	t.Blocks = []ui.Color{
		ui.Hex("#c2a26f"), ui.Hex("#b8854b"), ui.Hex("#a1533c"), ui.Hex("#8a5e42"),
		ui.Hex("#a97b4f"), ui.Hex("#b46a44"), ui.Hex("#7d5039"), ui.Hex("#c4ab7f"),
		ui.Hex("#94683f"), ui.Hex("#8f4a35"),
	}
	return t
}

// Ocean 深蓝：清理 tab。
func Ocean() Theme {
	t := base(ui.Hex("#102154"), ui.Hex("#15151f"))
	return t
}

// Wine 暗酒红：软件 tab（参照 Mole 截图 13）。
func Wine() Theme {
	t := base(ui.Hex("#3d1a1d"), ui.Hex("#18141a"))
	return t
}

// Violet 暗紫：备用主题。
func Violet() Theme {
	t := base(ui.Hex("#2c1d4f"), ui.Hex("#16141f"))
	return t
}

// Teal 青绿：优化 tab。
func Teal() Theme {
	t := base(ui.Hex("#0f3b3c"), ui.Hex("#141a1c"))
	return t
}

// Olive 暗橄榄 / 暗金棕：状态 tab（顶部偏橄榄黄，往下变暗）。
func Olive() Theme {
	t := base(ui.Hex("#4a3d18"), ui.Hex("#1b1a17"))
	return t
}

// Graphite 石墨：备用主题。
func Graphite() Theme {
	t := base(ui.Hex("#2b2f36"), ui.Hex("#151618"))
	return t
}

// Mono 是数字用的等宽字体。
const Mono = "monospace"
