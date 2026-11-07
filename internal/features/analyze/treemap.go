package analyze

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"vole/internal/kit"
	"vole/internal/kit/treemap"
)

const (
	maxBlocks = 40 // 超过的小项合并成「其他」
	blockGap  = 3  // 块四周各留 3，相邻块间距 6
)

// block 是 treemap 里的一块：一个条目，或合并后的「其他 N 项」。
type block struct {
	it    item
	other int // >0 表示合并块，含这么多项
	value float64
}

// blocks 把条目转成块：大小为 0 的不画；还在算的根项给一个占位面积，让它先占个位置。
func blocks(items []item) []block {
	var known int64
	var n int
	for _, it := range items {
		if it.size > 0 && !it.pending {
			known += it.size
			n++
		}
	}
	placeholder := float64(1)
	if n > 0 {
		placeholder = float64(known) / float64(n) / 2
	}

	var out []block
	var rest block
	for _, it := range items {
		v := float64(it.size)
		if it.pending {
			v = placeholder
		}
		if v <= 0 {
			continue
		}
		if len(out) < maxBlocks {
			out = append(out, block{it: it, value: v})
			continue
		}
		rest.other++
		rest.value += v
		rest.it.size += it.size
	}
	if rest.other > 0 {
		rest.it.name = fmt.Sprintf("其他 %d 项", rest.other)
		out = append(out, rest)
	}
	return out
}

// treemap 画占用图：squarified 布局，圆角带间距，点块下钻，右键在 Finder 中显示。
func (m *Module) treemap(c *ui.Context, t kit.Theme, v viewState) {
	box := ui.Box(c.Key("treemap")).Grow(1).FillWidth().MinHeight(0)
	// 用上一帧的尺寸排布；第一帧还没有尺寸时按窗口估一个，再请求重画一帧
	b := box.LayoutBounds()
	w, h := b.W, b.H
	if w <= 0 || h <= 0 {
		cw, ch := c.Size()
		w, h = max(cw-sidebarWidth-2*kit.Gutter-12, 100), max(ch-kit.TopBarH-32-10-kit.Gutter, 100) // 顶栏、标题栏 32、间距 10
		c.Invalidate()
	}
	bs := blocks(v.items)
	values := make([]float64, len(bs))
	for i, bl := range bs {
		values[i] = bl.value
	}
	rects := treemap.Squarify(values, treemap.Rect{W: float64(w), H: float64(h)})

	box.Children(func() {
		for i, bl := range bs {
			r := rects[i]
			color := t.Blocks[i%len(t.Blocks)]
			if bl.other > 0 {
				color = ui.RGBA(255, 255, 255, 0.10)
			}
			m.block(c, t, bl, color, float32(r.X), float32(r.Y), float32(r.W), float32(r.H))
		}
	})
}

func (m *Module) block(c *ui.Context, t kit.Theme, bl block, color ui.Color, x, y, w, h float32) {
	x, y, w, h = x+blockGap, y+blockGap, w-2*blockGap, h-2*blockGap
	if w <= 1 || h <= 1 {
		return
	}
	it := bl.it
	key := it.path
	if bl.other > 0 {
		key = "other"
	}
	e := ui.ButtonBase(c.Key("block-" + key)).Label("块 " + it.name).Absolute().Left(x).Top(y).Width(w).Height(h).
		Radius(min(kit.RadiusM, w/4, h/4)).Background(color).Clip().Center().Tooltip(it.name + "  " + blockSize(c, m, it))
	if it.pending {
		e.Background(color.Alpha(0.55)) // 计算中：半透明，和已出结果的块区分开
	}
	if e.Hovered() && bl.other == 0 {
		e.Border(2, ui.RGBA(255, 255, 255, 0.6))
	}
	if bl.other == 0 {
		m.revealMenu(e, it.path)
		if it.drillable() {
			path := it.path
			e.OnClick(func() { m.drill(path) })
		}
	}
	ink := ui.Hex("#fffaf2")
	e.Children(func() {
		switch {
		case w >= 84 && h >= 42:
			ui.Column(c).AlignItems(ui.Center).Gap(2).Padding(0, 8).MaxWidth(w).Children(func() {
				ui.Row(c).Gap(5).MaxWidth(w - 16).Children(func() {
					if it.isDir && bl.other == 0 {
						ui.Icon(c, kit.FolderIcon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(ink)
					}
					ui.Text(c, it.name).FontSize(kit.TextBody).FontWeight(kit.WeightSemibold).TextColor(ink).SingleLine().Ellipsis("…").MinWidth(0)
				})
				ui.Text(c, blockSize(c, m, it)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(ink.Alpha(0.85)).SingleLine()
			})
		case w >= 44 && h >= 22:
			ui.Text(c, it.name).FontSize(kit.TextCaption).TextColor(ink.Alpha(0.9)).SingleLine().Ellipsis("…").MaxWidth(w - 8)
		}
	})
}

// blockSize 是块上的第二行：计算中显示已用时，否则显示大小。
func blockSize(c *ui.Context, m *Module, it item) string {
	if it.pending {
		return "计算中 · " + m.elapsed(c, it.started)
	}
	return sizeLabel(it.size, it.status)
}
