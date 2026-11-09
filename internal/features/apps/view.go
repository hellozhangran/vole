// SPDX-License-Identifier: GPL-3.0-only

package apps

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/apps"
	"vole/internal/kit"
)

var (
	red    = ui.Hex("#f0716a") // 勾选框、取消选择（参照 Mole 软件 tab 的红）
	orange = ui.Hex("#f0a04b") // 很久没用、Intel、个人数据、大文件
	green  = ui.Hex("#34d399") // 活跃中
	white  = ui.Hex("#ffffff")
	ink    = ui.Hex("#111111")

	refreshIcon = kit.LineIcon(`<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>`)
	appIcon     = kit.LineIcon(`<rect x="4" y="4" width="16" height="16" rx="4"/><path d="M9 9h6v6H9z"/>`)
	chevDown    = kit.LineIcon(`<path d="m6 9 6 6 6-6"/>`)
	chevUp      = kit.LineIcon(`<path d="m6 15 6-6 6 6"/>`)
	searchIcon  = kit.LineIcon(`<circle cx="11" cy="11" r="6.5"/><path d="m16 16 4.5 4.5"/>`)
	warnIcon    = kit.LineIcon(`<path d="M12 3 2.5 20h19L12 3Z"/><path d="M12 10v4M12 17v.5"/>`)
)

const (
	rowH       = 64  // app 行高（参照 Mole 截图 13）
	fileRowH   = 44  // 关联文件行高
	bottomBarH = 76  // 底部固定栏
	bigFile    = 1e9 // 个人数据超过 1 GB 就醒目标出
	iconSize   = 40  // app 图标
	searchW    = 200 // 搜索框宽度
	dialogW    = 500 // 确认框宽度
)

// View 按阶段画页面。
func (m *Module) View(c *ui.Context) {
	t := m.theme
	switch m.phase {
	case phaseRunning:
		m.runningView(c, t)
	case phaseDone:
		m.doneView(c, t)
	default:
		m.listView(c, t)
	}
}

// listView 列表页：工具栏（数量、刷新、排序、搜索）+ app 列表 + 底部栏 + 确认框。
func (m *Module) listView(c *ui.Context, t kit.Theme) {
	ui.Box(c).Fill().Children(func() {
		ui.Column(c).Fill().Children(func() {
			m.toolbar(c, t)
			switch {
			case m.phase == phaseLoading:
				m.centered(c, t, "", "正在读取已安装的应用…", "第一次要算每个 app 的大小，大约几秒")
			case m.loadErr != nil:
				m.centered(c, t, kit.ArtError, "读不到已安装的应用", m.loadErr.Error())
			default:
				m.list(c, t)
			}
			if len(m.selected()) > 0 {
				m.bottomBar(c, t)
			}
		})
		if m.confirm {
			m.confirmDialog(c, t)
		}
	})
}

// centered 是居中的等待 / 出错 / 空状态。art 为空时放奔跑田鼠。
func (m *Module) centered(c *ui.Context, t kit.Theme, art kit.ArtName, title, detail string) {
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Center().Gap(10).Padding(0, 40, 60, 40).Children(func() {
		if art == "" {
			kit.RunningVole(c, kit.Hero, kit.RunningFPS, true)
		} else {
			kit.Art(c, art, kit.ArtS).Margin(0, 0, 4, 0)
		}
		ui.Text(c, title).FontSize(kit.TextHeadline).TextAlign(ui.Center)
		if detail != "" {
			ui.Text(c, detail).FontSize(kit.TextSecondary).TextColor(t.Muted).TextAlign(ui.Center).MaxWidth(460)
		}
		if art == kit.ArtError {
			kit.PillButton(c.Key("retry"), t, "重新读取", true).Margin(10, 0, 0, 0).OnClick(m.refresh)
		}
	})
}

// toolbar：左边「已安装应用 N 个 · X GB」+ 刷新；右边排序（名称 / 大小 / 最近使用 / 安装日期）+ 搜索。
func (m *Module) toolbar(c *ui.Context, t kit.Theme) {
	ui.Row(c.Key("toolbar")).FillWidth().Shrink(0).Padding(4, kit.PageX, 10, kit.PageX).Gap(10).Children(func() {
		ui.Text(c, "已安装应用").FontSize(kit.TextBody).FontWeight(kit.WeightSemibold).SingleLine()
		if m.phase == phaseList && m.loadErr == nil {
			ui.Text(c, fmt.Sprintf("%d 个 · %s", len(m.apps), kit.HumanSize(m.total()))).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		}
		kit.IconButton(c.Key("refresh"), t, "刷新", refreshIcon, kit.ControlM).OnClick(func() {
			if m.phase != phaseLoading {
				m.refresh()
			}
		})
		ui.Spacer(c)
		for _, s := range []struct {
			k     sortKey
			label string
		}{{sortName, "名称"}, {sortSize, "大小"}, {sortUsed, "最近使用"}, {sortInstalled, "安装日期"}} {
			label := s.label
			if m.sort == s.k {
				if m.asc {
					label += " ↑"
				} else {
					label += " ↓"
				}
			} else {
				label += " ⇅"
			}
			k := s.k
			b := kit.Segment(c.Key("sort-"+s.label), t, label, m.sort == s.k).Label("按" + s.label + "排序")
			if m.sort == s.k {
				b.TextColor(red)
			}
			b.OnClick(func() { m.setSort(k) })
		}
		m.searchBox(c, t)
	})
}

// searchBox：放大镜 + 输入框（深色半透明底，跟主题一致）+ 有内容时的清除按钮。
func (m *Module) searchBox(c *ui.Context, t kit.Theme) {
	ui.Row(c.Key("search")).Width(searchW).Height(kit.ControlL).Padding(0, 10).Gap(6).Radius(kit.ControlL / 2).Background(t.Surface).Children(func() {
		ui.Icon(c, searchIcon).Size(kit.IconM, kit.IconM).TextColor(t.Muted).Shrink(0)
		ui.TextInputBase(c.Key("input"), &m.query).Label("搜索应用").Placeholder("搜索应用").Grow(1).MinWidth(0).
			FontSize(kit.TextSecondary).TextColor(t.Text)
		if m.query != "" {
			kit.IconButton(c.Key("clear"), t, "清除搜索", kit.CloseIcon, kit.ControlS).OnClick(func() { m.query = "" })
		}
	})
}

// list：app 列表；搜索无结果时给空状态。
func (m *Module) list(c *ui.Context, t kit.Theme) {
	apps := m.visible()
	if len(apps) == 0 {
		m.centered(c, t, kit.ArtAnalyzeIdle, fmt.Sprintf("没有找到「%s」", strings.TrimSpace(m.query)), "换个名字或 bundle id 试试")
		return
	}
	ui.Scroll(c.Key("apps")).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Gap(2).Padding(0, kit.PageX-12, 16, kit.PageX-12).Children(func() {
			for _, a := range apps {
				m.appRow(c, t, a)
			}
		})
	})
}

// appRow：图标 + 名称 +「版本 · 大小 · 状态」+（选中时）「N 个文件 · X ⌄」+ 勾选框；展开时逐项列出关联文件。
func (m *Module) appRow(c *ui.Context, t kit.Theme, a core.App) {
	ok := selectable(a)
	on := m.sel[a.Path]
	col := ui.Column(c.Key(a.Path)).FillWidth().Radius(kit.RadiusL)
	if on {
		col.Background(t.Selected)
	}
	col.Children(func() {
		row := ui.Row(c.Key("row")).FillWidth().Height(rowH).Padding(0, 14).Gap(14).Radius(kit.RadiusL)
		if !on && row.Hovered() {
			row.Background(t.Hover)
		}
		if a.Blocked != "" {
			row.Tooltip(a.Blocked)
		}
		row.Children(func() {
			m.appIcon(c, t, a, iconSize)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				name := ui.Text(c, a.Name).FontSize(kit.TextTitle).FontWeight(kit.WeightSemibold).SingleLine().Ellipsis("…").FillWidth()
				if !ok {
					name.TextColor(t.Faint)
				}
				m.metaLine(c, t, a)
			})
			if on {
				m.filesSummary(c, t, a)
			}
			state := kit.Unchecked
			if on {
				state = kit.Checked
			}
			app := a
			kit.Check(c.Key("check"), t, "勾选 "+a.Name, state, red, !ok).OnClick(func() { m.toggle(app) })
		})
		if on && m.expanded[a.Path] {
			m.filesList(c, t, a)
		}
	})
}

// appIcon：app 自己的图标；还没取到时用通用图标。
func (m *Module) appIcon(c *ui.Context, t kit.Theme, a core.App, size float32) {
	b := m.icon(a.Path)
	if b == nil {
		ui.Box(c).Size(size, size).Shrink(0).Center().Radius(kit.RadiusM).Background(t.Surface).Children(func() {
			ui.Icon(c, appIcon).Size(size*0.55, size*0.55).TextColor(t.Faint)
		})
		return
	}
	el := ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) { p.Image(b, r, ui.Contain) })
	if !selectable(a) {
		el.Opacity(0.5)
	}
}

// metaLine：版本 · 大小 · 状态 · 架构 · 来源 · 权限；不能卸的写原因。
func (m *Module) metaLine(c *ui.Context, t kit.Theme, a core.App) {
	type part struct {
		text  string
		color ui.Color
	}
	var parts []part
	if a.Version != "" {
		parts = append(parts, part{a.Version, t.Faint})
	}
	switch {
	case a.Source == core.SourceSteam:
		parts = append(parts, part{"Steam 管理", t.Faint})
	case a.SizeKnown:
		parts = append(parts, part{kit.HumanSize(a.Size), t.Muted})
	}
	now := m.now()
	switch {
	case a.Running && a.MenuBar:
		parts = append(parts, part{"菜单栏常驻", t.Muted})
	case a.Running:
		parts = append(parts, part{"活跃中", green})
	case a.Idle(now):
		parts = append(parts, part{ago(now.Sub(a.LastUsed)) + " 活跃", orange})
	}
	if a.Intel {
		parts = append(parts, part{"Intel", orange})
	}
	switch a.Source {
	case core.SourceAppStore:
		parts = append(parts, part{"App Store", t.Faint})
	case core.SourceHomebrew:
		parts = append(parts, part{"Homebrew", t.Faint})
	}
	if a.NeedsAdmin && a.Blocked == "" {
		parts = append(parts, part{"需要管理员密码", t.Faint})
	}
	if a.Blocked != "" {
		parts = append(parts, part{a.Blocked, t.Muted})
	}
	ui.Row(c).Gap(6).MinWidth(0).Children(func() {
		for i, p := range parts {
			if i > 0 {
				ui.Text(c, "·").FontSize(kit.TextSecondary).TextColor(t.Faint)
			}
			ui.Text(c, p.text).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(p.color).SingleLine()
		}
	})
}

// ago：「6个月前」「1年前」「12天前」。
func ago(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 365:
		return fmt.Sprintf("%d年前", days/365)
	case days >= 30:
		return fmt.Sprintf("%d个月前", days/30)
	}
	return fmt.Sprintf("%d天前", days)
}

// filesSummary：选中行右侧「N 个文件 · X ⌄」，点开 / 收起关联文件。
func (m *Module) filesSummary(c *ui.Context, t kit.Theme, a core.App) {
	fs := m.files[a.Path]
	label := "正在查找关联文件…"
	if fs != nil && !fs.loading {
		picked := m.pickedFiles(a)
		label = fmt.Sprintf("%d 个文件 · %s", len(fs.files), kit.HumanSize(a.Size+sizeOf(picked)))
	}
	open := m.expanded[a.Path]
	b := ui.ButtonBase(c.Key("files")).Label("展开 "+a.Name+" 的关联文件").Height(kit.ControlL).Padding(0, 10).Gap(6).Radius(kit.ControlL / 2).TextColor(t.Muted).Shrink(0)
	if b.Hovered() {
		b.Background(t.Hover).TextColor(t.Text)
	}
	b.Children(func() {
		ui.Text(c, label).Font(kit.Mono).FontSize(kit.TextSecondary).SingleLine()
		icon := chevDown
		if open {
			icon = chevUp
		}
		ui.Icon(c, icon).Size(kit.IconM, kit.IconM)
	})
	path := a.Path
	b.OnClick(func() { m.expanded[path] = !open })
}

// filesList：关联文件逐项。缓存和日志默认勾；个人数据默认不勾、标「个人数据」；系统级只列出、不能勾；
// 超过 1 GB 的个人数据醒目标出，避免误勾。
func (m *Module) filesList(c *ui.Context, t kit.Theme, a core.App) {
	fs := m.files[a.Path]
	ui.Column(c.Key("files-list")).FillWidth().Padding(0, 14, 10, 14+iconSize+14).Gap(2).Children(func() {
		switch {
		case fs == nil || fs.loading:
			ui.Text(c, "正在查找关联文件…").FontSize(kit.TextSecondary).TextColor(t.Muted).Padding(8, 0)
			return
		case len(fs.files) == 0:
			ui.Text(c, "没有找到关联文件，只会移走 app 本身。").FontSize(kit.TextSecondary).TextColor(t.Muted).Padding(8, 0)
			return
		}
		ui.Text(c, "缓存和日志默认勾选；个人数据（设置、聊天记录、下载的内容）默认不勾，删了重装 app 后找不回。").
			FontSize(kit.TextCaption).TextColor(t.Faint).Padding(4, 0, 6, 0)
		for _, f := range fs.files {
			m.fileRow(c, t, a, fs, f)
		}
	})
}

func (m *Module) fileRow(c *ui.Context, t kit.Theme, a core.App, fs *filesState, f core.File) {
	system := f.Kind == core.KindSystem
	big := f.Kind == core.KindData && f.Size >= bigFile
	row := ui.Row(c.Key(f.Path)).FillWidth().Height(fileRowH).Padding(0, 10).Gap(10).Radius(kit.RadiusM)
	if big {
		row.Background(orange.Alpha(0.08))
	}
	row.Children(func() {
		state := kit.Unchecked
		if fs.sel[f.Path] && !system {
			state = kit.Checked
		}
		p := f.Path
		kit.Check(c.Key("check"), t, "勾选 "+m.pretty(p), state, red, system).OnClick(func() { fs.sel[p] = !fs.sel[p] })
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			name := ui.Text(c, m.pretty(p)).Font(kit.Mono).FontSize(kit.TextSecondary).SingleLine().Ellipsis("…").FillWidth()
			if system {
				name.TextColor(t.Faint)
			}
			if f.Extra != "" {
				ui.Text(c, "Vole 补充："+f.Extra).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine().Ellipsis("…").FillWidth()
			}
		})
		switch {
		case system:
			tag(c, "系统级 · 需要管理员权限，不处理", t.Faint)
		case big:
			tag(c, "个人数据 · 很大，确认后再勾", orange)
		case f.Kind == core.KindData:
			tag(c, "个人数据", orange)
		default:
			tag(c, "缓存和日志", t.Muted)
		}
		size := ""
		if !system {
			size = kit.HumanSize(f.Size)
		}
		s := ui.Text(c, size).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(76).Shrink(0).TextAlign(ui.End).SingleLine()
		if big {
			s.TextColor(orange).FontWeight(kit.WeightSemibold)
		}
		kit.IconButton(c.Key("reveal"), t, "在 Finder 中显示", kit.FolderIcon, kit.ControlM).Background(t.Surface).OnClick(func() { m.reveal(p) })
	})
}

func tag(c *ui.Context, text string, color ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(kit.TextCaption).FontWeight(kit.WeightSemibold).Padding(2, 8).Radius(kit.RadiusS).
		Background(color.Alpha(0.16)).TextColor(color).Shrink(0).SingleLine()
}

// pretty 把家目录写成 ~。
func (m *Module) pretty(p string) string {
	if m.home != "" {
		if rest, ok := strings.CutPrefix(p, m.home+"/"); ok {
			return "~/" + rest
		}
	}
	return p
}

// bottomBar：选中 app 的图标 +「名称 · N 个 App · 大小」+「取消选择」；右边白色大按钮「移除 N 项」。
func (m *Module) bottomBar(c *ui.Context, t kit.Theme) {
	sel := m.selected()
	ui.Row(c.Key("bottom")).FillWidth().Height(bottomBarH).Shrink(0).Padding(0, kit.PageX).Gap(12).
		Background(t.BgBottom.Alpha(0.92)).Border(1, ui.RGBA(255, 255, 255, 0.06)).BorderWidth(1, 0, 0, 0).Children(func() {
		m.appIcon(c, t, sel[0], kit.ControlL)
		names := sel[0].Name
		if len(sel) > 1 {
			names = fmt.Sprintf("%s 等", sel[0].Name)
		}
		ui.Text(c, fmt.Sprintf("%s · %d 个 App · %s", names, len(sel), kit.HumanSize(m.selectedSize()))).FontSize(kit.TextBody).SingleLine().Ellipsis("…").MinWidth(0)
		cancel := ui.ButtonBase(c.Key("clear")).Label("取消选择").TextColor(red).FontSize(kit.TextBody).Shrink(0)
		if cancel.Hovered() {
			cancel.TextColor(white)
		}
		cancel.Children(func() { ui.Text(c, "取消选择").SingleLine() })
		cancel.OnClick(m.clearSelection)
		ui.Spacer(c)
		busy := m.filesLoading()
		label := fmt.Sprintf("移除 %d 项", len(sel))
		b := ui.ButtonBase(c.Key("remove")).Label("移除").Height(kit.ButtonL).Padding(0, 40).Radius(kit.ButtonL / 2).
			Background(white).TextColor(ink).Disabled(busy).Shrink(0)
		if busy {
			b.Opacity(0.4)
		}
		b.Children(func() { ui.Text(c, label).FontSize(kit.TextTitle).FontWeight(kit.WeightSemibold).SingleLine() })
		b.OnClick(func() { m.confirm = true })
	})
}

// confirmDialog：列清楚要移到废纸篓的内容；有运行中的 app 时按钮叫「退出并移除」并说明怎么退出；要管理员密码的说明会弹密码框。
func (m *Module) confirmDialog(c *ui.Context, t kit.Theme) {
	sel := m.selected()
	var running, admin []string
	var data int
	for _, a := range sel {
		if a.Running {
			running = append(running, a.Name)
		}
		if a.NeedsAdmin {
			admin = append(admin, a.Name)
		}
		for _, f := range m.pickedFiles(a) {
			if f.Kind == core.KindData {
				data++
			}
		}
	}
	ui.Box(c.Key("confirm")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Children(func() {
		ui.ButtonBase(c.Key("scrim")).Label("取消移除").Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(0, 0, 0, 0.55)).OnClick(func() { m.confirm = false })
		ui.Column(c.Key("dialog")).Attach(ui.AnchorCenter, ui.AnchorCenter).Width(dialogW).Padding(26, 28).Gap(14).Radius(18).Background(ui.Hex("#2a1c20")).
			Border(1, ui.RGBA(255, 255, 255, 0.08)).Shadow(0, 20, 60, 0, ui.RGBA(0, 0, 0, 0.5)).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				if len(running) > 0 {
					ui.Icon(c, warnIcon).Size(kit.IconL+4, kit.IconL+4).TextColor(red)
				}
				title := fmt.Sprintf("移除 %d 个 App？", len(sel))
				if len(running) == 1 && len(sel) == 1 {
					title = running[0] + " 正在运行"
				}
				ui.Text(c, title).FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
			})
			ui.Text(c, "将移到废纸篓约 "+kit.HumanSize(m.selectedSize())).Font(kit.Mono).FontSize(kit.TextTitle).TextColor(red).SingleLine()
			line := func(text string, color ui.Color) {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, "•").FontSize(kit.TextBody).TextColor(color).Shrink(0)
					ui.Text(c, text).FontSize(kit.TextBody).TextColor(color).Grow(1).MinWidth(0)
				})
			}
			ui.Column(c).FillWidth().Gap(8).Margin(4, 0, 6, 0).Children(func() {
				for i, a := range sel {
					if i == 4 {
						line(fmt.Sprintf("还有 %d 个 App…", len(sel)-4), t.Muted)
						break
					}
					files := m.pickedFiles(a)
					text := a.Name + "：app 本体"
					if len(files) > 0 {
						text += fmt.Sprintf(" + %d 个关联文件", len(files))
					}
					line(text+"（"+kit.HumanSize(a.Size+sizeOf(files))+"）", t.Text)
				}
				if data > 0 {
					line(fmt.Sprintf("包括 %d 项个人数据：重装 app 后找不回，确认不再需要再移除。", data), orange)
				}
				if len(running) > 0 {
					line(strings.Join(running, "、")+" 正在运行：会先正常退出，5 秒内没退出就强制结束，还退不掉就跳过不删。未保存的更改可能会丢失。", red)
				}
				if len(admin) > 0 {
					line(strings.Join(admin, "、")+" 需要管理员权限：系统会弹出密码框（第一次还会询问是否允许 Vole 控制「访达」）。", t.Muted)
				}
				line("全部移到废纸篓，可以从废纸篓找回；空间要清空废纸篓后才释放。", t.Muted)
			})
			ui.Row(c).FillWidth().Gap(10).Justify(ui.End).Children(func() {
				kit.PillButton(c.Key("no"), t, "取消", false).OnClick(func() { m.confirm = false })
				label := "移除"
				if len(running) > 0 {
					label = "退出并移除"
				}
				yes := ui.ButtonBase(c.Key("yes")).Label("确认"+label).Height(kit.ControlL).Padding(0, 16).Radius(kit.ControlL/2).
					FontSize(kit.TextBody).Background(red.Alpha(0.16)).Border(1, red.Alpha(0.6)).TextColor(red)
				if yes.Hovered() {
					yes.Background(red.Alpha(0.26))
				}
				yes.Children(func() { ui.Text(c, label).SingleLine() })
				yes.OnClick(m.startRun)
			})
		})
	})
}

// runningView 卸载中：奔跑田鼠 + 进度 + 当前 app + 取消（当前这个 app 做完就停）。
func (m *Module) runningView(c *ui.Context, t kit.Theme) {
	total := len(m.plan)
	ui.Column(c).Fill().Center().Gap(14).Padding(kit.PageTop, kit.PageX, 50, kit.PageX).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(kit.PageTop).DragWindow()
		kit.RunningVole(c, 1.6*kit.Hero, kit.RunningFPS, true)
		ui.Text(c, "正在移除").FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
		frac := float32(0)
		if total > 0 {
			frac = float32(m.runDone) / float32(total)
		}
		ui.Box(c).Width(360).Height(6).Radius(3).Background(t.Surface).Margin(6, 0, 0, 0).Children(func() {
			ui.Box(c).WidthPercent(max(frac, 0.02) * 100).FillHeight().Radius(3).Background(red)
		})
		ui.Text(c, fmt.Sprintf("第 %d / %d 个", min(m.runDone+1, total), total)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		if m.runDone < total {
			a := m.plan[m.runDone].App
			ui.Row(c).Gap(8).Children(func() {
				m.appIcon(c, t, a, kit.ControlM)
				ui.Text(c, a.Name).FontSize(kit.TextBody).SingleLine()
			})
			if a.Running {
				ui.Text(c, "正在让它退出…").FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine()
			} else if a.NeedsAdmin {
				ui.Text(c, "请在系统弹出的密码框里输入管理员密码").FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine()
			}
		}
		label := "取消"
		if m.cancelling {
			label = "正在完成当前这个 App…"
		}
		kit.PillButton(c.Key("cancel-run"), t, label, false).Margin(10, 0, 0, 0).Disabled(m.cancelling).OnClick(m.cancelRun)
	})
}

// doneView 完成页：移到废纸篓多少；跳过、失败的 app 和原因、没移走的关联文件；打开废纸篓；完成。
func (m *Module) doneView(c *ui.Context, t kit.Theme) {
	rep := m.report
	var done, moved int
	var size int64
	var problems []core.Outcome
	for _, o := range rep.Outcomes {
		if o.Status == core.StatusDone {
			done++
			moved += o.Moved
			size += o.Size
		}
		if o.Status != core.StatusDone || len(o.Failures) > 0 {
			problems = append(problems, o)
		}
	}
	ui.Column(c).Fill().Center().Gap(12).Padding(kit.PageTop, kit.PageX, 40, kit.PageX).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(kit.PageTop).DragWindow()
		kit.Art(c, kit.ArtCleanDone, kit.ArtM)
		ui.Text(c, "已移到废纸篓 "+kit.HumanSize(size)).Font(kit.Mono).FontSize(kit.TextDisplay).FontWeight(kit.WeightSemibold).SingleLine()
		sub := fmt.Sprintf("移除了 %d 个 App，共 %d 项；空间要清空废纸篓后才释放", done, moved)
		if rep.Canceled {
			canceled := 0
			for _, o := range rep.Outcomes {
				if o.Status == core.StatusCanceled {
					canceled++
				}
			}
			sub = fmt.Sprintf("移除了 %d 个 App；已取消，剩下 %d 个没有处理", done, canceled)
		}
		ui.Text(c, sub).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
		if len(problems) > 0 {
			ui.Column(c.Key("problems")).Width(640).MaxHeight(260).Margin(10, 0, 0, 0).Radius(kit.RadiusL).Background(t.Surface).Clip().Children(func() {
				ui.Text(c, fmt.Sprintf("%d 个 App 没有完全移除", len(problems))).FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).
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
			kit.PillButton(c.Key("trash"), t, "打开废纸篓", false).OnClick(func() { m.openPath(m.be.TrashDir()) })
			kit.PillButton(c.Key("log"), t, "查看日志", false).OnClick(func() { m.openPath(m.be.LogDir()) })
			kit.PillButton(c.Key("ok"), t, "完成", true).OnClick(m.finish)
		})
	})
}

func (m *Module) problemRow(c *ui.Context, t kit.Theme, o core.Outcome) {
	color := t.Muted
	if o.Status == core.StatusFailed {
		color = red
	}
	ui.Row(c.Key(o.App.Path)).FillWidth().Gap(10).Padding(7, 8).AlignItems(ui.Start).Children(func() {
		tag(c, string(o.Status), color)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, o.App.Name).FontSize(kit.TextBody).SingleLine().Ellipsis("…").FillWidth()
			if o.Reason != "" {
				ui.Text(c, o.Reason).FontSize(kit.TextCaption).TextColor(t.Faint).MaxLines(2).FillWidth()
			}
			for _, f := range o.Failures {
				ui.Text(c, f).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine().Ellipsis("…").FillWidth()
			}
		})
	})
}
