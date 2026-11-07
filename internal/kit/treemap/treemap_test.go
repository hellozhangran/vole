package treemap

import (
	"math"
	"math/rand"
	"testing"
)

const eps = 1e-6

func area(r Rect) float64 { return r.W * r.H }

// check 校验通用性质：面积成比例、在边界内、互不重叠。
func check(t *testing.T, values []float64, b Rect, out []Rect) {
	t.Helper()
	if len(out) != len(values) {
		t.Fatalf("长度不符：%d vs %d", len(out), len(values))
	}
	total := 0.0
	for _, v := range values {
		if v > 0 {
			total += v
		}
	}
	for i, r := range out {
		if values[i] <= 0 {
			if r != (Rect{}) {
				t.Fatalf("值 %v 应得零矩形，got %+v", values[i], r)
			}
			continue
		}
		want := values[i] / total * area(b)
		if math.Abs(area(r)-want) > 1e-6*area(b) {
			t.Fatalf("第 %d 项面积 %v，期望 %v", i, area(r), want)
		}
		if r.X < b.X-eps || r.Y < b.Y-eps || r.X+r.W > b.X+b.W+eps || r.Y+r.H > b.Y+b.H+eps {
			t.Fatalf("第 %d 项越界：%+v", i, r)
		}
		for j := i + 1; j < len(out); j++ {
			o := out[j]
			if values[j] <= 0 {
				continue
			}
			ix := math.Min(r.X+r.W, o.X+o.W) - math.Max(r.X, o.X)
			iy := math.Min(r.Y+r.H, o.Y+o.H) - math.Max(r.Y, o.Y)
			if ix > eps && iy > eps {
				t.Fatalf("第 %d 和 %d 项重叠：%+v %+v", i, j, r, o)
			}
		}
	}
}

func TestPaperExample(t *testing.T) {
	// 论文里的例子：6×4 的区域，值 6 6 4 3 2 2 1
	values := []float64{6, 6, 4, 3, 2, 2, 1}
	b := Rect{0, 0, 6, 4}
	out := Squarify(values, b)
	check(t, values, b, out)
	for i, r := range out {
		if ar := math.Max(r.W/r.H, r.H/r.W); ar > 3 {
			t.Fatalf("第 %d 项长宽比 %.2f 太扁", i, ar)
		}
	}
	// 最大的两项排在左侧一列
	if out[0].X != 0 || out[1].X != 0 {
		t.Fatalf("最大项应在最左：%+v %+v", out[0], out[1])
	}
}

func TestOrderAndZeros(t *testing.T) {
	values := []float64{1, 0, 10, -3, 5}
	b := Rect{10, 20, 300, 200}
	out := Squarify(values, b)
	check(t, values, b, out)
	if out[2].X != b.X || out[2].Y != b.Y {
		t.Fatalf("最大项应在左上角：%+v", out[2])
	}
}

func TestDegenerate(t *testing.T) {
	if out := Squarify(nil, Rect{0, 0, 10, 10}); len(out) != 0 {
		t.Fatal("空输入应返回空")
	}
	for _, out := range [][]Rect{
		Squarify([]float64{0, 0}, Rect{0, 0, 10, 10}),
		Squarify([]float64{1, 2}, Rect{0, 0, 0, 10}),
	} {
		for _, r := range out {
			if r != (Rect{}) {
				t.Fatalf("应全为零矩形：%+v", out)
			}
		}
	}
	single := Squarify([]float64{42}, Rect{1, 2, 30, 40})
	if single[0] != (Rect{1, 2, 30, 40}) {
		t.Fatalf("单项应占满：%+v", single[0])
	}
}

func TestRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for n := 1; n <= 60; n++ {
		values := make([]float64, n)
		for i := range values {
			values[i] = math.Pow(10, rng.Float64()*6) // 跨 6 个数量级，模拟真实目录
		}
		b := Rect{0, 0, 800 + rng.Float64()*400, 400 + rng.Float64()*400}
		check(t, values, b, Squarify(values, b))
	}
}
