// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"vole/internal/i18n"
	"vole/internal/kit"
)

var (
	blue   = ui.Hex("#4b9dff") // 勾选框、大小数字（参照 Mole 结果页的蓝）
	orange = ui.Hex("#f0a04b") // 「需确认」标签
	red    = ui.Hex("#f08a7a")
	green  = ui.Hex("#34d399")
	white  = ui.Hex("#ffffff")
	ink    = ui.Hex("#111111")
)

// View 按阶段画页面。
func (m *Module) View(c *ui.Context) {
	t := m.theme
	switch m.phase {
	case phaseScanning:
		m.scanningView(c, t)
	case phaseResults:
		m.resultsView(c, t)
	case phaseCleaning:
		m.cleaningView(c, t)
	case phaseDone:
		m.doneView(c, t)
	default:
		m.homeView(c, t)
	}
}

// homeView 首屏：主视觉 + 标语 + 白色大按钮「扫描」+ 上次清理。
func (m *Module) homeView(c *ui.Context, t kit.Theme) {
	ui.Column(c).Fill().Center().Gap(20).Padding(0, 0, 50, 0).Children(func() {
		kit.Art(c, kit.ArtCleanHome, kit.ArtL).Margin(0, 0, 4, 0) // 插画自带留白，和标语之间收紧一些
		ui.Text(c, i18n.T("翻一翻土，给磁盘透透气")).FontSize(kit.TextHeadline).TextColor(t.Muted).SingleLine()
		b := ui.ButtonBase(c.Key("scan")).Label(i18n.T("扫描")).Width(240).Height(kit.ButtonL).Radius(kit.ButtonL/2).
			Background(white).TextColor(ink).Shadow(0, 6, 24, 0, ui.RGBA(0, 0, 0, 0.35)).Margin(8, 0, 0, 0)
		if b.Hovered() {
			b.Background(ui.Hex("#f0f2f7"))
		}
		b.Children(func() {
			ui.Text(c, i18n.T("扫描")).FontSize(kit.TextTitle).FontWeight(kit.WeightSemibold).SingleLine()
		})
		b.OnClick(m.startScan)
		if m.scanErr != nil {
			ui.Text(c, i18n.T("扫描失败：")+m.scanErr.Error()).FontSize(kit.TextSecondary).TextColor(red).MaxWidth(480).TextAlign(ui.Center)
		} else if m.lastOK {
			ui.Text(c, fmt.Sprintf(i18n.T("上次清理：%s，释放 %s"), m.lastAt.Local().Format(i18n.T("1月2日 15:04")), kit.HumanSize(m.lastFreed))).
				FontSize(kit.TextSecondary).TextColor(t.Faint).SingleLine()
		}
	})
}

// scanningView 扫描中：奔跑田鼠 + 大字「扫描中」+ 正在扫的类别 + 取消。
func (m *Module) scanningView(c *ui.Context, t kit.Theme) {
	ui.Column(c).Fill().Center().Gap(16).Padding(0, 0, 50, 0).Children(func() {
		kit.RunningVole(c, 2*kit.Hero, kit.RunningFPS, true).Margin(0, 0, 10, 0)
		ui.Text(c, i18n.T("扫描中")).FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
		ui.Row(c).Gap(8).Children(func() {
			kit.Dot(c, blue, 6)
			what := i18n.T("正在准备")
			if len(m.running) > 0 {
				what = i18n.T("正在查找") + strings.Join(m.running, i18n.T("、"))
			}
			ui.Text(c, what).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		})
		kit.PillButton(c.Key("cancel-scan"), t, i18n.T("取消"), false).Margin(10, 0, 0, 0).OnClick(m.cancelScan)
	})
}
