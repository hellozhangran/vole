// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/clean"
	"vole/internal/i18n"
	"vole/internal/kit"
)

// cleaningView 清理中：奔跑田鼠 + 进度 + 当前项 + 取消（当前这一项做完就停）。
func (m *Module) cleaningView(c *ui.Context, t kit.Theme) {
	total := len(m.plan)
	ui.Column(c).Fill().Center().Gap(14).Padding(kit.PageTop, kit.PageX, 50, kit.PageX).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(kit.PageTop).DragWindow()
		kit.RunningVole(c, 1.6*kit.Hero, kit.RunningFPS, true)
		ui.Text(c, i18n.T("清理中")).FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
		frac := float32(0)
		if total > 0 {
			frac = float32(m.runDone) / float32(total)
		}
		ui.Box(c).Width(360).Height(6).Radius(3).Background(t.Surface).Margin(6, 0, 0, 0).Children(func() {
			ui.Box(c).WidthPercent(max(frac, 0.02) * 100).FillHeight().Radius(3).Background(blue)
		})
		ui.Text(c, fmt.Sprintf(i18n.T("第 %d / %d 项 · 已释放 %s"), min(m.runDone+1, total), total, kit.HumanSize(m.runFreed))).
			Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		if m.runDone < total {
			_, pretty, _ := strings.Cut(m.plan[m.runDone].ID, ":")
			ui.Text(c, pretty).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine().Ellipsis("…").MaxWidth(560)
		}
		label := i18n.T("取消")
		if m.cancelling {
			label = i18n.T("正在完成当前这一项…")
		}
		b := kit.PillButton(c.Key("cancel-run"), t, label, false).Margin(10, 0, 0, 0).Disabled(m.cancelling)
		b.OnClick(m.cancelRun)
	})
}

// doneView 完成页：释放了多少；跳过、失败、取消的项及原因；查看日志；返回首屏。
func (m *Module) doneView(c *ui.Context, t kit.Theme) {
	rep := m.report
	var done int
	var problems []core.Outcome
	for _, o := range rep.Outcomes {
		if o.Status == core.StatusDone {
			done++
		} else {
			problems = append(problems, o)
		}
	}
	ui.Column(c).Fill().Center().Gap(12).Padding(kit.PageTop, kit.PageX, 40, kit.PageX).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(kit.PageTop).DragWindow()
		kit.Art(c, kit.ArtCleanDone, kit.ArtM)
		ui.Text(c, i18n.T("已释放 ")+kit.HumanSize(rep.Freed)).Font(kit.Mono).FontSize(kit.TextDisplay).FontWeight(kit.WeightSemibold).SingleLine()
		sub := fmt.Sprintf(i18n.T("清理了 %d 项"), done)
		if rep.Canceled {
			canceled := 0
			for _, o := range rep.Outcomes {
				if o.Status == core.StatusCanceled {
					canceled++
				}
			}
			sub += fmt.Sprintf(i18n.T("；已取消，剩下 %d 项没有处理"), canceled)
		}
		ui.Text(c, sub).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
		if len(problems) > 0 {
			ui.Column(c.Key("problems")).Width(640).MaxHeight(260).Margin(10, 0, 0, 0).Radius(kit.RadiusL).Background(t.Surface).Clip().Children(func() {
				ui.Text(c, fmt.Sprintf(i18n.T("%d 项没有清理"), len(problems))).FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).
					TextColor(t.Muted).Padding(12, 16, 6, 16)
				ui.Scroll(c.Key("list")).FillWidth().MinHeight(0).Grow(1).Children(func() {
					ui.Column(c).FillWidth().Padding(0, 8, 8, 8).Children(func() {
						for _, o := range problems {
							m.problemRow(c, t, o)
						}
					})
				})
			})
		}
		ui.Row(c).Gap(10).Margin(14, 0, 0, 0).Children(func() {
			kit.PillButton(c.Key("log"), t, i18n.T("查看日志"), false).OnClick(func() { m.openPath(m.be.LogDir()) })
			kit.PillButton(c.Key("ok"), t, i18n.T("完成"), true).OnClick(m.finish)
		})
	})
}

func (m *Module) problemRow(c *ui.Context, t kit.Theme, o core.Outcome) {
	color := t.Muted
	if o.Status == core.StatusFailed {
		color = red
	}
	_, pretty, _ := strings.Cut(o.Item.ID, ":")
	ui.Row(c.Key(o.Item.ID)).FillWidth().Gap(10).Padding(7, 8).Children(func() {
		tag(c, i18n.T(string(o.Status)), color)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, pretty).Font(kit.Mono).FontSize(kit.TextCaption).SingleLine().Ellipsis("…").FillWidth()
			if o.Reason != "" {
				ui.Text(c, o.Reason).FontSize(kit.TextCaption).TextColor(t.Faint).MaxLines(2).FillWidth()
			}
		})
	})
}
