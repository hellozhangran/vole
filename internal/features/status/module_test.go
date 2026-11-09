package status

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vole/internal/core/procs"
	core "vole/internal/core/status"
	"vole/internal/kit"
	"vole/internal/task"
)

// step 是假数据源的一步：推一帧，或者让进程退出。
type step struct {
	snap *core.Snapshot
	exit error
}

// fakeSource 每次 Watch 都从 steps 里取：推帧或退出；ctx 取消时返回 ctx.Err()。
type fakeSource struct {
	mu       sync.Mutex
	calls    int
	canceled int
	steps    chan step
}

func (f *fakeSource) Watch(ctx context.Context, _ time.Duration, emit func(core.Snapshot)) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			f.mu.Lock()
			f.canceled++
			f.mu.Unlock()
			return ctx.Err()
		case s := <-f.steps:
			if s.exit != nil {
				return s.exit
			}
			emit(*s.snap)
		}
	}
}

func (f *fakeSource) counts() (calls, canceled int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.canceled
}

func fastFrame() *core.Snapshot {
	return &core.Snapshot{
		At: time.Date(2026, 11, 7, 5, 13, 50, 0, time.Local), Uptime: (29*24 + 12) * time.Hour, Health: 82, HealthNote: "Good",
		CPU:    core.CPU{Usage: 28.4, PerCore: []float64{40, 22, 35, 18, 60, 12, 8, 30}, Load1: 3.8, Cores: 8},
		Memory: core.Memory{Used: 21.19e9, Total: 34.36e9, UsedPercent: 61.7, SwapUsed: 16.8e9},
		Disks:  []core.Disk{{Mount: "/", Used: 457e9, Total: 494e9, UsedPercent: 92.5}},
		Net:    []core.Iface{{Name: "en0", IP: "10.0.0.2", Rate: core.Rate{In: 1.5e6, Out: 91e3}}},
		NetHistory: core.NetHistory{
			In:  []float64{0.2e6, 0.5e6, 0.3e6, 1.2e6, 0.4e6, 0.6e6, 1.5e6},
			Out: []float64{20e3, 40e3, 30e3, 90e3, 50e3, 60e3, 91e3},
		},
	}
}

func fullFrame() *core.Snapshot {
	s := fastFrame()
	s.Model, s.Chip, s.OS = "MacBook Pro", "Apple M1 Pro", "macOS 14.6.1"
	s.Health, s.HealthNote = 69, "Good: High CPU, Disk Almost Full"
	s.CPU.PCores, s.CPU.ECores = 6, 2
	s.Memory.Pressure = "normal"
	s.GPUs = []core.GPU{{Name: "Apple M1 Pro", Usage: -1, Cores: 14}}
	s.Batteries = []core.Battery{{Percent: 44, Status: "discharging", TimeLeft: "1:03", Health: "Good", Cycles: 327, Capacity: 81}}
	s.Thermal = core.Thermal{BatteryTemp: 31}
	s.TopProcs = []core.Proc{
		{PID: 93944, Name: "replayd", CPU: 80.5, MemBytes: 11e6},
		{PID: 64297, Name: "ChatGPT", CPU: 19.4, MemBytes: 359e6},
		{PID: 125, Name: "opendirectoryd", CPU: 8.1, MemBytes: 17e6},
		{PID: 152, Name: "WindowServer", CPU: 7.6, MemBytes: 138e6},
		{PID: 85, Name: "logd", CPU: 4.7, MemBytes: 38e6},
	}
	s.Alerts = []int{93944}
	return s
}

type harness struct {
	t    *testing.T
	m    *Module
	src  *fakeSource
	ps   *fakeProcs
	tt   *ui.Tester
	main chan func()
}

type testHost struct{ r *task.Runner }

func (h testHost) Tasks() *task.Runner { return h.r }

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, src: &fakeSource{steps: make(chan step)}, ps: &fakeProcs{lists: make(chan []procs.Proc)}, main: make(chan func(), 64)}
	h.m = New(h.src, h.ps)
	h.m.act = testActions(h)
	r := task.NewRunner(func(fn func()) { h.main <- fn })
	t.Cleanup(r.Close)
	h.m.Init(testHost{r})
	th := h.m.Theme()
	// 套上和外壳一样的主题背景，截图才像真窗口
	h.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().TextColor(th.Text).FontSize(14).
			LinearGradient(ui.LinearGradient{From: th.BgTop, To: th.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
			Children(func() { h.m.View(c) })
	}, 1200, 700)
	return h
}

// push 推一步给假数据源，并等它的结果回到主线程。
func (h *harness) push(s step, results int) {
	h.t.Helper()
	select {
	case h.src.steps <- s:
	case <-time.After(3 * time.Second):
		h.t.Fatal("数据源没有在 Watch")
	}
	h.settle(results)
}

func (h *harness) settle(n int) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		select {
		case fn := <-h.main:
			fn()
		case <-time.After(5 * time.Second):
			h.t.Fatalf("等第 %d 个回调超时", i+1)
		}
	}
	h.tt.Frame()
}

func (h *harness) want(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if !h.tt.HasText(s) {
			h.t.Fatalf("界面上应有 %q；当前文字：%q", s, h.tt.Texts())
		}
	}
}

func (h *harness) wantNot(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if h.tt.HasText(s) {
			h.t.Fatalf("界面上不应有 %q", s)
		}
	}
}

func (h *harness) snapshot(name string) {
	dir := os.Getenv("VOLE_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	_ = png.Encode(f, h.tt.Image())
}

func (h *harness) waitCalls(want int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if calls, _ := h.src.counts(); calls >= want {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("Watch 应被调用 %d 次", want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFramesFillCardsProgressively(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.tt.Frame()
	h.want("正在读取系统状态…")
	h.snapshot("01-loading")

	// 首帧（fast）：CPU/内存/磁盘/网络有数，慢指标卡片显示采集中，不整页等
	h.push(step{snap: fastFrame()}, 1)
	h.want("28", "62", "37.0", "1.50", "采集中…")
	h.wantNot("正在读取系统状态…", "0°C")
	h.snapshot("02-first-frame")

	// 完整帧：电池、GPU 到齐；进程表来自 Vole 自己的采集；拿不到的值显示「—」，不显示 0
	h.push(step{snap: fullFrame()}, 1)
	h.pushProcs(sampleProcs(50))
	h.want("69", "良好", "CPU 负载偏高，磁盘快满了", "44", "可用 1:03", "健康 81%", "327 次循环 · 31°C · 状况 Good",
		"占用需要管理员权限才能读取", "当前权限下读不到风扇转速", "Apple M1 Pro", "压力 正常",
		"replayd")
	h.wantNot("采集中…", "0 RPM", "0°C")
	h.snapshot("03-full")
}

func TestStopsWhenHiddenAndResumes(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{snap: fullFrame()}, 1)

	h.m.OnDeactivate()
	deadline := time.Now().Add(3 * time.Second)
	for _, canceled := h.src.counts(); canceled < 1; _, canceled = h.src.counts() {
		if time.Now().After(deadline) {
			t.Fatal("切走后采集应被取消")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.tt.Frame()
	h.want("69") // 切回来之前先显示最后一帧

	h.m.OnActivate()
	h.waitCalls(2)
	h.push(step{snap: fastFrame()}, 1)
	h.want("28")
}

func TestUnexpectedExitRestartsThenGivesUp(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{snap: fullFrame()}, 1)

	// 意外退出 → 提示正在重启 → 2 秒后自动重启
	h.push(step{exit: &core.ExitError{ExitCode: 3, Stderr: "boom"}}, 1)
	h.want("状态采集意外退出，正在重启…", "69")
	h.snapshot("05-restarting")
	h.settle(1) // 重启定时器到点
	h.waitCalls(2)

	// 连续第 2 次没帧就退出 → 再重启一次；第 3 次 → 停止自动重启，显示重试
	h.push(step{exit: &core.ExitError{ExitCode: 3}}, 1)
	h.settle(1)
	h.waitCalls(3)
	h.push(step{exit: &core.ExitError{ExitCode: 3, Stderr: "boom"}}, 1)
	h.want("重试", "状态采集已停止：状态采集意外退出（已收到 0 帧，退出码 3）：boom")
	time.Sleep(restartDelay + 500*time.Millisecond)
	if calls, _ := h.src.counts(); calls != 3 {
		t.Fatalf("放弃后不应再重启：%d", calls)
	}
	h.snapshot("06-gave-up")

	// 重试：清零计数重新启动，收到帧后提示条消失
	if err := h.tt.Click("重试"); err != nil {
		t.Fatal(err)
	}
	h.waitCalls(4)
	h.push(step{snap: fastFrame()}, 1)
	h.wantNot("重试")
}

func TestUnavailableShowsError(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.push(step{exit: &core.ExitError{ExitCode: -1, Err: errors.Join(core.ErrUnavailable, errors.New("找不到 status 二进制"))}}, 1)
	h.want("状态组件不可用", "重试")
	h.snapshot("07-unavailable")
	if calls, _ := h.src.counts(); calls != 1 {
		t.Fatalf("组件不可用不应自动重启：%d", calls)
	}
}

func TestHealthText(t *testing.T) {
	level, issues := healthText("Needs Attention: High Memory, Something New")
	if level != "需要关注" || issues != "内存占用偏高，Something New" {
		t.Fatalf("got %q %q", level, issues)
	}
	if level, issues := healthText("Excellent"); level != "优秀" || issues != "没有发现问题" {
		t.Fatalf("got %q %q", level, issues)
	}
}

// TestHealthArt：健康度卡片按等级换插画；三档各截一张（设了 VOLE_SNAPSHOT_DIR 时）。
func TestHealthArt(t *testing.T) {
	for _, c := range []struct {
		note  string
		score int
		want  kit.ArtName
	}{
		{"Excellent", 92, kit.ArtHealthGood}, {"Good: High CPU", 69, kit.ArtHealthGood},
		{"Fair: High Memory", 50, kit.ArtHealthFair}, {"Needs Attention: Disk Almost Full", 30, kit.ArtHealthPoor},
		{"", 70, kit.ArtHealthGood}, {"", 50, kit.ArtHealthFair}, {"", 10, kit.ArtHealthPoor}, {"", -1, kit.ArtHealthGood},
	} {
		if got := healthArt(c.note, c.score); got != c.want {
			t.Fatalf("%q %d：%s", c.note, c.score, got)
		}
	}
	for _, lv := range []struct {
		name, note string
		score      int
	}{{"good", "Good: High CPU, Disk Almost Full", 69}, {"fair", "Fair: High Memory, Memory Pressure", 52}, {"poor", "Needs Attention: Overheating, Disk Almost Full", 31}} {
		h := newHarness(t)
		h.m.OnActivate()
		s := fullFrame()
		s.Health, s.HealthNote = lv.score, lv.note
		h.push(step{snap: s}, 1)
		h.snapshot("health-" + lv.name)
	}
}
