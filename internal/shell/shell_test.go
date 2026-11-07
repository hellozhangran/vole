package shell_test

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/analyze"
	"vole/internal/core/sysenv"
	"vole/internal/features/analyze"
	"vole/internal/features/soon"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/shell"
)

// instant 立即返回一份假结果：每个路径下 6 个子目录。
type instant struct{}

func (instant) Scan(_ context.Context, req core.Request) (core.Report, error) {
	r := core.Report{Path: req.Path, Status: core.StatusComplete}
	for i, n := range []string{"Code", "Library", ".cache", "Downloads", "Movies", "go"} {
		size := int64(90-i*14) * 1e9
		r.Entries = append(r.Entries, core.Entry{Name: n, Path: req.Path + "/" + n, Size: size, IsDir: true, Status: core.StatusComplete})
		r.TotalSize += size
	}
	return r, nil
}

func TestTabsKeepModuleState(t *testing.T) {
	main := make(chan func(), 64)
	mods := []module.Module{
		soon.New("clean", "清理", kit.Ocean()),
		soon.New("apps", "软件", kit.Violet()),
		soon.New("optimize", "优化", kit.Teal()),
		analyze.New(instant{}, sysenv.Info{Home: os.Getenv("HOME")}),
		soon.New("status", "状态", kit.Olive()),
	}
	s := shell.NewShell(shell.Options{Name: "Vole", Version: "0.1.0", Modules: mods, DefaultID: "analyze"}, func(fn func()) { main <- fn })
	tt := ui.NewTester(s.View, 1200, 780)
	drain := func() {
		for {
			select {
			case fn := <-main:
				fn()
			case <-time.After(500 * time.Millisecond):
				tt.Frame()
				return
			}
		}
	}
	drain()
	if !tt.HasText("整个磁盘") || !tt.HasText("Vole 0.1.0") {
		t.Fatalf("启动应停在分析 tab：%q", tt.Texts())
	}
	snap(t, tt, "10-shell-analyze-root")

	// 下钻一层，切到清理再切回来，应仍在下钻后的目录
	if err := tt.Click("块 Applications"); err != nil {
		t.Fatal(err)
	}
	drain()
	snap(t, tt, "11-shell-analyze-dir")
	if err := tt.Click("清理"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("清理功能即将推出") {
		t.Fatalf("应显示清理占位：%q", tt.Texts())
	}
	snap(t, tt, "12-shell-clean")
	if err := tt.Click("分析"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("返回 Applications") {
		t.Fatalf("切回分析应保留下钻位置：%q", tt.Texts())
	}
}

func snap(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("VOLE_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = png.Encode(f, tt.Image())
}
