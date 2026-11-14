package clean

import (
	"os"
	"testing"
	"time"

	"vole/internal/i18n"
)

// TestLangShots 出清理首屏和结果页的截图，文件名带语言；用 VOLE_LANG=en 等跑（不做文字断言）。
func TestLangShots(t *testing.T) {
	if os.Getenv("VOLE_SNAPSHOT_DIR") == "" {
		t.Skip("设置 VOLE_SNAPSHOT_DIR 才截图")
	}
	res := fakeResult()
	for i := range res.Categories { // 假数据的类别名写的是中文；真实扫描时由 core/clean 的规则翻译
		res.Categories[i].Title = i18n.T(res.Categories[i].Title)
	}
	be := &fakeBackend{res: res}
	h := newHarness(t, be)
	h.tt.Frame()
	lang := string(i18n.Current())
	h.snapshot(lang + "-clean-home")
	_ = h.tt.Click(i18n.T("扫描"))
	h.tt.Frame()
	h.drain(200 * time.Millisecond)
	h.snapshot(lang + "-clean-results")
}
