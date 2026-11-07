// Package treemap 实现 squarified treemap 布局（Bruls, Huizing, van Wijk 2000）。
// 纯算法，不依赖任何 UI 框架。
package treemap

import "sort"

// Rect 是一个矩形，坐标单位由调用方决定（UI 里是 DIP）。
type Rect struct{ X, Y, W, H float64 }

// Squarify 把 bounds 按 values 的比例切成矩形，返回与 values 同序的结果。
// 值 <= 0 的项得到零矩形；输入顺序无要求，内部按大小降序排布（大块在左上）。
func Squarify(values []float64, bounds Rect) []Rect {
	out := make([]Rect, len(values))
	if bounds.W <= 0 || bounds.H <= 0 {
		return out
	}
	idx := make([]int, 0, len(values))
	total := 0.0
	for i, v := range values {
		if v > 0 {
			idx = append(idx, i)
			total += v
		}
	}
	if total <= 0 {
		return out
	}
	sort.SliceStable(idx, func(a, b int) bool { return values[idx[a]] > values[idx[b]] })

	// 把数值换算成面积
	scale := bounds.W * bounds.H / total
	areas := make([]float64, len(idx))
	for k, i := range idx {
		areas[k] = values[i] * scale
	}

	free := bounds
	start := 0 // 当前行在 idx 中的起点
	for k := 0; k < len(idx); {
		side := min(free.W, free.H)
		// 加入下一项若让本行最差长宽比变好（或本行为空）就加入，否则先把本行排下去
		if k == start || worst(areas[start:k+1], side) <= worst(areas[start:k], side) {
			k++
			continue
		}
		free = layoutRow(idx[start:k], areas[start:k], free, out, false)
		start = k
	}
	layoutRow(idx[start:], areas[start:], free, out, true)
	return out
}

// worst 返回一行面积沿 side 边排布时的最差长宽比（越接近 1 越好）。
func worst(row []float64, side float64) float64 {
	s, lo, hi := 0.0, row[0], row[0]
	for _, a := range row {
		s += a
		lo, hi = min(lo, a), max(hi, a)
	}
	ss, s2 := side*side, s*s
	return max(ss*hi/s2, s2/(ss*lo))
}

// layoutRow 把一行沿 free 的短边排好，返回剩余区域。last 为最后一行时占满剩余空间，消除浮点误差。
func layoutRow(ids []int, areas []float64, free Rect, out []Rect, last bool) Rect {
	s := 0.0
	for _, a := range areas {
		s += a
	}
	if free.W >= free.H {
		// 竖着排一列在左侧
		w := s / free.H
		if last {
			w = free.W
		}
		y := free.Y
		for k, i := range ids {
			h := areas[k] / s * free.H
			if k == len(ids)-1 {
				h = free.Y + free.H - y
			}
			out[i] = Rect{free.X, y, w, h}
			y += h
		}
		return Rect{free.X + w, free.Y, free.W - w, free.H}
	}
	// 横着排一行在顶部
	h := s / free.W
	if last {
		h = free.H
	}
	x := free.X
	for k, i := range ids {
		w := areas[k] / s * free.W
		if k == len(ids)-1 {
			w = free.X + free.W - x
		}
		out[i] = Rect{x, free.Y, w, h}
		x += w
	}
	return Rect{free.X, free.Y + h, free.W, free.H - h}
}
