// SPDX-License-Identifier: GPL-3.0-only

// Package menubar 是菜单栏快捷面板：点菜单栏的田鼠图标弹出，布局照 Mole 的菜单栏面板（.local/dev/mole-ui/18）：
// 头部健康分和机器信息、CPU / 内存 / 磁盘 / 网络四张卡、电池、高占用进程，底部一排快捷按钮和「清理观察」。
// 卡片和图表跟状态 tab 共用 features/statusui；数据全部来自 core/status（和状态 tab 共用一个采集进程），
// 只在面板打开时采集，关上就停。状态只在主线程读写。
package menubar

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"vole/internal/core/fsops"
	core "vole/internal/core/status"
	ui2 "vole/internal/features/statusui"
	"vole/internal/kit"
	"vole/internal/task"
)

// 菜单栏图标：田鼠侧面剪影，template 图标（黑色 + 透明，系统按深浅色着色）。
// 由 .local/dev/menubar/make_icons.py 从奔跑帧提取（候选 a2-side-ear），32px = 16pt @2x。
//
//go:embed tray@2x.png
var TrayIcon []byte

// Width 是面板宽度（pt）；高度随内容变（有没有电池、有没有清理记录），见 Height。
const Width = 340

const (
	interval   = time.Second // 采集间隔（和状态 tab 一样，共用一个进程）
	historyLen = 60          // 内存曲线保留的点数（约 1 分钟，同状态 tab）
	topN       = 3           // 高占用进程显示几个
)

// 各段的高度（pt）。Height 按这些加出面板总高，测试里核对最后一段没有超出面板。
const (
	pad      = 12 // 面板四周留白
	gap      = 10 // 段与段的间距
	headH    = 66 // 健康分一行 + 标签一行
	cardH    = 122
	batteryH = 92
	procRowH = 26
	procsH   = 12*2 + 18 + 6 + procRowH*topN // 标题行 + 3 行
	buttonH  = kit.ControlL
	toolsH   = 10*2 + buttonH // 底部卡：一排按钮
	observeH = 1 + 8*2 + 18   // 底部卡里的分隔线 + 「清理观察」一行
	errLineH = 18             // 开机启动设置失败的提示
)

// Host 是面板要外壳做的事。
type Host interface {
	ShowMain(tab string) // 打开（必要时重新显示）主窗口并切到某个 tab；tab 为空 = 保持当前
	Quit()
}

// Login 是「开机时启动」开关的后端（mygo.App.SetOpenAtLogin / OpenAtLogin）。
type Login struct {
	Enabled  func() bool
	Set      func(bool) error
	Disabled string // 非空：开关置灰，写原因（如开发版不是 .app，系统不让注册）
}

// Options 是面板的数据来源。
type Options struct {
	Source core.Source              // 用 core/status.Shared（和状态 tab 同一个）
	Host   Host                     // 外壳
	Login  Login                    // 开机时启动
	Freed  func() fsops.Freed       // 读操作日志里的累计清理量（每次弹出读一次）
	Ports  func() map[string]string // 网卡 → 硬件端口名（第一次弹出时读一次）
}

// Panel 是面板。
type Panel struct {
	o     Options
	theme kit.Theme
	tasks *task.Runner

	open     bool
	snap     *core.Snapshot
	full     bool // 收到过带慢指标（电池、进程、硬件）的帧
	memHist  []float64
	err      error // 采集器不可用
	freed    fsops.Freed
	ports    map[string]string
	loginOn  bool
	loginErr string
}

// New 创建面板。
func New(o Options) *Panel { return &Panel{o: o, theme: kit.Olive()} }

// Init 给面板一个跑后台任务的 Runner（回调排到面板窗口的主线程）。
func (p *Panel) Init(tasks *task.Runner) { p.tasks = tasks }

// Show 面板弹出：开始采集，读累计清理量、开机启动状态，第一次还读网卡类型。
func (p *Panel) Show() {
	if p.open {
		return
	}
	p.open, p.err = true, nil
	if p.o.Login.Enabled != nil && p.o.Login.Disabled == "" {
		p.loginOn = p.o.Login.Enabled()
	}
	src := p.o.Source
	task.Stream(p.tasks, "watch",
		func(ctx context.Context, emit func(core.Snapshot)) error { return src.Watch(ctx, interval, emit) },
		p.onFrame, func(err error) { p.err = err })
	if p.o.Freed != nil {
		read := p.o.Freed
		task.Go(p.tasks, "freed", func(context.Context) (fsops.Freed, error) { return read(), nil },
			func(f fsops.Freed, err error) {
				if err == nil {
					p.freed = f
				}
			})
	}
	if p.ports == nil && p.o.Ports != nil {
		read := p.o.Ports
		task.Go(p.tasks, "ports", func(context.Context) (map[string]string, error) { return read(), nil },
			func(m map[string]string, err error) {
				if err == nil {
					p.ports = m
				}
			})
	}
}

// Hide 面板收起：停掉采集（共享进程里没有别的订阅者时，status 进程随之退出）。
func (p *Panel) Hide() {
	if !p.open {
		return
	}
	p.open = false
	p.tasks.Cancel("watch")
}

func (p *Panel) onFrame(s core.Snapshot) {
	p.snap, p.err = &s, nil
	if s.Model != "" {
		p.full = true
	}
	p.memHist = append(p.memHist, s.Memory.UsedPercent)
	if len(p.memHist) > historyLen {
		p.memHist = p.memHist[len(p.memHist)-historyLen:]
	}
}

// showBattery：有电池才显示电池卡；还没收到慢指标时先留着位置（笔记本占多数，免得弹出后再变高）。
func (p *Panel) showBattery() bool {
	return !p.full || (p.snap != nil && len(p.snap.Batteries) > 0)
}

// Height 是面板现在该有的高度（pt）：外壳在每次更新后按它调面板大小。
func (p *Panel) Height() int {
	h := pad*2 + headH + gap + cardH + gap + cardH + gap + procsH + gap + toolsH
	if p.showBattery() {
		h += gap + batteryH
	}
	if p.freed.Times > 0 {
		h += observeH
	}
	if p.loginErr != "" {
		h += errLineH
	}
	return int(math.Ceil(float64(h)))
}

var (
	openIcon    = kit.LineIcon(`<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M3 9h18"/>`)
	cleanIcon   = kit.LineIcon(`<path d="m14.5 6.5 3 3M4 20l8.5-8.5M14 4l6 6-3 3-6-6 3-3Z"/>`)
	loginIcon   = kit.LineIcon(`<path d="M12 3v9"/><path d="M6.5 6.5a8 8 0 1 0 11 0"/>`)
	quitIcon    = kit.LineIcon(`<path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4"/><path d="M10 8l-4 4 4 4M6 12h10"/>`)
	sparkleIcon = kit.LineIcon(`<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8Z"/><path d="M19 16v4M17 18h4"/>`)
)

// View 画面板。首帧之前各处显示占位（—、采集中…），不留空白。
func (p *Panel) View(c *ui.Context) {
	t := p.theme
	var s core.Snapshot
	if p.snap != nil {
		s = *p.snap
	}
	ui.Column(c).Fill().Padding(pad).Gap(gap).Radius(14).
		LinearGradient(ui.LinearGradient{From: t.BgTop, To: t.BgBottom, Angle: 180, Oklab: true}).
		Border(1, ui.RGBA(255, 255, 255, 0.08)).TextColor(t.Text).FontSize(kit.TextBody).Children(func() {
		p.header(c, t, &s)
		ui.Row(c.Key("row1")).FillWidth().Height(cardH).Shrink(0).Gap(gap).Children(func() {
			p.cpuCard(c, t, &s)
			p.memCard(c, t, &s)
		})
		ui.Row(c.Key("row2")).FillWidth().Height(cardH).Shrink(0).Gap(gap).Children(func() {
			p.diskCard(c, t, &s)
			p.netCard(c, t, &s)
		})
		if p.showBattery() {
			ui.Row(c.Key("row3")).FillWidth().Height(batteryH).Shrink(0).Children(func() { p.batteryCard(c, t, &s) })
		}
		p.procs(c, t, &s)
		p.tools(c, t)
	})
}

// header：健康插画 + 健康分 + 诊断 · 可用空间；下面一排标签（芯片、内存容量、系统版本、开机时长）。
func (p *Panel) header(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui.Column(c.Key("head")).FillWidth().Height(headH).Shrink(0).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Height(40).Gap(8).Children(func() {
			kit.Art(c, ui2.HealthArt(s.HealthNote, healthScore(p.snap)), 40)
			score := ui2.Dash
			if p.snap != nil && s.Health >= 0 {
				score = fmt.Sprint(s.Health)
			}
			ui.Text(c, score).Font(kit.Mono).FontSize(kit.TextDisplay).TextColor(t.Text).SingleLine().Shrink(0)
			ui.Text(c, p.diagnosis(s)).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		})
		ui.Row(c).FillWidth().Gap(6).Children(func() {
			for _, tag := range tags(p.snap) {
				ui2.Tag(c, t, tag).Shrink(0)
			}
		})
	})
}

func healthScore(s *core.Snapshot) int {
	if s == nil {
		return -1
	}
	return s.Health
}

// diagnosis：status 的第一个问题（没有问题时写等级），后面跟可用空间。
func (p *Panel) diagnosis(s *core.Snapshot) string {
	switch {
	case p.snap == nil && p.err != nil:
		return "状态组件不可用"
	case p.snap == nil:
		return ui2.Waiting
	}
	level, issues := ui2.HealthText(s.HealthNote)
	text := level
	if _, tail, _ := strings.Cut(s.HealthNote, ":"); strings.TrimSpace(tail) != "" {
		text, _, _ = strings.Cut(issues, "，")
	}
	if len(s.Disks) > 0 {
		d := s.Disks[0]
		free := kit.HumanSize(int64(d.Total-min(d.Used, d.Total))) + " 可用"
		if text == "" {
			return free
		}
		return text + " · " + free
	}
	return text
}

// tags：芯片（去掉「Apple」）、内存容量（按 GiB 取整，同「关于本机」）、系统版本、开机时长。拿不到的不显示。
func tags(s *core.Snapshot) []string {
	if s == nil {
		return nil
	}
	var out []string
	if chip := strings.TrimPrefix(s.Chip, "Apple "); chip != "" {
		out = append(out, chip)
	}
	if s.Memory.Total > 0 {
		out = append(out, fmt.Sprintf("%.0f GB", math.Round(float64(s.Memory.Total)/(1<<30))))
	}
	if s.OS != "" {
		out = append(out, s.OS)
	}
	if s.Uptime > 0 {
		out = append(out, "已运行 "+ui2.UptimeText(s.Uptime))
	}
	return out
}

// ── 四张卡 ──

func (p *Panel) cpuCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui2.Card(c, t, ui2.Compact, "cpu", ui2.CPUIcon, "CPU", ui2.TempTag(s.Thermal.CPUTemp), 1, func() {
		if p.snap == nil {
			waitingBody(c, t)
			return
		}
		ui2.Value(c, t, ui2.Compact, fmt.Sprintf("%.0f", s.CPU.Usage), "%", "")
		cores := s.CPU.PerCore
		ui2.Chart(c, func(pt *ui.Painter, r ui.Rect) { ui2.Bars(pt, r, cores, ui2.Green) })
		ui2.Footer(c, t, busyText(s.CPU), fmt.Sprintf("负载 %.1f/%d", s.CPU.Load1, s.CPU.Cores))
	})
}

// busyText：1 分钟负载和核数比。到核数算「繁忙」，过半「适中」，否则「空闲」（两个字，卡片底部放得下）。
func busyText(cpu core.CPU) string {
	if cpu.Cores <= 0 {
		return ""
	}
	r := cpu.Load1 / float64(cpu.Cores)
	switch {
	case r >= 1:
		return "繁忙"
	case r >= 0.5:
		return "适中"
	}
	return "空闲"
}

func (p *Panel) memCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	ui2.Card(c, t, ui2.Compact, "mem", ui2.MemIcon, "内存", ui2.PressureTag(s.Memory.Pressure), 1, func() {
		if p.snap == nil {
			waitingBody(c, t)
			return
		}
		ui2.Value(c, t, ui2.Compact, fmt.Sprintf("%.0f", s.Memory.UsedPercent), "%", "")
		hist := p.memHist
		ui2.Chart(c, func(pt *ui.Painter, r ui.Rect) { ui2.Area(pt, r, hist, historyLen, 100, ui2.Gold) })
		ui2.Footer(c, t, "交换空间 "+kit.HumanSize(int64(s.Memory.SwapUsed)))
	})
}

func (p *Panel) diskCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	if len(s.Disks) == 0 {
		ui2.Card(c, t, ui2.Compact, "disk", ui2.DiskIcon, "磁盘", "", 1, func() { waitingBody(c, t) })
		return
	}
	d := s.Disks[0]
	ui2.Card(c, t, ui2.Compact, "disk", ui2.DiskIcon, "磁盘", kit.HumanSize(int64(d.Total)), 1, func() {
		ui2.Value(c, t, ui2.Compact, fmt.Sprintf("%.0f", d.UsedPercent), "%", "")
		frac := d.UsedPercent / 100
		ui2.Chart(c, func(pt *ui.Painter, r ui.Rect) { ui2.UsageBar(pt, r, frac) })
		ui2.Footer(c, t, "可用 "+kit.HumanSize(int64(d.Total-min(d.Used, d.Total))))
	})
}

func (p *Panel) netCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var in, out float64
	name := ""
	for _, i := range s.Net {
		in += i.Rate.In
		out += i.Rate.Out
		if name == "" && i.IP != "" {
			name = i.Name
		}
	}
	ui2.Card(c, t, ui2.Compact, "net", ui2.NetIcon, "网络", connType(name, p.ports), 1, func() {
		if p.snap == nil {
			waitingBody(c, t)
			return
		}
		num, unit, _ := strings.Cut(ui2.RateText(in+out), " ")
		ui2.Value(c, t, ui2.Compact, num, unit, "")
		h := s.NetHistory
		ui2.Chart(c, func(pt *ui.Painter, r ui.Rect) { ui2.Lines(pt, r, h.In, h.Out, ui2.Teal, ui2.Blue) })
		ui2.Footer(c, t, "↑ "+shortSize(out), "↓ "+shortSize(in))
	})
}

// connType 按硬件端口名写连接类型：Wi-Fi、以太网；认不出的写端口名，没有端口名的写网卡名（如 utun3）。
func connType(dev string, ports map[string]string) string {
	port := ports[dev]
	switch {
	case port == "Wi-Fi":
		return "Wi-Fi"
	case strings.Contains(port, "Ethernet") || strings.Contains(port, "LAN"):
		return "以太网"
	case port != "":
		return port
	}
	return dev
}

// shortSize：1.50 MB → 1.5M，91.0 KB → 91K（卡片底部放得下上下行两个数）。
func shortSize(bps float64) string {
	num, unit, _ := strings.Cut(kit.HumanSize(int64(bps)), " ")
	if strings.Contains(num, ".") {
		num = strings.TrimRight(strings.TrimRight(num, "0"), ".")
	}
	if unit == "B" {
		return num
	}
	return num + unit[:1]
}

func waitingBody(c *ui.Context, t kit.Theme) {
	ui2.Value(c, t, ui2.Compact, ui2.Dash, "", "")
	ui2.Note(c, t, ui2.Waiting)
}

// batteryCard：电量、剩余时间或充电状态、此刻 CPU 最高的进程，右边一个电量环。
// Mole 写「最大消耗」；status 不给耗电数据，这里如实写成 CPU 最高的进程。
func (p *Panel) batteryCard(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	var b *core.Battery
	if len(s.Batteries) > 0 {
		b = &s.Batteries[0]
	}
	tag := ""
	if b != nil && b.Capacity > 0 {
		tag = fmt.Sprintf("健康 %d%%", b.Capacity)
	}
	ui2.Card(c, t, ui2.Compact, "battery", ui2.BatteryIcon, "电池", tag, 1, func() {
		if b == nil {
			waitingBody(c, t)
			return
		}
		ui.Row(c).Grow(1).FillWidth().MinHeight(0).Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Gap(6).Children(func() {
				ui2.Value(c, t, ui2.Compact, fmt.Sprintf("%.0f", b.Percent), "%", ui2.BatteryState(b))
				ui.Spacer(c)
				top := ""
				if procs := topProcs(s.TopProcs, 1); len(procs) > 0 {
					top = fmt.Sprintf("CPU 最高 %s %.1f%%", procs[0].Name, procs[0].CPU)
				}
				ui2.Footer(c, t, top)
			})
			pct := b.Percent
			ui.Box(c).Size(44, 44).Shrink(0).Draw(func(pt *ui.Painter, r ui.Rect) { ui2.Ring(pt, r, pct/100, ui2.Green) })
		})
	})
}

// topProcs 按 CPU 从高到低取前 n 个（status 每帧带的前几名，不另外跑 ps）。
func topProcs(all []core.Proc, n int) []core.Proc {
	ps := slices.Clone(all)
	slices.SortStableFunc(ps, func(a, b core.Proc) int { return cmp.Compare(b.CPU, a.CPU) })
	return ps[:min(n, len(ps))]
}

// procs：高占用进程前 3 个（名称、CPU、内存）；点一行打开主窗口的状态 tab。
func (p *Panel) procs(c *ui.Context, t kit.Theme, s *core.Snapshot) {
	const cpuW, memW = 56, 72
	ui.Column(c.Key("procs")).FillWidth().Height(procsH).Shrink(0).Padding(12, ui2.Compact.PadX).Gap(6).
		Radius(kit.RadiusL).Background(ui2.CardBG).Border(1, ui2.CardBorder).Children(func() {
		ui.Row(c).FillWidth().Height(18).Gap(8).Children(func() {
			ui.Icon(c, ui2.ProcIcon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(t.Muted)
			ui.Text(c, "高占用进程").FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).LetterSpacing(2).TextColor(t.Muted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "CPU").FontSize(kit.TextCaption).TextColor(t.Faint).Width(cpuW).TextAlign(ui.End).SingleLine()
			ui.Text(c, "内存").FontSize(kit.TextCaption).TextColor(t.Faint).Width(memW).TextAlign(ui.End).SingleLine()
		})
		list := topProcs(s.TopProcs, topN)
		if len(list) == 0 {
			ui2.Note(c, t, ui2.Waiting)
			return
		}
		ui.Column(c).FillWidth().Children(func() { // 行之间不留间距，行高已经够
			for _, pr := range list {
				b := ui.ButtonBase(c.Key(fmt.Sprint("proc-", pr.PID))).Label("查看进程 "+pr.Name).FillWidth().Height(procRowH).
					Padding(0, 6).Margin(0, -6).Gap(8).Radius(kit.RadiusS).Justify(ui.Start)
				if b.Hovered() {
					b.Background(t.Hover)
				}
				b.Children(func() {
					ui.Text(c, pr.Name).FontSize(kit.TextBody).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
					ui.Text(c, fmt.Sprintf("%.1f%%", pr.CPU)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(cpuW).TextAlign(ui.End).SingleLine()
					ui.Text(c, kit.HumanSize(int64(pr.MemBytes))).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(memW).TextAlign(ui.End).SingleLine()
				})
				b.OnClick(func() { p.o.Host.ShowMain("status") })
			}
		})
	})
}

// tools：底部卡。一排小图标按钮（打开 Vole、扫描清理 | 开机时启动、退出）；有清理记录时下面一行「清理观察」。
func (p *Panel) tools(c *ui.Context, t kit.Theme) {
	ui.Column(c.Key("tools")).FillWidth().Shrink(0).Padding(10, ui2.Compact.PadX).
		Radius(kit.RadiusL).Background(ui2.CardBG).Border(1, ui2.CardBorder).Children(func() {
		ui.Row(c).FillWidth().Height(buttonH).Gap(6).Children(func() {
			p.iconButton(c, t, "open", openIcon, "打开 Vole", false, false, func() { p.o.Host.ShowMain("") })
			p.iconButton(c, t, "clean", cleanIcon, "扫描清理", false, false, func() { p.o.Host.ShowMain("clean") })
			ui.Spacer(c)
			p.loginButton(c, t)
			p.iconButton(c, t, "quit", quitIcon, "退出 Vole", false, false, p.o.Host.Quit)
		})
		if p.loginErr != "" {
			ui.Text(c, p.loginErr).FontSize(kit.TextCaption).TextColor(ui2.Red).Height(errLineH).SingleLine().Ellipsis("…").FillWidth()
		}
		if p.freed.Times > 0 {
			ui.Box(c).FillWidth().Height(1).Margin(8, 0).Background(ui.RGBA(255, 255, 255, 0.06))
			ui.Row(c.Key("observe")).FillWidth().Height(18).Gap(6).Children(func() {
				ui.Icon(c, sparkleIcon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(ui2.Teal)
				ui.Text(c, "清理观察").FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).TextColor(t.Muted).SingleLine()
				ui.Spacer(c)
				ui.Text(c, fmt.Sprintf("已清出 %s · 共 %d 次", kit.HumanSize(p.freed.Bytes), p.freed.Times)).
					Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
			})
		}
	})
}

// iconButton 是底部的小图标按钮；鼠标停留时显示 tip。on = 开关打开（绿底），disabled = 置灰。
func (p *Panel) iconButton(c *ui.Context, t kit.Theme, key string, icon *ui.SVG, tip string, on, disabled bool, onClick func()) {
	b := ui.ButtonBase(c.Key(key)).Label(tip).Size(buttonH, buttonH).Shrink(0).Radius(kit.RadiusM).Center().Disabled(disabled).Tooltip(tip)
	switch {
	case on:
		b.Background(ui2.Green.Alpha(0.28))
	case !disabled && b.Hovered():
		b.Background(t.Hover)
	}
	col := t.Muted
	if on {
		col = t.Text
	}
	b.Children(func() { ui.Icon(c, icon).Size(kit.IconL, kit.IconL).TextColor(col) })
	if disabled {
		b.Opacity(0.4)
	}
	b.OnClick(func() {
		if !disabled {
			onClick()
		}
	})
}

// loginButton：「开机时启动」开关；打开时绿底。系统不让注册时置灰，原因写在鼠标停留提示里。
func (p *Panel) loginButton(c *ui.Context, t kit.Theme) {
	disabled := p.o.Login.Disabled != "" || p.o.Login.Set == nil
	on := p.loginOn && !disabled
	tip := "开机时启动：未开启"
	switch {
	case disabled:
		tip = "开机时启动：" + p.o.Login.Disabled
	case on:
		tip = "开机时启动：已开启"
	}
	p.iconButton(c, t, "login", loginIcon, tip, on, disabled, func() {
		want := !p.loginOn
		if err := p.o.Login.Set(want); err != nil {
			p.loginErr = "开机时启动设置失败：" + err.Error()
			return
		}
		p.loginErr = ""
		p.loginOn = want
		if p.o.Login.Enabled != nil {
			p.loginOn = p.o.Login.Enabled()
		}
	})
}
