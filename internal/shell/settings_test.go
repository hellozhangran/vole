package shell_test

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"vole/internal/features/soon"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/shell"
)

// fakeSettings 记录打开次数；打开时画一行字。
type fakeSettings struct{ opens int }

func (f *fakeSettings) Open() { f.opens++ }
func (f *fakeSettings) View(c *ui.Context, open *bool) {
	if *open {
		ui.Text(c, "假设置面板")
	}
}

// 菜单「设置… ⌘,」和菜单栏面板的「设置」都走 OpenSettings：每次打开前重新读状态，打开后盖在主窗口上。
func TestOpenSettings(t *testing.T) {
	fs := &fakeSettings{}
	mods := []module.Module{soon.New("clean", "清理", kit.Ocean())}
	s := shell.NewShell(shell.Options{Name: "Vole", Version: "0.1.0", Modules: mods, DefaultID: "clean",
		Settings: func(shell.Controller) shell.Settings { return fs }}, func(fn func()) { fn() })
	tt := ui.NewTester(s.View, 1200, 780)
	tt.Frame()
	if tt.HasText("假设置面板") {
		t.Fatal("没打开时不该画设置面板")
	}
	s.OpenSettings()
	tt.Frame()
	if fs.opens != 1 || !tt.HasText("假设置面板") {
		t.Fatalf("应打开设置面板：%d %q", fs.opens, tt.Texts())
	}
}
