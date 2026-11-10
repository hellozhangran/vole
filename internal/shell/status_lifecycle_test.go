package shell_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vole/internal/core/procs"
	statusmole "vole/internal/core/status/mole"
	"vole/internal/core/sysenv"
	"vole/internal/features/analyze"
	"vole/internal/features/status"
	"vole/internal/module"
	"vole/internal/shell"
)

// countPS 在 3 秒内看到的 ps 采样进程个数（只数 Vole 的扫描参数）；采样是短命进程，用多次 pgrep 估计。
func countPS() int {
	n := 0
	for i := 0; i < 60; i++ {
		out, _ := exec.Command("pgrep", "-f", "ps -axo pid=,ppid=,uid=,rss=,time=,lstart=").Output()
		n += len(strings.Fields(string(out)))
		time.Sleep(50 * time.Millisecond)
	}
	return n
}

// statusPIDs 返回用 bin 启动的 status 进程。
func statusPIDs(bin string) []string {
	out, _ := exec.Command("pgrep", "-f", bin+" --watch").Output()
	return strings.Fields(string(out))
}

func waitPIDs(t *testing.T, bin string, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for (len(statusPIDs(bin)) > 0) != want {
		if time.Now().After(deadline) {
			t.Fatalf("status 进程存在=%v，期望 %v", len(statusPIDs(bin)) > 0, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStatusProcessLifecycle 用真实 status 二进制：只有状态 tab 可见时才有进程；切走消失、切回恢复、退出清干净。
func TestStatusProcessLifecycle(t *testing.T) {
	bin := os.Getenv("VOLE_STATUS_BIN")
	if bin == "" {
		bin, _ = filepath.Abs("../../build/dev-bin/status")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("没有 status 二进制（scripts/build-mole.sh status）")
	}
	main := make(chan func(), 256)
	mods := []module.Module{
		analyze.New(rootSizes{}, sysenv.Info{Home: os.Getenv("HOME")}),
		status.New(&statusmole.Source{Binary: bin}, procs.PS{}),
	}
	s := shell.NewShell(shell.Options{Name: "Vole", Version: "0.1.0", Modules: mods, DefaultID: "analyze"}, func(fn func()) { main <- fn })
	tt := ui.NewTester(s.View, refW, refH)
	pump := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			select {
			case fn := <-main:
				fn()
			case <-time.After(50 * time.Millisecond):
			}
		}
		tt.Frame()
	}
	click := func(label string) {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	pump(300 * time.Millisecond)
	waitPIDs(t, bin, false) // 分析 tab：不该有 status 进程

	click("状态")
	waitPIDs(t, bin, true)
	pump(2500 * time.Millisecond) // 首帧 + 完整帧
	if !tt.HasText("健康度") || !tt.HasText("名称（50）") {
		t.Fatalf("应显示真实指标：%q", tt.Texts())
	}
	snap(t, tt, "procs-real")

	click("分析")
	waitPIDs(t, bin, false) // 切走：进程消失
	if n := countPS(); n != 0 {
		t.Fatalf("切走后不应再起 ps 采样：%d", n)
	}

	click("状态")
	waitPIDs(t, bin, true) // 切回：恢复
	pump(1500 * time.Millisecond)

	shell.CloseShell(s) // 和 Cmd+Q 同一条路径
	waitPIDs(t, bin, false)
}
