// SPDX-License-Identifier: GPL-3.0-only

package settings

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	store "vole/internal/core/settings"
	"vole/internal/core/sysenv"
	"vole/internal/i18n"
	"vole/internal/kit"
)

type harness struct {
	t        *testing.T
	p        *Panel
	tt       *ui.Tester
	open     bool
	path     string
	fda      sysenv.Access
	fdaOpens int
	relaunch int
	loginOn  bool
	loginErr error
	system   i18n.Lang // 「跟随系统」时系统语言会解析成什么
}

func newHarness(t *testing.T, login Login) *harness {
	h := &harness{t: t, open: true, path: filepath.Join(t.TempDir(), "Vole", "settings.json"), fda: sysenv.AccessDenied, system: i18n.Current()}
	if login.Disabled == "" {
		login = Login{Enabled: func() bool { return h.loginOn }, Set: func(v bool) error {
			if h.loginErr != nil {
				return h.loginErr
			}
			h.loginOn = v
			return nil
		}}
	}
	h.p = New(Options{Path: h.path, FDA: func() sysenv.Access { return h.fda }, OpenFDA: func() { h.fdaOpens++ },
		Login: login, Relaunch: func() { h.relaunch++ },
		Resolve: func(choice string) i18n.Lang {
			if choice == "" {
				return h.system
			}
			return i18n.Lang(choice)
		}})
	t1 := kit.Earth()
	// 套一层主窗口的背景，截图才像在窗口里弹出
	h.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().TextColor(t1.Text).FontSize(kit.TextBody).
			LinearGradient(ui.LinearGradient{From: t1.BgTop, To: t1.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
			Children(func() { h.p.View(c, &h.open) })
	}, 1200, 780)
	h.p.Open()
	h.tt.Frame()
	return h
}

func (h *harness) want(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if !h.tt.HasText(s) {
			h.t.Fatalf("应有 %q；当前：%q", s, h.tt.Texts())
		}
	}
}

func (h *harness) click(label string) {
	h.t.Helper()
	if err := h.tt.Click(label); err != nil {
		h.t.Fatalf("点 %q：%v；当前：%q", label, err, h.tt.Texts())
	}
	h.tt.Frame()
}

func (h *harness) saved() string {
	h.t.Helper()
	s, err := store.Load(h.path)
	if err != nil {
		h.t.Fatal(err)
	}
	return s.Lang
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

// 以下断言用中文原文，只在默认（中文）下跑；截图用 TestShots，换 VOLE_LANG 各跑一次。
func skipUnlessChinese(t *testing.T) {
	if i18n.Current() != i18n.ZH {
		t.Skip("断言用中文原文")
	}
}

func TestLanguageSavedAndRelaunch(t *testing.T) {
	skipUnlessChinese(t)
	h := newHarness(t, Login{})
	h.want("设置", "完全磁盘访问", "未授权", "去设置", "语言", "重启 Vole 后生效", "跟随系统", "中文", "한국어", "English", "开机自动启动")
	if h.tt.HasText("立即重启") {
		t.Fatal("没改语言时不该提示重启")
	}
	// 选 English：马上写进设置文件，并提示立即重启
	h.click("English")
	if h.saved() != "en" {
		t.Fatalf("应保存 en：%q", h.saved())
	}
	h.want("立即重启")
	h.click("立即重启")
	if h.relaunch != 1 {
		t.Fatal("应重启 Vole")
	}
	// 选回中文（和正在用的一样）：不再提示重启
	h.click("中文")
	if h.saved() != "zh" || h.tt.HasText("立即重启") {
		t.Fatalf("%q %v", h.saved(), h.tt.HasText("立即重启"))
	}
	// 跟随系统：设置里不写语言；系统是韩语时提示重启
	h.system = i18n.KO
	h.click("跟随系统")
	if h.saved() != "" {
		t.Fatalf("跟随系统应清空：%q", h.saved())
	}
	h.want("立即重启")
	// 重新打开：从设置文件读回选择
	h.click("English")
	h.p.lang = ""
	h.p.Open()
	if h.p.lang != "en" {
		t.Fatalf("应读回 en：%q", h.p.lang)
	}
}

func TestSaveError(t *testing.T) {
	skipUnlessChinese(t)
	h := newHarness(t, Login{})
	// 设置目录的位置被一个普通文件占了：写不进去
	_ = os.MkdirAll(filepath.Dir(filepath.Dir(h.path)), 0o755)
	_ = os.WriteFile(filepath.Dir(h.path), []byte("x"), 0o600)
	h.click("한국어")
	h.want("保存设置失败：")
	if h.p.lang != "" {
		t.Fatal("保存失败时保留原来的选择")
	}
}

func TestFullDiskAccess(t *testing.T) {
	skipUnlessChinese(t)
	h := newHarness(t, Login{})
	h.click("去设置")
	if h.fdaOpens != 1 {
		t.Fatal("应打开系统设置")
	}
	// 授权后重新打开：显示「已授权」，没有「去设置」
	h.fda = sysenv.AccessGranted
	h.p.Open()
	h.tt.Frame()
	h.want("已授权")
	if h.tt.HasText("去设置") {
		t.Fatal("已授权时不该有「去设置」")
	}
}

func TestLoginSwitch(t *testing.T) {
	skipUnlessChinese(t)
	h := newHarness(t, Login{})
	h.click("开机自动启动开关")
	if !h.loginOn || !h.p.loginOn {
		t.Fatal("应打开开机自动启动")
	}
	h.loginErr = errors.New("Operation not permitted")
	h.click("开机自动启动开关")
	h.want("设置失败：Operation not permitted")
	if !h.p.loginOn {
		t.Fatal("失败时开关保持原样")
	}
	// 开发版不能注册：置灰、写原因，点了没反应
	d := newHarness(t, Login{Disabled: "开发版不是 .app，系统不让注册登录项；打包后可用"})
	d.want("开发版不是 .app，系统不让注册登录项；打包后可用")
	d.click("开机自动启动开关")
	if d.p.loginOn || d.p.loginErr != "" {
		t.Fatal("置灰时点了不该有反应")
	}
}

func TestClose(t *testing.T) {
	skipUnlessChinese(t)
	h := newHarness(t, Login{})
	h.click("关闭设置")
	if h.open {
		t.Fatal("点关闭应关掉面板")
	}
	if h.tt.HasText("完全磁盘访问") {
		t.Fatal("关掉后不该再画面板")
	}
}

// TestShots 出设置面板的截图（设了 VOLE_SNAPSHOT_DIR 时）；用 VOLE_LANG=zh / ko / en 各跑一次。
func TestShots(t *testing.T) {
	if os.Getenv("VOLE_SNAPSHOT_DIR") == "" {
		t.Skip("设置 VOLE_SNAPSHOT_DIR 才截图")
	}
	t.Setenv("VOLE_LANG", "") // 界面语言已经按 VOLE_LANG 定好；清掉它，截图里才是普通用户看到的说明
	h := newHarness(t, Login{})
	h.loginOn = true
	h.p.Open()
	h.p.lang = "en" // 选了别的语言：显示「立即重启」
	if i18n.Current() == i18n.EN {
		h.p.lang = "ko"
	}
	h.tt.Frame()
	h.snapshot("settings-" + string(i18n.Current()))
}
