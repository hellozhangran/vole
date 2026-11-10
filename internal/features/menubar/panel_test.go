// SPDX-License-Identifier: GPL-3.0-only

package menubar

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vole/internal/core/fsops"
	core "vole/internal/core/status"
	"vole/internal/task"
)

// frame 是一帧假数据：full = false 时是首帧（还没有电池、进程、硬件这些慢指标）。
func frame(n int, full, battery bool) core.Snapshot {
	s := core.Snapshot{
		At: time.Date(2026, 11, 10, 9, 30, n%60, 0, time.Local), Uptime: (29*24 + 12) * time.Hour, Health: 82, HealthNote: "Good",
		CPU:    core.CPU{Usage: 28 + float64(n%5), PerCore: []float64{40, 22, 35, 18, 60, 12, 8, 30}, Load1: 3.8, Cores: 8},
		Memory: core.Memory{Used: 21.19e9, Total: 32 << 30, UsedPercent: 61 + float64(n%3), SwapUsed: 16.8e9},
		Disks:  []core.Disk{{Mount: "/", Used: 457e9, Total: 494e9, UsedPercent: 92.5}},
		Net:    []core.Iface{{Name: "en0", IP: "10.0.0.2", Rate: core.Rate{In: 1.5e6, Out: 91e3}}},
		NetHistory: core.NetHistory{
			In:  []float64{0.2e6, 0.5e6, 0.3e6, 1.2e6, 0.4e6, 0.6e6, 1.5e6},
			Out: []float64{20e3, 40e3, 30e3, 90e3, 50e3, 60e3, 91e3},
		},
	}
	if !full {
		return s
	}
	s.Model, s.Chip, s.OS = "MacBook Pro", "Apple M1 Pro", "macOS 14.6.1"
	s.Health, s.HealthNote = 69, "Good: Disk Almost Full, High CPU"
	s.Memory.Pressure = "normal"
	s.Thermal.CPUTemp = 59
	if battery {
		s.Batteries = []core.Battery{{Percent: 44, Status: "discharging", TimeLeft: "1:03", Health: "Good", Cycles: 327, Capacity: 81}}
	}
	s.TopProcs = []core.Proc{
		{PID: 152, Name: "WindowServer", CPU: 7.6, MemBytes: 138e6},
		{PID: 93944, Name: "replayd", CPU: 80.5, MemBytes: 11e6},
		{PID: 64297, Name: "ChatGPT", CPU: 19.4, MemBytes: 359e6},
		{PID: 125, Name: "opendirectoryd", CPU: 8.1, MemBytes: 17e6},
	}
	return s
}

// fakeSource 先发一帧首帧，之后每 20ms 发一帧完整帧；gate 关着时不发（模拟首帧之前）。
type fakeSource struct {
	gate    chan struct{}
	battery bool
	live    atomic.Int32
	watches atomic.Int32
}

func (f *fakeSource) Watch(ctx context.Context, _ time.Duration, emit func(core.Snapshot)) error {
	f.watches.Add(1)
	f.live.Add(1)
	defer f.live.Add(-1)
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	emit(frame(0, false, f.battery))
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			emit(frame(n, true, f.battery))
		}
	}
}

type fakeHost struct{ shown, quits []string }

func (h *fakeHost) ShowMain(tab string) { h.shown = append(h.shown, tab) }
func (h *fakeHost) Quit()               { h.quits = append(h.quits, "quit") }

type harness struct {
	t     *testing.T
	p     *Panel
	src   *fakeSource
	host  *fakeHost
	tt    *ui.Tester
	main  chan func()
	freed fsops.Freed
}

func newHarness(t *testing.T, login Login, battery bool, freed fsops.Freed) *harness {
	h := &harness{t: t, src: &fakeSource{gate: make(chan struct{}), battery: battery}, host: &fakeHost{}, main: make(chan func(), 256), freed: freed}
	h.p = New(Options{Source: core.NewShared(h.src), Host: h.host, Login: login,
		Freed: func() fsops.Freed { return h.freed },
		Ports: func() map[string]string { return map[string]string{"en0": "Wi-Fi"} }})
	r := task.NewRunner(func(fn func()) { h.main <- fn })
	t.Cleanup(r.Close)
	h.p.Init(r)
	h.tt = ui.NewTester(func(c *ui.Context) { h.p.View(c) }, Width, h.p.Height())
	return h
}

// drain 跑完排到主线程的回调，再按面板现在的高度出一帧（同外壳：内容变了就改面板大小）。
func (h *harness) drain(d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case fn := <-h.main:
			fn()
		case <-deadline:
			h.tt.SetSize(Width, h.p.Height())
			h.tt.Frame()
			return
		}
	}
}

func (h *harness) want(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if !h.tt.HasText(s) {
			h.t.Fatalf("应有 %q；当前：%q", s, h.tt.Texts())
		}
	}
}

func (h *harness) wantNot(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if h.tt.HasText(s) {
			h.t.Fatalf("不该有 %q；当前：%q", s, h.tt.Texts())
		}
	}
}

func (h *harness) click(label string) {
	h.t.Helper()
	if err := h.tt.Click(label); err != nil {
		h.t.Fatalf("点 %s：%v", label, err)
	}
	h.tt.Frame()
}

// fits：最后一段（label 所在的元素）的底边在面板里，离底边正好留出四周留白——Height 算的和实际排版对得上。
func (h *harness) fits(label string) {
	h.t.Helper()
	r, ok := h.tt.Find(label)
	if !ok {
		h.t.Fatalf("找不到 %q", label)
	}
	bottom := r.Y + r.H
	limit := float32(h.p.Height() - pad)
	if bottom > limit+0.5 {
		h.t.Fatalf("%q 底边 %.1f 超出面板（高 %d，留白 %d）", label, bottom, h.p.Height(), pad)
	}
	if bottom < limit-30 {
		h.t.Fatalf("%q 底边 %.1f 离面板底边太远（高 %d），Height 算多了", label, bottom, h.p.Height())
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

func TestPanelLifecycleAndData(t *testing.T) {
	on := false
	h := newHarness(t, Login{Enabled: func() bool { return on }, Set: func(v bool) error { on = v; return nil }},
		true, fsops.Freed{Bytes: 15_173_816 * 1024, Times: 4})
	// 没打开：不采集
	h.drain(50 * time.Millisecond)
	if h.src.watches.Load() != 0 {
		t.Fatal("面板没打开时不该启动采集")
	}
	// 打开：首帧之前是占位，不空白；电池位置先留着
	h.p.Show()
	h.drain(50 * time.Millisecond)
	h.want("采集中…", "—", "CPU", "内存", "磁盘", "网络", "电池", "高占用进程")
	h.snapshot("01-first-frame-pending")
	close(h.src.gate)
	h.drain(300 * time.Millisecond)
	h.want(
		"69", "磁盘快满了 · 37.0 GB 可用", "M1 Pro", "32 GB", "macOS 14.6.1", "已运行 29d 12h", // 头部
		"59°C", "空闲 · 负载 3.8/8", // CPU
		"压力 正常", "交换空间 16.8 GB", // 内存
		"494 GB", "92", "可用 37.0 GB", // 磁盘
		"Wi-Fi", "1.59", "MB/s", "↑ 91K · ↓ 1.5M", // 网络：上下行合计
		"健康 81%", "44", "可用 1:03", "CPU 最高 replayd 80.5%", // 电池
		"replayd", "80.5%", "ChatGPT", "359 MB", "opendirectoryd", // 前 3 个按 CPU
		"清理观察", "已清出 15.5 GB · 共 4 次")
	h.wantNot("采集中…", "WindowServer")
	h.fits("清理观察")
	h.snapshot("02-panel")

	// 点进程行：打开主窗口的状态 tab
	h.click("查看进程 ChatGPT")
	// 快捷按钮
	for _, label := range []string{"打开 Vole", "扫描清理", "开机时启动：未开启", "退出 Vole"} {
		h.click(label)
	}
	if got := h.host.shown; len(got) != 3 || got[0] != "status" || got[1] != "" || got[2] != "clean" || len(h.host.quits) != 1 {
		t.Fatalf("快捷操作：%v %v", h.host.shown, h.host.quits)
	}
	if !on {
		t.Fatal("开机时启动应打开")
	}
	h.drain(50 * time.Millisecond)
	if _, ok := h.tt.Find("开机时启动：已开启"); !ok {
		t.Fatal("打开后按钮应显示已开启")
	}
	h.snapshot("03-login-on")

	// 关上：采集停掉
	h.p.Hide()
	h.drain(100 * time.Millisecond)
	if h.src.live.Load() != 0 {
		t.Fatal("面板关上后 status 进程应停掉")
	}
}

// 台式机：确认没有电池后电池卡收起、面板变矮；没有清理记录时不显示「清理观察」。
func TestPanelNoBatteryNoRecords(t *testing.T) {
	h := newHarness(t, Login{Enabled: func() bool { return false }, Set: func(bool) error { return nil }}, false, fsops.Freed{})
	h.p.Show()
	close(h.src.gate)
	withSlot := h.p.Height()
	h.drain(300 * time.Millisecond)
	h.wantNot("电池", "清理观察")
	if h.p.Height() != withSlot-gap-batteryH {
		t.Fatalf("没有电池时应收起电池卡：%d → %d", withSlot, h.p.Height())
	}
	h.fits("退出 Vole")
	h.snapshot("04-no-battery")
	h.p.Hide()
}

func TestPanelSharesProcessWithStatusTab(t *testing.T) {
	h := newHarness(t, Login{Disabled: "开发版不是 .app，系统不让注册登录项；打包后可用"}, true, fsops.Freed{})
	close(h.src.gate)
	shared := h.p.o.Source.(*core.Shared)
	// 状态 tab 先订阅
	ctx, stop := context.WithCancel(context.Background())
	go func() { _ = shared.Watch(ctx, time.Second, func(core.Snapshot) {}) }()
	time.Sleep(50 * time.Millisecond)
	h.p.Show()
	h.drain(100 * time.Millisecond)
	if h.src.watches.Load() != 1 || h.src.live.Load() != 1 {
		t.Fatalf("状态 tab 和面板同时开着只能有一个进程：启动 %d 次，在跑 %d", h.src.watches.Load(), h.src.live.Load())
	}
	// 开机启动置灰：原因在鼠标停留提示里，点了没反应
	h.click("开机时启动：开发版不是 .app，系统不让注册登录项；打包后可用")
	if h.p.loginOn || h.p.loginErr != "" {
		t.Fatal("置灰时点开机启动不该有反应")
	}
	h.snapshot("05-login-disabled")
	h.p.Hide()
	h.drain(50 * time.Millisecond)
	if h.src.live.Load() != 1 {
		t.Fatal("面板关了、状态 tab 还开着：进程要留着")
	}
	stop()
	time.Sleep(50 * time.Millisecond)
	if h.src.live.Load() != 0 {
		t.Fatal("都关了进程应停")
	}
}

func TestPanelLoginError(t *testing.T) {
	h := newHarness(t, Login{Enabled: func() bool { return false }, Set: func(bool) error { return errors.New("Operation not permitted") }}, true, fsops.Freed{Bytes: 1e9, Times: 1})
	h.p.Show()
	close(h.src.gate)
	h.drain(200 * time.Millisecond)
	before := h.p.Height()
	h.click("开机时启动：未开启")
	h.drain(50 * time.Millisecond)
	h.want("开机时启动设置失败：Operation not permitted")
	if h.p.Height() != before+errLineH {
		t.Fatalf("出错提示要占一行：%d → %d", before, h.p.Height())
	}
	h.fits("清理观察")
	h.p.Hide()
}

func TestPanelStatusUnavailable(t *testing.T) {
	h := newHarness(t, Login{}, true, fsops.Freed{})
	h.p.o.Source = core.NewShared(errSource{})
	h.p.Show()
	h.drain(100 * time.Millisecond)
	h.want("状态组件不可用")
	h.snapshot("06-unavailable")
	h.p.Hide()
}

type errSource struct{}

func (errSource) Watch(context.Context, time.Duration, func(core.Snapshot)) error {
	return &core.ExitError{ExitCode: -1, Err: errors.New("找不到 status")}
}

func TestTexts(t *testing.T) {
	for _, c := range []struct {
		load  float64
		cores int
		want  string
	}{{9.5, 8, "繁忙"}, {4, 8, "适中"}, {1, 8, "空闲"}, {1, 0, ""}} {
		if got := busyText(core.CPU{Load1: c.load, Cores: c.cores}); got != c.want {
			t.Fatalf("%v/%d：%q", c.load, c.cores, got)
		}
	}
	for in, want := range map[float64]string{1.5e6: "1.5M", 91e3: "91K", 2.5e6: "2.5M", 500: "500", 0: "0"} {
		if got := shortSize(in); got != want {
			t.Fatalf("%v：%q 应为 %q", in, got, want)
		}
	}
	ports := map[string]string{"en0": "Wi-Fi", "en5": "Ethernet Adapter (en5)", "en7": "USB 10/100/1000 LAN", "bridge0": "Thunderbolt Bridge"}
	for dev, want := range map[string]string{"en0": "Wi-Fi", "en5": "以太网", "en7": "以太网", "bridge0": "Thunderbolt Bridge", "utun3": "utun3", "": ""} {
		if got := connType(dev, ports); got != want {
			t.Fatalf("%s：%q", dev, got)
		}
	}
}
