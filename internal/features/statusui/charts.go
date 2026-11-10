package statusui

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// Bars 每个值（0–100）一根柱子，底部对齐，带浅色底槽。
func Bars(p *ui.Painter, r ui.Rect, values []float64, color ui.Color) {
	n := len(values)
	if n == 0 || r.W <= 0 || r.H <= 0 {
		return
	}
	gap := float32(4)
	w := (r.W - gap*float32(n-1)) / float32(n)
	for i, v := range values {
		x := r.X + float32(i)*(w+gap)
		p.Fill(ui.Rect{X: x, Y: r.Y, W: w, H: r.H}, ui.RGBA(255, 255, 255, 0.04), 3)
		h := max(r.H*float32(Clamp01(v/100)), 2)
		p.FillGradient(ui.Rect{X: x, Y: r.Y + r.H - h, W: w, H: h},
			ui.LinearGradient{From: color.Mix(ui.Hex("#ffffff"), 0.15), To: color, Angle: 180}, 3)
	}
}

// Meter 一根横向进度（GPU 有读数时用）。
func Meter(p *ui.Painter, r ui.Rect, frac float64, color ui.Color) {
	y := r.Y + r.H/2 - 4
	p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: 8}, ui.RGBA(255, 255, 255, 0.06), 4)
	p.Fill(ui.Rect{X: r.X, Y: y, W: max(r.W*float32(Clamp01(frac)), 8), H: 8}, color, 4)
}

// UsageBar 磁盘已用比例：红到蓝的渐变横条。
func UsageBar(p *ui.Painter, r ui.Rect, frac float64) {
	y := r.Y + r.H/2 - 6
	p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: 12}, ui.RGBA(255, 255, 255, 0.06), 6)
	p.FillGradient(ui.Rect{X: r.X, Y: y, W: max(r.W*float32(Clamp01(frac)), 12), H: 12},
		ui.LinearGradient{From: Red, To: Blue, Angle: 90, Oklab: true}, 6)
}

// Area 面积图：values 按 [0, top] 归一化，最新的在右边；capacity 是横轴能放的点数，点还没攒满时先铺满整个宽度。
func Area(p *ui.Painter, r ui.Rect, values []float64, capacity int, top float64, color ui.Color) {
	if len(values) == 1 {
		values = []float64{values[0], values[0]} // 只有一个点时画成一条平线
	}
	pts := Points(r, values, min(max(len(values), 2), capacity), top)
	if len(pts) < 2 {
		return
	}
	var fill ui.Path
	fill.MoveTo(pts[0][0], r.Y+r.H)
	for _, pt := range pts {
		fill.LineTo(pt[0], pt[1])
	}
	fill.LineTo(pts[len(pts)-1][0], r.Y+r.H).Close()
	p.FillPathGradient(&fill, ui.LinearGradient{From: color.Alpha(0.35), To: color.Alpha(0), Angle: 180})
	p.StrokePolyline(pts, 1.6, color)
}

// Lines 双折线（网络下行 / 上行），两条共用一个纵轴上限。
func Lines(p *ui.Painter, r ui.Rect, a, b []float64, ca, cb ui.Color) {
	top := 1.0
	for _, v := range append(append([]float64{}, a...), b...) {
		top = max(top, v)
	}
	n := max(len(a), len(b), 2)
	if pts := Points(r, a, n, top*1.1); len(pts) >= 2 {
		p.StrokePolyline(pts, 1.6, ca)
	}
	if pts := Points(r, b, n, top*1.1); len(pts) >= 2 {
		p.StrokePolyline(pts, 1.6, cb)
	}
}

// Points 把一串值映射成 r 里的折线点：横向按 capacity 等分，最新的点贴右边。
func Points(r ui.Rect, values []float64, capacity int, top float64) [][2]float32 {
	if len(values) == 0 || capacity < 2 || top <= 0 {
		return nil
	}
	if len(values) > capacity {
		values = values[len(values)-capacity:]
	}
	step := r.W / float32(capacity-1)
	start := r.X + r.W - step*float32(len(values)-1)
	out := make([][2]float32, len(values))
	for i, v := range values {
		out[i] = [2]float32{start + step*float32(i), r.Y + r.H - r.H*float32(Clamp01(v/top))}
	}
	return out
}

// Ring 环形进度：从 12 点钟方向顺时针。
func Ring(p *ui.Painter, r ui.Rect, frac float64, color ui.Color) {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	rad := min(r.W, r.H)/2 - 3
	var track ui.Path
	track.Circle(cx, cy, rad)
	p.StrokePath(&track, 4, ui.RGBA(255, 255, 255, 0.08))
	frac = Clamp01(frac)
	if frac <= 0 {
		return
	}
	var arc ui.Path
	steps := max(int(frac*72), 2)
	for i := 0; i <= steps; i++ {
		a := -math.Pi/2 + 2*math.Pi*frac*float64(i)/float64(steps)
		x, y := cx+rad*float32(math.Cos(a)), cy+rad*float32(math.Sin(a))
		if i == 0 {
			arc.MoveTo(x, y)
		} else {
			arc.LineTo(x, y)
		}
	}
	p.StrokePath(&arc, 4, color)
}

// Clamp01 把 v 限制在 [0, 1]。
func Clamp01(v float64) float64 { return max(0, min(1, v)) }
