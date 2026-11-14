// Package soon 是还没做的 tab 的占位模块：主视觉 + 「即将推出」。
package soon

import (
	"github.com/egoist/mygo/ui"

	"vole/internal/i18n"
	"vole/internal/kit"
	"vole/internal/module"
)

type placeholder struct {
	id, title string
	theme     kit.Theme
}

// New 创建一个占位模块。
func New(id, title string, theme kit.Theme) module.Module {
	return &placeholder{id: id, title: title, theme: theme}
}

func (p *placeholder) ID() string       { return p.id }
func (p *placeholder) Title() string    { return p.title }
func (p *placeholder) Theme() kit.Theme { return p.theme }
func (p *placeholder) Init(module.Host) {}
func (p *placeholder) OnActivate()      {}
func (p *placeholder) OnDeactivate()    {}
func (p *placeholder) View(c *ui.Context) {
	t := p.theme
	ui.Column(c).Fill().Center().Gap(16).Padding(0, 0, 60, 0).Children(func() {
		kit.Art(c, kit.ArtComingSoon, kit.ArtM)
		ui.Column(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, p.title+i18n.T("功能即将推出")).FontSize(kit.TextHeadline).SingleLine()
			ui.Text(c, i18n.T("当前版本先做磁盘分析，可以到「分析」看看")).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
		})
	})
}
