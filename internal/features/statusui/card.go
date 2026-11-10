// Package statusui 是系统状态的卡片骨架、图表和文案，状态 tab 和菜单栏面板共用。
// 只管怎么画，不管数据从哪来；尺寸用 Size 区分（状态 tab 用 Large，菜单栏面板用 Compact）。
package statusui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"vole/internal/kit"
)

// 卡片图标（自绘通用线条）。
var (
	HealthIcon  = kit.LineIcon(`<path d="M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10Z"/>`)
	CPUIcon     = kit.LineIcon(`<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>`)
	GPUIcon     = kit.LineIcon(`<rect x="3" y="5" width="18" height="12" rx="2"/><path d="M8 21h8M12 17v4"/>`)
	MemIcon     = kit.LineIcon(`<rect x="3" y="7" width="18" height="10" rx="2"/><path d="M7 7v10M11 7v10M15 7v10"/>`)
	BatteryIcon = kit.LineIcon(`<rect x="3" y="7" width="16" height="10" rx="2"/><path d="M21 11v2M6 10v4"/>`)
	DiskIcon    = kit.LineIcon(`<rect x="3" y="12" width="18" height="7" rx="2"/><path d="M5.5 12 8 5h8l2.5 7"/><path d="M7 15.5h.01"/>`)
	NetIcon     = kit.LineIcon(`<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>`)
	FanIcon     = kit.LineIcon(`<circle cx="12" cy="12" r="2"/><path d="M12 10c0-4 1-7 4-7s3 4-2 7M14 12c4 0 7 1 7 4s-4 3-7-2M12 14c0 4-1 7-4 7s-3-4 2-7M10 12c-4 0-7-1-7-4s4-3 7 2"/>`)
	ProcIcon    = kit.LineIcon(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 10 3 2-3 2M12 14h4"/>`)
)

// 卡片和图表的颜色。
var (
	CardBG     = ui.RGBA(255, 255, 255, 0.045)
	CardBorder = ui.RGBA(255, 255, 255, 0.08)
	Green      = ui.Hex("#3fae8a")
	Orange     = ui.Hex("#e8964a")
	Gold       = ui.Hex("#d9b25a")
	Teal       = ui.Hex("#4cc3b5")
	Blue       = ui.Hex("#5b8def")
	Red        = ui.Hex("#e5655a")
)

// Dash 是拿不到的值：不能显示成 0。
const Dash = "—"

// Waiting 是慢指标还没到时的提示。
const Waiting = "采集中…"

// Size 是卡片的尺寸档。
type Size struct {
	PadY, PadX float32 // 卡片内边距
	Gap        float32 // 卡片内各行的间距
	Num        float32 // 主数值字号
}

var (
	Large   = Size{PadY: 14, PadX: 16, Gap: 8, Num: kit.TextDisplay}    // 状态 tab
	Compact = Size{PadY: 10, PadX: 10, Gap: 6, Num: kit.TextLargeTitle} // 菜单栏面板
)

// Card 是一张指标卡：标题行（图标 + 标题 + 右上小标签）+ 主体。grow 控制在一行里的宽度占比，高度跟随所在的行。
func Card(c *ui.Context, t kit.Theme, sz Size, key string, icon *ui.SVG, title, tag string, grow float32, body func()) {
	ui.Column(c.Key(key)).Grow(grow).Basis(0).MinWidth(0).FillHeight().Padding(sz.PadY, sz.PadX).Gap(sz.Gap).
		Radius(kit.RadiusL).Background(CardBG).Border(1, CardBorder).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Icon(c, icon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(t.Muted)
			ui.Text(c, title).FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).LetterSpacing(2).TextColor(t.Muted).SingleLine()
			ui.Spacer(c)
			if tag != "" {
				Tag(c, t, tag).Shrink(0) // 标签完整显示，挤不下时让标题让位
			}
		})
		body()
	})
}

// Tag 是灰底等宽小标签（卡片右上角、面板头部的芯片 / 系统版本）。
func Tag(c *ui.Context, t kit.Theme, text string) ui.Element {
	return ui.Text(c, text).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Muted).Padding(1, 6).Radius(kit.RadiusS).Background(t.Surface).SingleLine()
}

// Value 是卡片主数值行：大号数字 + 单位 + 补充。
func Value(c *ui.Context, t kit.Theme, sz Size, num, unit, extra string) {
	ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
		col := t.Text
		if num == Dash {
			col = t.Faint
		}
		ui.Text(c, num).Font(kit.Mono).FontSize(sz.Num).TextColor(col).SingleLine()
		if unit != "" {
			ui.Text(c, unit).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
		}
		if extra != "" {
			ui.Text(c, extra).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		}
	})
}

// Chart 是卡片中部的图表区，占满剩余高度。
func Chart(c *ui.Context, draw func(p *ui.Painter, r ui.Rect)) {
	ui.Box(c).Grow(1).FillWidth().MinHeight(0).Draw(draw)
}

// Note 是图表区里的一句灰字（采集中 / 拿不到）。
func Note(c *ui.Context, t kit.Theme, text string) {
	ui.Box(c).Grow(1).FillWidth().MinHeight(0).Justify(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine()
	})
}

// Footer 是卡片底部的灰色补充信息，空的段落自动略过，其余用「 · 」连起来。
func Footer(c *ui.Context, t kit.Theme, parts ...string) {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	ui.Text(c, strings.Join(keep, " · ")).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine().Ellipsis("…").FillWidth()
}
