// SPDX-License-Identifier: GPL-3.0-only

package soon

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"vole/internal/kit"
)

// TestComingSoonScene：即将推出页用「戴安全帽」插画；在各自主题色背景上截图（设了 VOLE_SNAPSHOT_DIR 时）。
func TestComingSoonScene(t *testing.T) {
	for _, m := range []struct {
		id, title string
		th        kit.Theme
	}{{"apps", "软件", kit.Violet()}, {"optimize", "优化", kit.Teal()}} {
		p := New(m.id, m.title, m.th)
		th := p.Theme()
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().TextColor(th.Text).FontSize(kit.TextBody).
				LinearGradient(ui.LinearGradient{From: th.BgTop, To: th.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
				Children(func() { p.View(c) })
		}, 1194, 676) // 窗口 762 减去顶栏
		tt.Frame()
		if !tt.HasText(m.title + "功能即将推出") {
			t.Fatalf("%s：%q", m.id, tt.Texts())
		}
		if dir := os.Getenv("VOLE_SNAPSHOT_DIR"); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
			f, err := os.Create(filepath.Join(dir, "coming-soon-"+m.id+".png"))
			if err != nil {
				t.Fatal(err)
			}
			_ = png.Encode(f, tt.Image())
			f.Close()
		}
	}
}
