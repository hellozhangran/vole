package analyze

import (
	"os"
	"testing"

	"vole/internal/i18n"
)

// TestLangShots 出分析首屏（根目录扫完）的截图，文件名带语言；用 VOLE_LANG=en 等跑（不做文字断言）。
func TestLangShots(t *testing.T) {
	if os.Getenv("VOLE_SNAPSHOT_DIR") == "" {
		t.Skip("设置 VOLE_SNAPSHOT_DIR 才截图")
	}
	h := newHarness(t)
	h.m.OnActivate()
	h.settle(4) // 根目录下 4 项扫完
	h.snapshot(string(i18n.Current()) + "-analyze-root")
}
