package shell_test

import (
	"context"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/analyze"
	"vole/internal/core/procs"
	corestatus "vole/internal/core/status"
	"vole/internal/core/sysenv"
	"vole/internal/features/analyze"
	"vole/internal/features/status"
	"vole/internal/module"
	"vole/internal/shell"
)

// 和作者截图同一块屏幕、同样窗口大小：1194×762pt，2 倍渲染。
const refW, refH = 1194, 762

// rootSizes 是作者截图时根视图的四个目录大小，截图数字对得上方便比较。
type rootSizes struct{}

func (rootSizes) Scan(_ context.Context, req core.Request) (core.Report, error) {
	sizes := map[string]int64{"/Applications": 22.8e9, "/Library": 32e9, "/opt/homebrew": 3.19e9}
	size, ok := sizes[req.Path]
	if !ok {
		size = 378e9 // ~
	}
	st := core.StatusComplete
	if req.Path == "/Library" {
		st = core.StatusPartial
	}
	return core.Report{Path: req.Path, Status: st, TotalSize: size,
		Entries: []core.Entry{{Name: "sub", Path: req.Path + "/sub", Size: size, IsDir: true, Status: core.StatusComplete}}}, nil
}

// procList 推一批固定的进程后挂住，直到取消（截图用）。
type procList struct{}

func (procList) Watch(ctx context.Context, _ time.Duration, _ procs.Options, emit func([]procs.Proc)) error {
	at := time.Date(2026, 11, 7, 5, 13, 50, 0, time.Local)
	emit([]procs.Proc{
		{PID: 93944, Name: "replayd", Path: "/usr/libexec/replayd", CPU: 80.5, MemBytes: 11e6, Started: at},
		{PID: 64297, Name: "ChatGPT", Path: "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT", CPU: 19.4, MemBytes: 359e6, Started: at},
		{PID: 125, Name: "opendirectoryd", Path: "/usr/libexec/opendirectoryd", CPU: 8.1, MemBytes: 17e6, Started: at},
		{PID: 152, Name: "WindowServer", CPU: 7.6, MemBytes: 138e6, Started: at},
		{PID: 85, Name: "logd", Path: "/usr/libexec/logd", CPU: 4.7, MemBytes: 38e6, Started: at},
	})
	<-ctx.Done()
	return ctx.Err()
}

// frames 推一帧完整状态后挂住，直到取消。
type frames struct{}

func (frames) Watch(ctx context.Context, _ time.Duration, emit func(corestatus.Snapshot)) error {
	emit(corestatus.Snapshot{
		At: time.Now(), Uptime: (29*24 + 12) * time.Hour, Model: "MacBook Pro", Chip: "Apple M1 Pro", OS: "macOS 14.6.1",
		Health: 69, HealthNote: "Good: High CPU",
		CPU:    corestatus.CPU{Usage: 79, PerCore: []float64{70, 62, 80, 82, 78, 92, 90, 85}, Load1: 3.8, Cores: 8, PCores: 6, ECores: 2},
		GPUs:   []corestatus.GPU{{Name: "Apple M1 Pro", Usage: -1, Cores: 14}},
		Memory: corestatus.Memory{Used: 21.19e9, Total: 34.36e9, UsedPercent: 66, SwapUsed: 16.8e9},
		Disks:  []corestatus.Disk{{Mount: "/", Used: 457e9, Total: 494e9, UsedPercent: 92}},
		Net:    []corestatus.Iface{{Name: "en0", IP: "10.0.0.2", Rate: corestatus.Rate{In: 1.5e6, Out: 91e3}}},
		NetHistory: corestatus.NetHistory{In: []float64{0.1e6, 0.3e6, 0.2e6, 1.4e6, 0.3e6, 0.5e6, 0.2e6, 1.5e6},
			Out: []float64{20e3, 40e3, 30e3, 90e3, 50e3, 60e3, 40e3, 91e3}},
		Batteries: []corestatus.Battery{{Percent: 44, Status: "discharging", TimeLeft: "1:03", Health: "Good", Cycles: 327, Capacity: 81}},
		Thermal:   corestatus.Thermal{BatteryTemp: 31},
		TopProcs: []corestatus.Proc{{PID: 93944, Name: "replayd", CPU: 80.5, MemBytes: 11e6}, {PID: 64297, Name: "ChatGPT", CPU: 19.4, MemBytes: 359e6},
			{PID: 125, Name: "opendirectoryd", CPU: 8.1, MemBytes: 17e6}, {PID: 152, Name: "WindowServer", CPU: 7.6, MemBytes: 138e6}, {PID: 85, Name: "logd", CPU: 4.7, MemBytes: 38e6}},
		Alerts: []int{93944},
	})
	<-ctx.Done()
	return ctx.Err()
}

func newRefShell(t *testing.T, defaultID string) (*ui.Tester, func()) {
	t.Helper()
	main := make(chan func(), 64)
	mods := []module.Module{
		analyze.New(rootSizes{}, sysenv.Info{Home: os.Getenv("HOME")}),
		status.New(frames{}, procList{}),
	}
	s := shell.NewShell(shell.Options{Name: "Vole", Version: "0.1.0", Modules: mods, DefaultID: defaultID}, func(fn func()) { main <- fn })
	tt := ui.NewTester(s.View, refW, refH)
	tt.SetScale(2)
	tt.SetDark(false) // 作者系统是浅色外观：提示框颜色要在这种情况下也看得清
	drain := func() {
		for {
			select {
			case fn := <-main:
				fn()
			case <-time.After(400 * time.Millisecond):
				tt.Frame()
				return
			}
		}
	}
	drain()
	return tt, drain
}

// TestTypescaleSnapshots 出和 Mole 截图同尺寸的对照图，并检查提示框能显示。
func TestTypescaleSnapshots(t *testing.T) {
	tt, _ := newRefShell(t, "analyze")
	snap(t, tt, "typescale-analyze")
	sideBySide(t, tt, "mole-ui/02-analyze-root-partial.png", "typescale-analyze-vs-mole")

	r, ok := tt.Find("块 Users/ran")
	if !ok {
		r, ok = tt.Find("块 " + filepath.Base(os.Getenv("HOME")))
	}
	if ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Advance(2 * time.Second)
		snap(t, tt, "typescale-tooltip")
	}

	st, _ := newRefShell(t, "status")
	snap(t, st, "typescale-status")
	sideBySide(t, st, "mole-ui/04-status.png", "typescale-status-vs-mole")
}

// sideBySide 把 Mole 参考图（.local/dev 下）和 Vole 当前画面左右拼在一起。
func sideBySide(t *testing.T, tt *ui.Tester, ref, name string) {
	dir := os.Getenv("VOLE_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	f, err := os.Open(filepath.Join("..", "..", ".local", "dev", ref))
	if err != nil {
		return // 不在作者本机：没有参考图
	}
	defer f.Close()
	mole, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	vole := tt.Image()
	mb, vb := mole.Bounds(), vole.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, mb.Dx()+vb.Dx()+20, max(mb.Dy(), vb.Dy())))
	draw.Draw(out, mb.Add(image.Pt(-mb.Min.X, -mb.Min.Y)), mole, mb.Min, draw.Src)
	draw.Draw(out, vb.Add(image.Pt(mb.Dx()+20-vb.Min.X, -vb.Min.Y)), vole, vb.Min, draw.Src)
	o, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	_ = png.Encode(o, out)
}
