package status

import (
	"fmt"
	"sort"
	"strings"
	ui2 "vole/internal/features/statusui"
	"vole/internal/i18n"

	"github.com/egoist/mygo/ui"

	"vole/internal/core/procs"
	"vole/internal/kit"
)

const (
	colPID  = 80
	colCPU  = 170
	colMem  = 90
	colMore = 28
)

var (
	yellow  = ui.Hex("#e8b04a")
	pinIcon = kit.LineIcon(`<path d="M9 4h6l-1 6 3 3H7l3-3-1-6Z"/><path d="M12 13v7"/>`)
	dots    = kit.LineIcon(`<circle cx="6" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="18" cy="12" r="1"/>`)
)

// sortedProcs 按当前排序列排好；固定的行始终排在最上面（固定行之间也按当前列排）。
func (m *Module) sortedProcs() []procs.Proc {
	ps := append([]procs.Proc(nil), m.pt.list...)
	less := func(a, b procs.Proc) bool {
		switch m.sortBy {
		case byName:
			an, _ := displayName(a)
			bn, _ := displayName(b)
			return strings.ToLower(an) < strings.ToLower(bn)
		case byPID:
			return a.PID < b.PID
		case byMem:
			return a.MemBytes < b.MemBytes
		}
		return a.CPU < b.CPU
	}
	sort.SliceStable(ps, func(i, j int) bool {
		pi, pj := m.pinned(ps[i]), m.pinned(ps[j])
		if pi != pj {
			return pi
		}
		if m.desc {
			return less(ps[j], ps[i])
		}
		return less(ps[i], ps[j])
	})
	return ps
}

// procTable 进程表：名称 / PID / CPU / 内存 / 操作，点表头排序。数据是 Vole 自己用 ps 采的 CPU 前 50。
func (m *Module) procTable(c *ui.Context, t kit.Theme) {
	ui.Column(c.Key("procs")).Grow(1).FillWidth().MinHeight(0).Radius(kit.RadiusL).Background(ui2.CardBG).Border(1, ui2.CardBorder).Clip().Children(func() {
		ui.Row(c.Key("thead")).FillWidth().Height(32).Shrink(0).Padding(0, 16).Gap(8).
			Border(1, ui2.CardBorder).BorderWidth(0, 0, 1, 0).Children(func() {
			m.th(c, t, fmt.Sprintf(i18n.T("名称（%d）"), len(m.pt.list)), byName, 0, false)
			m.th(c, t, "PID", byPID, colPID, true)
			m.th(c, t, "CPU", byCPU, colCPU, true)
			m.th(c, t, i18n.T("内存"), byMem, colMem, true)
			ui.Box(c).Width(colMore).Shrink(0)
		})
		ui.Scroll(c.Key("tbody")).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				if len(m.pt.list) == 0 {
					msg := ui2.Waiting
					if m.pt.err != nil {
						msg = i18n.T("读取进程列表失败：") + m.pt.err.Error()
					}
					ui.Text(c, msg).FontSize(kit.TextSecondary).TextColor(t.Faint).Padding(12, 16)
					return
				}
				for _, p := range m.sortedProcs() {
					m.procRow(c, t, p)
				}
			})
		})
	})
}

// th 是可点击排序的表头格；当前排序列显示方向箭头。width 为 0 表示占满剩余宽度。
func (m *Module) th(c *ui.Context, t kit.Theme, label string, col sortCol, width float32, right bool) {
	b := ui.ButtonBase(c.Key(fmt.Sprint("th-", col))).Label(i18n.Tf("按%s排序", strings.Split(label, i18n.T("（"))[0])).Height(32).Gap(4).TextColor(t.Faint)
	if width > 0 {
		b.Width(width).Shrink(0)
	} else {
		b.Grow(1).MinWidth(0)
	}
	if right {
		b.Justify(ui.End)
	} else {
		b.Justify(ui.Start)
	}
	if m.sortBy == col || b.Hovered() {
		b.TextColor(t.Text)
	}
	b.Children(func() {
		if m.sortBy == col {
			arrow := "↑"
			if m.desc {
				arrow = "↓"
			}
			ui.Text(c, arrow).FontSize(kit.TextCaption).SingleLine()
		}
		ui.Text(c, label).FontSize(kit.TextCaption).FontWeight(kit.WeightSemibold).LetterSpacing(1.5).SingleLine()
	})
	b.OnClick(func() { m.setSort(col) })
}

// procRow 一行进程。持续高 CPU 的行左侧加色条、数值变色（黄 / 红两档，阈值见 procs.go）。
func (m *Module) procRow(c *ui.Context, t kit.Theme, p procs.Proc) {
	name, sub := displayName(p)
	row := ui.Row(c.Key(keyOf(p))).Label(i18n.T("进程 ")+p.Name).FillWidth().Height(kit.RowTable).Padding(0, 16).Gap(8)
	if row.Hovered() {
		row.Background(t.Hover)
	}
	row.ContextMenu(func(menu *ui.Menu) { m.procMenu(menu, p) })

	lv := m.levelOf(p)
	bar, val := ui.RGBA(255, 255, 255, 0.45), t.Text
	switch lv {
	case levelHot:
		bar, val = ui2.Red, ui2.Red
	case levelWarn:
		bar, val = yellow, yellow
	}
	row.Children(func() {
		if lv != levelNone {
			ui.Box(c).Absolute().Left(6).Top(10).Width(3).Height(18).Radius(2).Background(bar)
		}
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			if b := m.icon(p); b != nil {
				ui.Image(c, b).Size(kit.IconL, kit.IconL).Shrink(0)
			} else {
				ui.Icon(c, ui2.ProcIcon).Size(kit.IconL, kit.IconL).Shrink(0).TextColor(t.Muted)
			}
			ui.Text(c, name).FontSize(kit.TextBody).FontWeight(kit.WeightSemibold).SingleLine().Ellipsis("…").MinWidth(0).Shrink(0)
			if sub != "" {
				ui.Text(c, sub).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine().Ellipsis("…").MinWidth(0)
			}
			if m.pinned(p) {
				ui.Icon(c, pinIcon).Size(kit.IconS, kit.IconS).Shrink(0).TextColor(t.Accent).Tooltip(i18n.T("已固定在顶部"))
			}
		})
		ui.Text(c, fmt.Sprint(p.PID)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(colPID).Shrink(0).TextAlign(ui.End).SingleLine()
		ui.Row(c).Width(colCPU).Shrink(0).Gap(10).Justify(ui.End).Children(func() {
			frac := float32(ui2.Clamp01(p.CPU / 100))
			ui.Box(c).Width(72).Height(6).Radius(3).Background(ui.RGBA(255, 255, 255, 0.06)).Children(func() {
				ui.Box(c).WidthPercent(max(frac, 0.04) * 100).FillHeight().Radius(3).Background(bar)
			})
			ui.Text(c, fmt.Sprintf("%.1f", p.CPU)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(val).Width(52).TextAlign(ui.End).SingleLine()
		})
		mem := ui2.Dash
		if p.MemBytes > 0 {
			mem = kit.HumanSize(int64(p.MemBytes))
		}
		ui.Text(c, mem).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(colMem).Shrink(0).TextAlign(ui.End).SingleLine()
		more := ui.ButtonBase(c.Key("more")).Label(i18n.T("操作 ")+p.Name).Size(colMore, 24).Shrink(0).Radius(kit.RadiusS).TextColor(t.Muted)
		if more.Hovered() {
			more.Background(t.Hover).TextColor(t.Text)
		}
		more.Children(func() { ui.Icon(c, dots).Size(kit.IconL, kit.IconL) })
		more.Menu(func(menu *ui.Menu) { m.procMenu(menu, p) })
	})
}
