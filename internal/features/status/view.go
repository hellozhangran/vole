package status

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/status"
	ui2 "vole/internal/features/statusui"
	"vole/internal/i18n"
	"vole/internal/kit"
)

// View 画状态 tab：首帧前是加载态，之后是 2×4 卡片 + 进程表。
func (m *Module) View(c *ui.Context) {
	t := m.theme
	if m.snap == nil {
		m.empty(c, t)
		return
	}
	s := m.snap
	ui.Column(c).Fill().Padding(12, kit.Gutter, kit.Gutter, kit.Gutter).Gap(12).Children(func() {
		ui.Row(c.Key("row1")).FillWidth().Height(168).Shrink(0).Gap(12).Children(func() {
			m.healthCard(c, t, s)
			m.cpuCard(c, t, s)
			m.gpuCard(c, t, s)
			m.memCard(c, t, s)
		})
		ui.Row(c.Key("row2")).FillWidth().Height(168).Shrink(0).Gap(12).Children(func() {
			m.batteryCard(c, t, s)
			m.diskCard(c, t, s)
			m.netCard(c, t, s)
			m.fanCard(c, t, s)
		})
		m.banner(c, t)
		m.procTable(c, t)
	})
}

// empty 是首帧前：加载动画；或者组件不可用 / 多次重启失败时的错误面板。
func (m *Module) empty(c *ui.Context, t kit.Theme) {
	ui.Column(c).Fill().Center().Gap(10).Padding(0, 40, 80, 40).Children(func() {
		if m.err != nil {
			kit.Art(c, kit.ArtError, kit.ArtS).Margin(0, 0, 4, 0)
			ui.Text(c, i18n.T("状态组件不可用")).FontSize(kit.TextHeadline)
			ui.Text(c, m.err.Error()+i18n.T("。Vole.app 可能不完整或已损坏，请重新安装；开发期请检查 VOLE_STATUS_BIN。")).
				FontSize(kit.TextSecondary).TextColor(t.Muted).TextAlign(ui.Center).MaxWidth(460)
			kit.PillButton(c.Key("retry"), t, i18n.T("重试"), true).Margin(10, 0, 0, 0).OnClick(m.retry)
			return
		}
		kit.RunningVole(c, kit.Hero, kit.RunningFPS, true)
		ui.Text(c, i18n.T("正在读取系统状态…")).FontSize(kit.TextHeadline)
	})
}

// banner 是已经有数据、但采集停了时的提示条（自动重启中 / 需要手动重试）。
func (m *Module) banner(c *ui.Context, t kit.Theme) {
	var text string
	switch {
	case m.err != nil:
		text = i18n.T("状态采集已停止：") + m.err.Error()
	case m.lastExit != nil && !m.running:
		text = i18n.T("状态采集意外退出，正在重启…")
	default:
		return
	}
	ui.Row(c.Key("banner")).FillWidth().Shrink(0).Gap(10).Padding(6, 12).Radius(kit.RadiusM).Background(ui2.Red.Alpha(0.14)).Children(func() {
		ui.Text(c, text).FontSize(kit.TextSecondary).TextColor(t.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if m.err != nil {
			kit.PillButton(c.Key("retry"), t, i18n.T("重试"), false).OnClick(m.retry)
		}
	})
}

// ── 8 张卡 ──

// healthCard 用 status 给的健康分和诊断语（只做中文翻译，不自己判断）。
func (m *Module) healthCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui2.Card(c, t, ui2.Large, "health", ui2.HealthIcon, i18n.T("健康度"), "", 1.45, func() {
		ui.Row(c).Grow(1).FillWidth().MinHeight(0).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Gap(8).Children(func() {
				level, issues := ui2.HealthText(s.HealthNote)
				if s.Health < 0 {
					ui2.Value(c, t, ui2.Large, ui2.Dash, "", "")
				} else {
					ui2.Value(c, t, ui2.Large, fmt.Sprint(s.Health), "", level)
				}
				ui.Text(c, issues).FontSize(kit.TextBody).TextColor(t.Muted).MaxLines(2).FillWidth()
				ui.Spacer(c)
				ui.Row(c).Gap(6).Children(func() {
					for _, tag := range []string{s.Chip, s.OS} {
						if tag != "" {
							ui.Text(c, tag).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Muted).Padding(1, 6).Radius(kit.RadiusS).Background(t.Surface).SingleLine()
						}
					}
				})
				boot := s.At.Add(-s.Uptime)
				ui2.Footer(c, t, i18n.T("已运行 ")+ui2.UptimeText(s.Uptime), i18n.Tf("自 %d月%d日", int(boot.Month()), boot.Day()))
			})
			// 主视觉：田鼠插画，按健康等级换表情（不用 Mole 的太阳）
			ui.Box(c).FillHeight().Center().Shrink(0).Children(func() {
				kit.Art(c, ui2.HealthArt(s.HealthNote, s.Health), kit.Hero)
			})
		})
	})
}

func (m *Module) cpuCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui2.Card(c, t, ui2.Large, "cpu", ui2.CPUIcon, "CPU", ui2.TempTag(s.Thermal.CPUTemp), 1, func() {
		ui2.Value(c, t, ui2.Large, fmt.Sprintf("%.0f", s.CPU.Usage), "%", "")
		ui2.Chart(c, func(p *ui.Painter, r ui.Rect) { ui2.Bars(p, r, s.CPU.PerCore, ui2.Green) })
		cores := fmt.Sprintf(i18n.T("%d 核"), s.CPU.Cores)
		if s.CPU.PCores > 0 {
			cores = fmt.Sprintf(i18n.T("%d 核（%dP+%dE）"), s.CPU.Cores, s.CPU.PCores, s.CPU.ECores)
		}
		ui2.Footer(c, t, fmt.Sprintf(i18n.T("负载 %.1f"), s.CPU.Load1), cores)
	})
}

func (m *Module) gpuCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var g *core.GPU
	if len(s.GPUs) > 0 {
		g = &s.GPUs[0]
	}
	ui2.Card(c, t, ui2.Large, "gpu", ui2.GPUIcon, "GPU", ui2.TempTag(s.Thermal.GPUTemp), 1, func() {
		switch {
		case g == nil && !m.full:
			ui2.Value(c, t, ui2.Large, ui2.Dash, "", "")
			ui2.Note(c, t, ui2.Waiting)
			ui2.Footer(c, t, "")
		case g == nil:
			ui2.Value(c, t, ui2.Large, ui2.Dash, "", "")
			ui2.Note(c, t, i18n.T("没有读到 GPU"))
			ui2.Footer(c, t, "")
		case g.Usage < 0:
			ui2.Value(c, t, ui2.Large, ui2.Dash, "%", "")
			ui2.Note(c, t, i18n.T("占用需要管理员权限才能读取"))
			ui2.Footer(c, t, fmt.Sprintf(i18n.T("%d GPU 核"), g.Cores), g.Name)
		default:
			ui2.Value(c, t, ui2.Large, fmt.Sprintf("%.0f", g.Usage), "%", "")
			ui2.Chart(c, func(p *ui.Painter, r ui.Rect) { ui2.Meter(p, r, g.Usage/100, ui2.Orange) })
			ui2.Footer(c, t, fmt.Sprintf(i18n.T("%d GPU 核"), g.Cores), g.Name)
		}
	})
}

func (m *Module) memCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui2.Card(c, t, ui2.Large, "mem", ui2.MemIcon, i18n.T("内存"), ui2.PressureTag(s.Memory.Pressure), 1, func() {
		ui2.Value(c, t, ui2.Large, fmt.Sprintf("%.0f", s.Memory.UsedPercent), "%", "")
		hist := m.memHist
		ui2.Chart(c, func(p *ui.Painter, r ui.Rect) { ui2.Area(p, r, hist, historyLen, 100, ui2.Gold) })
		ui2.Footer(c, t, kit.HumanSize(int64(s.Memory.Used)), i18n.T("交换空间 ")+kit.HumanSize(int64(s.Memory.SwapUsed)))
	})
}

func (m *Module) batteryCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var b *core.Battery
	if len(s.Batteries) > 0 {
		b = &s.Batteries[0]
	}
	tag := ""
	if b != nil && b.Capacity > 0 {
		tag = fmt.Sprintf(i18n.T("健康 %d%%"), b.Capacity)
	}
	ui2.Card(c, t, ui2.Large, "battery", ui2.BatteryIcon, i18n.T("电池"), tag, 1.45, func() {
		if b == nil {
			ui2.Value(c, t, ui2.Large, ui2.Dash, "", "")
			if m.full {
				ui2.Note(c, t, i18n.T("这台 Mac 没有电池"))
			} else {
				ui2.Note(c, t, ui2.Waiting)
			}
			ui2.Footer(c, t, "")
			return
		}
		ui.Row(c).Grow(1).FillWidth().MinHeight(0).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Gap(8).Children(func() {
				ui2.Value(c, t, ui2.Large, fmt.Sprintf("%.0f", b.Percent), "%", ui2.BatteryState(b))
				ui.Spacer(c)
				temp := ""
				if s.Thermal.BatteryTemp > 0 {
					temp = fmt.Sprintf("%.0f°C", s.Thermal.BatteryTemp)
				}
				ui2.Footer(c, t, fmt.Sprintf(i18n.T("%d 次循环"), b.Cycles), temp, ui2.BatteryHealth(b.Health))
			})
			pct := b.Percent
			ui.Box(c).Size(44, 44).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) { ui2.Ring(p, r, pct/100, ui2.Green) })
		})
	})
}

func (m *Module) diskCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	if len(s.Disks) == 0 {
		ui2.Card(c, t, ui2.Large, "disk", ui2.DiskIcon, i18n.T("磁盘"), "", 1, func() {
			ui2.Value(c, t, ui2.Large, ui2.Dash, "", "")
			ui2.Note(c, t, ui2.Waiting)
			ui2.Footer(c, t, "")
		})
		return
	}
	d := s.Disks[0]
	ui2.Card(c, t, ui2.Large, "disk", ui2.DiskIcon, i18n.T("磁盘"), kit.HumanSize(int64(d.Total)), 1, func() {
		free := kit.HumanSize(int64(d.Total - min(d.Used, d.Total)))
		num, unit, _ := strings.Cut(free, " ")
		ui2.Value(c, t, ui2.Large, num, unit, i18n.T("可用"))
		pct := d.UsedPercent
		ui2.Chart(c, func(p *ui.Painter, r ui.Rect) { ui2.UsageBar(p, r, pct/100) })
		ui2.Footer(c, t, i18n.T("已用 ")+kit.HumanSize(int64(d.Used)), fmt.Sprintf("%.0f%%", d.UsedPercent))
	})
}

func (m *Module) netCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var in, out float64
	name := ""
	for _, i := range s.Net {
		in += i.Rate.In
		out += i.Rate.Out
		if name == "" && i.IP != "" {
			name = i.Name
		}
	}
	ui2.Card(c, t, ui2.Large, "net", ui2.NetIcon, i18n.T("网络"), name, 1, func() {
		num, unit, _ := strings.Cut(ui2.RateText(in), " ")
		ui2.Value(c, t, ui2.Large, num, unit, "")
		h := s.NetHistory
		ui2.Chart(c, func(p *ui.Painter, r ui.Rect) { ui2.Lines(p, r, h.In, h.Out, ui2.Teal, ui2.Blue) })
		ui2.Footer(c, t, "↓ "+ui2.RateText(in), "↑ "+ui2.RateText(out))
	})
}

// fanCard 只读：转速拿不到（非 root、或 Apple 芯片读不到）显示「—」，不显示成 0。
func (m *Module) fanCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui2.Card(c, t, ui2.Large, "fan", ui2.FanIcon, i18n.T("风扇"), "", 1, func() {
		if s.Thermal.FanRPM > 0 {
			ui2.Value(c, t, ui2.Large, fmt.Sprint(s.Thermal.FanRPM), "RPM", "")
			ui2.Note(c, t, "")
			ui2.Footer(c, t, i18n.T("由 macOS 调节"))
			return
		}
		ui2.Value(c, t, ui2.Large, ui2.Dash, "RPM", "")
		if m.full {
			ui2.Note(c, t, i18n.T("当前权限下读不到风扇转速"))
		} else {
			ui2.Note(c, t, ui2.Waiting)
		}
		ui2.Footer(c, t, i18n.T("由 macOS 调节"))
	})
}
