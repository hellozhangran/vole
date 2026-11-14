// SPDX-License-Identifier: GPL-3.0-only

// Package settings 是设置面板：在主窗口里弹出（应用菜单「设置… ⌘,」或菜单栏面板进入），布局参照 Mole app 的设置面板，
// 只有「通用」一页，只放 Vole 现在真正支持的：完全磁盘访问、界面语言、开机自动启动。
// 语言写进 core/settings，下次启动由 i18n 读取，所以要重启才生效。状态只在主线程读写。
package settings

import (
	"os"

	"github.com/egoist/mygo/ui"

	store "vole/internal/core/settings"
	"vole/internal/core/sysenv"
	"vole/internal/i18n"
	"vole/internal/kit"
)

// Login 是「开机自动启动」的后端（和菜单栏面板用同一套：mygo.App.OpenAtLogin / SetOpenAtLogin）。
type Login struct {
	Enabled  func() bool
	Set      func(bool) error
	Disabled string // 非空：开关置灰，写原因（如开发版不是 .app，系统不让注册）
}

// Options 是面板的依赖。
type Options struct {
	Path     string               // 设置文件（core/settings.DefaultPath()）
	FDA      func() sysenv.Access // 完全磁盘访问的当前状态
	OpenFDA  func()               // 打开「系统设置 › 隐私与安全性 › 完全磁盘访问」
	Login    Login
	Relaunch func() // 重启 Vole（mygo.App.Relaunch）
	// Resolve 是选了某个语言后下次启动会用的语言（默认 i18n.Resolve，测试里换成固定的）。
	Resolve func(choice string) i18n.Lang
}

// Panel 是设置面板。
type Panel struct {
	o     Options
	theme kit.Theme

	lang     string // 设置里选的语言；空 = 跟随系统
	fda      sysenv.Access
	loginOn  bool
	loginErr string
	saveErr  string
}

// New 创建设置面板。
func New(o Options) *Panel {
	if o.Resolve == nil {
		o.Resolve = i18n.Resolve
	}
	return &Panel{o: o, theme: kit.Graphite()}
}

// Open 每次打开前重新读一遍：设置文件、完全磁盘访问、开机启动（可能在系统设置里改过）。
func (p *Panel) Open() {
	s, err := store.Load(p.o.Path)
	p.lang, p.saveErr, p.loginErr = s.Lang, "", ""
	if err != nil {
		p.saveErr = i18n.T("设置文件读不了，已按默认设置显示：") + err.Error()
	}
	if p.o.FDA != nil {
		p.fda = p.o.FDA()
	}
	if p.o.Login.Enabled != nil && p.o.Login.Disabled == "" {
		p.loginOn = p.o.Login.Enabled()
	}
}

// 语言选项：语言名用各自的写法，不翻译（找自己能看懂的那一个）；只有「跟随系统」随界面语言。
var langs = []struct{ code, name string }{{"", ""}, {"zh", "中文"}, {"ko", "한국어"}, {"en", "English"}}

var (
	closeIcon  = kit.LineIcon(`<path d="M6 6l12 12M18 6 6 18"/>`)
	green      = ui.Hex("#3fae8a")
	red        = ui.Hex("#f08a7a")
	panelBG    = ui.LinearGradient{From: ui.Hex("#3b3833"), To: ui.Hex("#2a2927"), Angle: 180, Oklab: true}
	lineColor  = ui.RGBA(255, 255, 255, 0.08)
	panelWidth = float32(600)
)

// View 画设置面板（打开时）：半透明遮罩 + 居中面板。点遮罩、按 Esc、点右上角关闭。
func (p *Panel) View(c *ui.Context, open *bool) {
	t := p.theme
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.42))
		panel.Width(panelWidth).Padding(28, 32, 20, 32).Radius(20).LinearGradient(panelBG).
			Border(1, ui.RGBA(255, 255, 255, 0.10)).TextColor(t.Text).FontSize(kit.TextBody)
		ui.Column(c).FillWidth().Children(func() {
			ui.Row(c).FillWidth().Margin(0, 0, 12, 0).Children(func() {
				ui.Text(c, i18n.T("设置")).FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
				ui.Spacer(c)
				kit.IconButton(c.Key("close"), t, i18n.T("关闭设置"), closeIcon, 32).Background(t.Surface).OnClick(func() { *open = false })
			})
			p.fdaRow(c, t)
			p.langRow(c, t)
			p.loginRow(c, t)
		})
	})
}

// row 是一行设置：左边标题和说明（extra 画在说明下面），右边控件；除最后一行都有分隔线。
func row(c *ui.Context, t kit.Theme, key, title, desc string, last bool, extra, right func()) {
	r := ui.Row(c.Key(key)).FillWidth().Padding(16, 0).Gap(16)
	if !last {
		r.Border(1, lineColor).BorderWidth(0, 0, 1, 0)
	}
	r.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(kit.TextHeadline).FontWeight(kit.WeightSemibold).SingleLine()
			if desc != "" {
				ui.Text(c, desc).FontSize(kit.TextBody).TextColor(t.Muted).MaxLines(3).FillWidth()
			}
			if extra != nil {
				extra()
			}
		})
		ui.Box(c).Shrink(0).Children(right)
	})
}

// badge 是绿底状态标签（如「已授权」）。
func badge(c *ui.Context, text string, color ui.Color) {
	ui.Text(c, text).FontSize(kit.TextBody).FontWeight(kit.WeightSemibold).TextColor(color).
		Padding(6, 14).Radius(kit.RadiusM).Background(color.Alpha(0.16)).SingleLine()
}

// button 是浅色描边的小按钮（「去设置」「立即重启」）。
func button(c *ui.Context, t kit.Theme, key, text string, onClick func()) {
	b := ui.ButtonBase(c.Key(key)).Label(text).Height(kit.ControlL+4).Padding(0, 14).Radius(kit.RadiusM).Background(green.Alpha(0.16))
	if b.Hovered() {
		b.Background(green.Alpha(0.26))
	}
	b.Children(func() { ui.Text(c, text).FontWeight(kit.WeightSemibold).TextColor(green).SingleLine() })
	b.OnClick(onClick)
}

func (p *Panel) fdaRow(c *ui.Context, t kit.Theme) {
	row(c, t, "fda", i18n.T("完全磁盘访问"), i18n.T("扫描、清理时才能读到受系统保护的目录（如 Mail、Safari、信息）"), false, nil, func() {
		if p.fda == sysenv.AccessGranted {
			badge(c, i18n.T("已授权"), green)
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, p.fda.String()).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
			button(c, t, "open-fda", i18n.T("去设置"), func() {
				if p.o.OpenFDA != nil {
					p.o.OpenFDA()
				}
			})
		})
	})
}

func (p *Panel) langRow(c *ui.Context, t kit.Theme) {
	pending := p.o.Resolve(p.lang) != i18n.Current() // 选的语言和正在用的不一样：要重启才生效
	desc := i18n.T("重启 Vole 后生效")
	if os.Getenv("VOLE_LANG") != "" {
		desc = i18n.T("现在由环境变量 VOLE_LANG 决定，它优先于这里的选择")
	}
	row(c, t, "lang", i18n.T("语言"), desc, false, func() {
		if p.saveErr != "" {
			ui.Text(c, p.saveErr).FontSize(kit.TextSecondary).TextColor(red).MaxLines(2).FillWidth()
		}
		if pending && p.o.Relaunch != nil {
			ui.Row(c).Margin(6, 0, 0, 0).Children(func() { button(c, t, "relaunch", i18n.T("立即重启"), p.o.Relaunch) })
		}
	}, func() {
		ui.Row(c.Key("langs")).Padding(3).Gap(2).Radius(kit.RadiusL).Background(t.Surface).Children(func() {
			for _, l := range langs {
				name := l.name
				if l.code == "" {
					name = i18n.T("跟随系统")
				}
				b := ui.ButtonBase(c.Key("lang-"+l.code)).Label(name).Height(kit.ControlL+2).Padding(0, 12).Radius(kit.RadiusM + 2).TextColor(t.Muted)
				switch {
				case l.code == p.lang:
					b.Background(green.Alpha(0.22)).TextColor(green)
				case b.Hovered():
					b.TextColor(t.Text)
				}
				b.Children(func() { ui.Text(c, name).FontWeight(kit.WeightSemibold).SingleLine() })
				code := l.code
				b.OnClick(func() { p.setLang(code) })
			}
		})
	})
}

// setLang 选语言：马上写进设置文件（失败时保留原来的选择并显示原因）。
func (p *Panel) setLang(code string) {
	if code == p.lang {
		return
	}
	if err := store.Save(p.o.Path, store.Settings{Lang: code}); err != nil {
		p.saveErr = i18n.T("保存设置失败：") + err.Error()
		return
	}
	p.lang, p.saveErr = code, ""
}

func (p *Panel) loginRow(c *ui.Context, t kit.Theme) {
	disabled := p.o.Login.Disabled != "" || p.o.Login.Set == nil
	desc := i18n.T("登录 Mac 时自动打开 Vole，只在菜单栏显示图标")
	if p.o.Login.Disabled != "" {
		desc = p.o.Login.Disabled
	}
	row(c, t, "login", i18n.T("开机自动启动"), desc, true, func() {
		if p.loginErr != "" {
			ui.Text(c, p.loginErr).FontSize(kit.TextSecondary).TextColor(red).MaxLines(2).FillWidth()
		}
	}, func() {
		on := p.loginOn && !disabled
		b := ui.ButtonBase(c.Key("login-switch")).Label(i18n.T("开机自动启动开关")).Disabled(disabled)
		b.Children(func() { switchView(c, on, disabled) })
		b.OnClick(func() {
			if disabled {
				return
			}
			want := !p.loginOn
			if err := p.o.Login.Set(want); err != nil {
				p.loginErr = i18n.T("设置失败：") + err.Error()
				return
			}
			p.loginErr, p.loginOn = "", want
			if p.o.Login.Enabled != nil {
				p.loginOn = p.o.Login.Enabled()
			}
		})
	})
}

// switchView 画一个开关（只画，点击由外面的按钮处理）。
func switchView(c *ui.Context, on, disabled bool) {
	w, h := float32(46), float32(26)
	bg := ui.RGBA(255, 255, 255, 0.14)
	if on {
		bg = green
	}
	el := ui.Box(c).Size(w, h).Shrink(0).Radius(h / 2).Background(bg).Children(func() {
		knob := ui.Box(c).Size(h-6, h-6).Radius((h - 6) / 2).Background(ui.Hex("#f5f1ec")).Absolute().Top(3)
		if on {
			knob.Right(3)
		} else {
			knob.Left(3)
		}
	})
	if disabled {
		el.Opacity(0.4)
	}
}
