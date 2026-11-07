package status

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/status"
	"vole/internal/kit"
)

// 卡片图标（自绘通用线条）。
var (
	healthIcon  = kit.LineIcon(`<path d="M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.6A4 4 0 0 1 19 10c0 5.6-7 10-7 10Z"/>`)
	cpuIcon     = kit.LineIcon(`<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>`)
	gpuIcon     = kit.LineIcon(`<rect x="3" y="5" width="18" height="12" rx="2"/><path d="M8 21h8M12 17v4"/>`)
	memIcon     = kit.LineIcon(`<rect x="3" y="7" width="18" height="10" rx="2"/><path d="M7 7v10M11 7v10M15 7v10"/>`)
	batteryIcon = kit.LineIcon(`<rect x="3" y="7" width="16" height="10" rx="2"/><path d="M21 11v2M6 10v4"/>`)
	diskIcon    = kit.LineIcon(`<rect x="3" y="12" width="18" height="7" rx="2"/><path d="M5.5 12 8 5h8l2.5 7"/><path d="M7 15.5h.01"/>`)
	netIcon     = kit.LineIcon(`<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>`)
	fanIcon     = kit.LineIcon(`<circle cx="12" cy="12" r="2"/><path d="M12 10c0-4 1-7 4-7s3 4-2 7M14 12c4 0 7 1 7 4s-4 3-7-2M12 14c0 4-1 7-4 7s-3-4 2-7M10 12c-4 0-7-1-7-4s4-3 7 2"/>`)
	procIcon    = kit.LineIcon(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 10 3 2-3 2M12 14h4"/>`)
)

var (
	cardBG     = ui.RGBA(255, 255, 255, 0.045)
	cardBorder = ui.RGBA(255, 255, 255, 0.08)
	green      = ui.Hex("#3fae8a")
	orange     = ui.Hex("#e8964a")
	gold       = ui.Hex("#d9b25a")
	teal       = ui.Hex("#4cc3b5")
	blue       = ui.Hex("#5b8def")
	red        = ui.Hex("#e5655a")
)

// dash 是拿不到的值：不能显示成 0。
const dash = "—"

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
			ui.Text(c, "状态组件不可用").FontSize(kit.TextHeadline)
			ui.Text(c, m.err.Error()+"。Vole.app 可能不完整或已损坏，请重新安装；开发期请检查 VOLE_STATUS_BIN。").
				FontSize(kit.TextSecondary).TextColor(t.Muted).TextAlign(ui.Center).MaxWidth(460)
			kit.PillButton(c.Key("retry"), t, "重试", true).Margin(10, 0, 0, 0).OnClick(m.retry)
			return
		}
		kit.RunningVole(c, kit.Hero, kit.RunningFPS, true)
		ui.Text(c, "正在读取系统状态…").FontSize(kit.TextHeadline)
	})
}

// banner 是已经有数据、但采集停了时的提示条（自动重启中 / 需要手动重试）。
func (m *Module) banner(c *ui.Context, t kit.Theme) {
	var text string
	switch {
	case m.err != nil:
		text = "状态采集已停止：" + m.err.Error()
	case m.lastExit != nil && !m.running:
		text = "状态采集意外退出，正在重启…"
	default:
		return
	}
	ui.Row(c.Key("banner")).FillWidth().Shrink(0).Gap(10).Padding(6, 12).Radius(kit.RadiusM).Background(red.Alpha(0.14)).Children(func() {
		ui.Text(c, text).FontSize(kit.TextSecondary).TextColor(t.Text).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		if m.err != nil {
			kit.PillButton(c.Key("retry"), t, "重试", false).OnClick(m.retry)
		}
	})
}

// ── 卡片骨架 ──

// card 是一张指标卡：标题行（图标 + 标题 + 右上小标签）+ 主体。grow 控制宽度占比。
func card(c *ui.Context, t kit.Theme, key string, icon *ui.SVG, title, tag string, grow float32, body func()) {
	ui.Column(c.Key(key)).Grow(grow).Basis(0).MinWidth(0).FillHeight().Padding(14, 16).Gap(8).
		Radius(kit.RadiusL).Background(cardBG).Border(1, cardBorder).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Icon(c, icon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(t.Muted)
			ui.Text(c, title).FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).LetterSpacing(2).TextColor(t.Muted).SingleLine()
			ui.Spacer(c)
			if tag != "" {
				ui.Text(c, tag).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Muted).Padding(1, 6).Radius(kit.RadiusS).Background(t.Surface).SingleLine()
			}
		})
		body()
	})
}

// value 是卡片主数值行：大号数字 + 单位 + 补充。
func value(c *ui.Context, t kit.Theme, num, unit, extra string) {
	ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
		col := t.Text
		if num == dash {
			col = t.Faint
		}
		ui.Text(c, num).Font(kit.Mono).FontSize(kit.TextDisplay).TextColor(col).SingleLine()
		if unit != "" {
			ui.Text(c, unit).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
		}
		if extra != "" {
			ui.Text(c, extra).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		}
	})
}

// chart 是卡片中部的图表区，占满剩余高度。
func chart(c *ui.Context, draw func(p *ui.Painter, r ui.Rect)) {
	ui.Box(c).Grow(1).FillWidth().MinHeight(0).Draw(draw)
}

// note 是图表区里的一句灰字（采集中 / 拿不到）。
func note(c *ui.Context, t kit.Theme, text string) {
	ui.Box(c).Grow(1).FillWidth().MinHeight(0).Justify(ui.Center).Children(func() {
		ui.Text(c, text).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine()
	})
}

// footer 是卡片底部的灰色补充信息。
func footer(c *ui.Context, t kit.Theme, parts ...string) {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	ui.Text(c, strings.Join(keep, " · ")).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine().Ellipsis("…").FillWidth()
}

const waiting = "采集中…"

// ── 8 张卡 ──

// healthCard 用 status 给的健康分和诊断语（只做中文翻译，不自己判断）。
func (m *Module) healthCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	card(c, t, "health", healthIcon, "健康度", "", 1.45, func() {
		ui.Row(c).Grow(1).FillWidth().MinHeight(0).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Gap(8).Children(func() {
				level, issues := healthText(s.HealthNote)
				if s.Health < 0 {
					value(c, t, dash, "", "")
				} else {
					value(c, t, fmt.Sprint(s.Health), "", level)
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
				footer(c, t, "已运行 "+uptimeText(s.Uptime), "自 "+fmt.Sprintf("%d月%d日", boot.Month(), boot.Day()))
			})
			// 主视觉：Vole 自己的程序化星球（不用 Mole 的太阳）
			ui.Box(c).FillHeight().Center().Shrink(0).Children(func() {
				kit.Planet(c, 96, t.Planet, false)
			})
		})
	})
}

func (m *Module) cpuCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	card(c, t, "cpu", cpuIcon, "CPU", tempTag(s.Thermal.CPUTemp), 1, func() {
		value(c, t, fmt.Sprintf("%.0f", s.CPU.Usage), "%", "")
		chart(c, func(p *ui.Painter, r ui.Rect) { bars(p, r, s.CPU.PerCore, green) })
		cores := fmt.Sprintf("%d 核", s.CPU.Cores)
		if s.CPU.PCores > 0 {
			cores = fmt.Sprintf("%d 核（%dP+%dE）", s.CPU.Cores, s.CPU.PCores, s.CPU.ECores)
		}
		footer(c, t, fmt.Sprintf("负载 %.1f", s.CPU.Load1), cores)
	})
}

func (m *Module) gpuCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var g *core.GPU
	if len(s.GPUs) > 0 {
		g = &s.GPUs[0]
	}
	card(c, t, "gpu", gpuIcon, "GPU", tempTag(s.Thermal.GPUTemp), 1, func() {
		switch {
		case g == nil && !m.full:
			value(c, t, dash, "", "")
			note(c, t, waiting)
			footer(c, t, "")
		case g == nil:
			value(c, t, dash, "", "")
			note(c, t, "没有读到 GPU")
			footer(c, t, "")
		case g.Usage < 0:
			value(c, t, dash, "%", "")
			note(c, t, "占用需要管理员权限才能读取")
			footer(c, t, fmt.Sprintf("%d GPU 核", g.Cores), g.Name)
		default:
			value(c, t, fmt.Sprintf("%.0f", g.Usage), "%", "")
			chart(c, func(p *ui.Painter, r ui.Rect) { meter(p, r, g.Usage/100, orange) })
			footer(c, t, fmt.Sprintf("%d GPU 核", g.Cores), g.Name)
		}
	})
}

func (m *Module) memCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	card(c, t, "mem", memIcon, "内存", pressureTag(s.Memory.Pressure), 1, func() {
		value(c, t, fmt.Sprintf("%.0f", s.Memory.UsedPercent), "%", "")
		hist := m.memHist
		chart(c, func(p *ui.Painter, r ui.Rect) { area(p, r, hist, 100, gold) })
		footer(c, t, kit.HumanSize(int64(s.Memory.Used)), "交换空间 "+kit.HumanSize(int64(s.Memory.SwapUsed)))
	})
}

func (m *Module) batteryCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var b *core.Battery
	if len(s.Batteries) > 0 {
		b = &s.Batteries[0]
	}
	tag := ""
	if b != nil && b.Capacity > 0 {
		tag = fmt.Sprintf("健康 %d%%", b.Capacity)
	}
	card(c, t, "battery", batteryIcon, "电池", tag, 1.45, func() {
		if b == nil {
			value(c, t, dash, "", "")
			if m.full {
				note(c, t, "这台 Mac 没有电池")
			} else {
				note(c, t, waiting)
			}
			footer(c, t, "")
			return
		}
		ui.Row(c).Grow(1).FillWidth().MinHeight(0).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Gap(8).Children(func() {
				value(c, t, fmt.Sprintf("%.0f", b.Percent), "%", batteryState(b))
				ui.Spacer(c)
				temp := ""
				if s.Thermal.BatteryTemp > 0 {
					temp = fmt.Sprintf("%.0f°C", s.Thermal.BatteryTemp)
				}
				footer(c, t, fmt.Sprintf("%d 次循环", b.Cycles), temp, batteryHealth(b.Health))
			})
			pct := b.Percent
			ui.Box(c).Size(44, 44).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) { ring(p, r, pct/100, green) })
		})
	})
}

func (m *Module) diskCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	if len(s.Disks) == 0 {
		card(c, t, "disk", diskIcon, "磁盘", "", 1, func() {
			value(c, t, dash, "", "")
			note(c, t, waiting)
			footer(c, t, "")
		})
		return
	}
	d := s.Disks[0]
	card(c, t, "disk", diskIcon, "磁盘", kit.HumanSize(int64(d.Total)), 1, func() {
		free := kit.HumanSize(int64(d.Total - min(d.Used, d.Total)))
		num, unit, _ := strings.Cut(free, " ")
		value(c, t, num, unit, "可用")
		pct := d.UsedPercent
		chart(c, func(p *ui.Painter, r ui.Rect) { usageBar(p, r, pct/100) })
		footer(c, t, "已用 "+kit.HumanSize(int64(d.Used)), fmt.Sprintf("%.0f%%", d.UsedPercent))
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
	card(c, t, "net", netIcon, "网络", name, 1, func() {
		num, unit, _ := strings.Cut(rateText(in), " ")
		value(c, t, num, unit, "")
		h := s.NetHistory
		chart(c, func(p *ui.Painter, r ui.Rect) { lines(p, r, h.In, h.Out, teal, blue) })
		footer(c, t, "↓ "+rateText(in), "↑ "+rateText(out))
	})
}

// fanCard 只读：转速拿不到（非 root、或 Apple 芯片读不到）显示「—」，不显示成 0。
func (m *Module) fanCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	card(c, t, "fan", fanIcon, "风扇", "", 1, func() {
		if s.Thermal.FanRPM > 0 {
			value(c, t, fmt.Sprint(s.Thermal.FanRPM), "RPM", "")
			note(c, t, "")
			footer(c, t, "由 macOS 调节")
			return
		}
		value(c, t, dash, "RPM", "")
		if m.full {
			note(c, t, "当前权限下读不到风扇转速")
		} else {
			note(c, t, waiting)
		}
		footer(c, t, "由 macOS 调节")
	})
}

// ── 文案 ──

// 健康度文案：status 输出 "Good: High CPU, Disk Almost Full"，这里只把它的固定词表翻成中文，认不出的原样显示。
var healthLevels = map[string]string{"Excellent": "优秀", "Good": "良好", "Fair": "一般", "Needs Attention": "需要关注"}
var healthIssues = map[string]string{
	"High CPU": "CPU 负载偏高", "High Memory": "内存占用偏高", "Memory Pressure": "内存压力大",
	"Critical Memory": "内存严重不足", "Disk Almost Full": "磁盘快满了", "Disk SMART Failing": "磁盘 SMART 异常",
	"Overheating": "温度过高", "Heavy Disk IO": "磁盘读写繁重", "Battery Service Soon": "电池需要维修",
	"Restart Recommended": "建议重启",
}

func healthText(msg string) (level, issues string) {
	head, tail, _ := strings.Cut(msg, ":")
	level = strings.TrimSpace(head)
	if zh, ok := healthLevels[level]; ok {
		level = zh
	}
	var out []string
	for _, it := range strings.Split(tail, ",") {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if zh, ok := healthIssues[it]; ok {
			it = zh
		}
		out = append(out, it)
	}
	if len(out) == 0 {
		return level, "没有发现问题"
	}
	return level, strings.Join(out, "，")
}

func tempTag(v float64) string {
	if v <= 0 {
		return "" // 拿不到就不显示，不写 0°C
	}
	return fmt.Sprintf("%.0f°C", v)
}

func pressureTag(p string) string {
	switch p {
	case "normal":
		return "压力 正常"
	case "warn":
		return "压力 偏高"
	case "critical":
		return "压力 严重"
	}
	return ""
}

func batteryState(b *core.Battery) string {
	switch b.Status {
	case "charging":
		return "充电中"
	case "charged":
		return "已充满"
	}
	if b.TimeLeft != "" {
		return "可用 " + b.TimeLeft
	}
	return ""
}

func batteryHealth(h string) string {
	if h == "" {
		return ""
	}
	return "状况 " + h
}

func uptimeText(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
}

// rateText 按 1000 进制格式化速率（字节/秒）。
func rateText(bps float64) string {
	return kit.HumanSize(int64(bps)) + "/s"
}
