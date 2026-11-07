package kit

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestRunningVoleRepaintsOnlyWhileRunning 确认：奔跑时按帧率重绘、循环换帧；停下后不再请求任何帧。
func TestRunningVoleRepaintsOnlyWhileRunning(t *testing.T) {
	running := true
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Center().Background(ui.Hex("#3a2418")).Children(func() {
			RunningVole(c, 120, RunningFPS, running)
		})
	}, 200, 200)

	// 1 秒 12.5 帧：约 12–13 次绘制
	before := runningPaints.Load()
	tt.Advance(time.Second)
	if n := runningPaints.Load() - before; n < 11 || n > 15 {
		t.Fatalf("1 秒应绘制约 12 次，实际 %d", n)
	}
	// 截 8 帧看循环（设置 VOLE_SNAPSHOT_DIR 时）
	for i := 0; i < 8; i++ {
		snap(t, tt, i)
		tt.Advance(80 * time.Millisecond)
	}

	running = false
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("停下后不应再请求帧")
	}
	// 注：FramePending 只看立即要画的帧，After 定的帧靠下面的绘制计数确认
	before = runningPaints.Load()
	tt.Advance(5 * time.Second)
	if n := runningPaints.Load() - before; n != 0 {
		t.Fatalf("停下后 5 秒内不应重绘，实际 %d 次", n)
	}
}

func snap(t *testing.T, tt *ui.Tester, i int) {
	dir := os.Getenv("VOLE_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, "running-frame-"+string(rune('1'+i))+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = png.Encode(f, tt.Image())
}
