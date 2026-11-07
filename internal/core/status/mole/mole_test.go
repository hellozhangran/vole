package mole

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"vole/internal/core/status"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.TrimSpace(string(data)))
}

func TestParseFast(t *testing.T) {
	s, err := parse(load(t, "fast.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU.Usage != 28.5 || len(s.CPU.PerCore) != 2 || s.CPU.Cores != 2 {
		t.Fatalf("CPU 不对：%+v", s.CPU)
	}
	if s.Memory.Total != 16e9 || s.Memory.UsedPercent != 50 {
		t.Fatalf("内存不对：%+v", s.Memory)
	}
	// MB/s → 字节/秒
	if s.DiskIO.In != 1.5e6 || s.Net[0].Rate.In != 0.25e6 || s.Net[0].Rate.Out != 0.125e6 || s.NetHistory.In[1] != 0.25e6 {
		t.Fatalf("速率换算不对：io=%+v net=%+v hist=%+v", s.DiskIO, s.Net, s.NetHistory)
	}
	// fast 帧没有慢指标
	if len(s.GPUs) != 0 || len(s.Batteries) != 0 || len(s.TopProcs) != 0 || s.Model != "" {
		t.Fatalf("fast 帧不应有慢指标：%+v", s)
	}
	if s.Health != 82 || s.Uptime != time.Hour {
		t.Fatalf("health/uptime 不对：%d %v", s.Health, s.Uptime)
	}
}

func TestParseFull(t *testing.T) {
	s, err := parse(load(t, "full.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "MacBook Pro" || s.Chip != "Apple M1 Pro" || s.OS != "macOS 14.6.1" {
		t.Fatalf("硬件不对：%q %q %q", s.Model, s.Chip, s.OS)
	}
	if len(s.GPUs) != 1 || s.GPUs[0].Usage != -1 {
		t.Fatalf("GPU 拿不到时应保留 -1：%+v", s.GPUs)
	}
	// 模拟器卷被过滤，系统盘和 /Volumes 保留
	if len(s.Disks) != 2 || s.Disks[0].Mount != "/" || s.Disks[1].Mount != "/Volumes/USB" || !s.Disks[1].External {
		t.Fatalf("磁盘过滤不对：%+v", s.Disks)
	}
	if b := s.Batteries[0]; b.Percent != 57 || b.Cycles != 327 || b.Capacity != 81 || b.TimeLeft != "1:24" {
		t.Fatalf("电池不对：%+v", b)
	}
	if s.Thermal.BatteryTemp != 30.51 || s.Thermal.CPUTemp != 0 {
		t.Fatalf("温度不对：%+v", s.Thermal)
	}
	if p := s.TopProcs[0]; p.PID != 42 || p.CPU != 127.7 || p.MemBytes != 205012992 {
		t.Fatalf("进程不对：%+v", p)
	}
	if len(s.Alerts) != 1 || s.Alerts[0] != 42 {
		t.Fatalf("只保留 active 的告警：%v", s.Alerts)
	}
	if !s.Proxy.Enabled || s.Proxy.Type != "SOCKS" || len(s.Bluetooth) != 1 || s.TrashBytes != 12345 {
		t.Fatalf("代理/蓝牙/废纸篓不对：%+v %+v %d", s.Proxy, s.Bluetooth, s.TrashBytes)
	}
}

func TestParseBad(t *testing.T) {
	for _, in := range []string{"", "not json", `{"cpu":{}}`} {
		if _, err := parse([]byte(in)); err == nil {
			t.Fatalf("%q 应报错", in)
		}
	}
	// health_score 缺失 → -1
	s, err := parse([]byte(`{"collected_at":"2026-11-07T05:13:50Z"}`))
	if err != nil || s.Health != -1 {
		t.Fatalf("缺 health_score 应为 -1：%v %d", err, s.Health)
	}
}

// fakeBinary 写一个假的 status：按 FAKE_MODE 输出帧 / 挂住 / 退出 / 输出垃圾，并记录参数和子进程 pid。
func fakeBinary(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	fast, _ := filepath.Abs("testdata/fast.json")
	full, _ := filepath.Abs("testdata/full.json")
	script := `#!/bin/sh
echo "$@" > "` + dir + `/args"
case "$FAKE_MODE" in
  stream)  cat "` + fast + `"; cat "` + full + `"; sleep 30 & echo $! > "` + dir + `/child"; wait ;;
  crash)   cat "` + fast + `"; cat "` + full + `"; echo "boom" >&2; exit 3 ;;
  exit0)   cat "` + fast + `" ;;
  garbage) while true; do echo "not json"; sleep 0.01; done ;;
  signal)  kill -9 $$ ;;
esac
`
	bin = filepath.Join(dir, "status")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestWatchCancelKillsProcessGroup(t *testing.T) {
	bin, dir := fakeBinary(t)
	t.Setenv("FAKE_MODE", "stream")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames := make(chan status.Snapshot, 8)
	errc := make(chan error, 1)
	go func() {
		errc <- (&Source{Binary: bin}).Watch(ctx, 2*time.Second, func(s status.Snapshot) { frames <- s })
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-frames:
		case <-time.After(3 * time.Second):
			t.Fatalf("没收到第 %d 帧", i+1)
		}
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.TrimSpace(string(args)) != "--watch --interval 2s" {
		t.Fatalf("参数不对：%q", args)
	}
	var pid int
	for i := 0; i < 100 && pid <= 0; i++ {
		data, _ := os.ReadFile(filepath.Join(dir, "child"))
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		time.Sleep(20 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("没拿到子进程 pid")
	}

	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消应返回 context.Canceled：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后没有及时返回")
	}
	for i := 0; i < 40; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("子进程 %d 仍存活", pid)
}

func TestWatchUnexpectedExit(t *testing.T) {
	bin, _ := fakeBinary(t)
	src := &Source{Binary: bin}
	ctx := context.Background()
	n := 0
	emit := func(status.Snapshot) { n++ }

	// 非零退出：带退出码、stderr、已收帧数，不算组件不可用
	t.Setenv("FAKE_MODE", "crash")
	err := src.Watch(ctx, time.Second, emit)
	var ee *status.ExitError
	if !errors.As(err, &ee) || ee.ExitCode != 3 || ee.Frames != 2 || !strings.Contains(ee.Stderr, "boom") || errors.Is(err, status.ErrUnavailable) {
		t.Fatalf("crash：%v", err)
	}
	if n != 2 {
		t.Fatalf("退出前的 2 帧应交付，got %d", n)
	}

	// 退出码 0 也算意外退出（watch 本不该结束），且能再次 Watch（重启）
	t.Setenv("FAKE_MODE", "exit0")
	if err := src.Watch(ctx, time.Second, emit); !errors.As(err, &ee) || ee.ExitCode != 0 || ee.Frames != 1 {
		t.Fatalf("exit0：%v", err)
	}

	// 持续输出垃圾：连续 3 行解析失败就停
	t.Setenv("FAKE_MODE", "garbage")
	done := make(chan error, 1)
	go func() { done <- src.Watch(ctx, time.Second, emit) }()
	select {
	case err := <-done:
		if !errors.As(err, &ee) || !strings.Contains(err.Error(), "解析") {
			t.Fatalf("garbage：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("垃圾输出应被及时停掉")
	}

	// 被信号杀掉 / 二进制缺失 / 损坏：组件不可用
	t.Setenv("FAKE_MODE", "signal")
	if err := src.Watch(ctx, time.Second, emit); !errors.Is(err, status.ErrUnavailable) {
		t.Fatalf("signal：%v", err)
	}
	if err := (&Source{Binary: "/nope/status"}).Watch(ctx, time.Second, emit); !errors.Is(err, status.ErrUnavailable) {
		t.Fatalf("missing：%v", err)
	}
	broken := filepath.Join(t.TempDir(), "status")
	_ = os.WriteFile(broken, []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 1, 2}, 0o755)
	if err := (&Source{Binary: broken}).Watch(ctx, time.Second, emit); !errors.Is(err, status.ErrUnavailable) {
		t.Fatalf("broken：%v", err)
	}
}
