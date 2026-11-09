// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/clean"
	"vole/internal/kit"
)

// 类别的图标和一句话说明。
var (
	catIcons = map[string]*ui.SVG{
		core.CatDev:     kit.LineIcon(`<path d="m14.5 6.5 3 3M4 20l8.5-8.5M14 4l6 6-3 3-6-6 3-3Z"/>`),
		core.CatXcode:   kit.LineIcon(`<rect x="7" y="2.5" width="10" height="19" rx="2.5"/><path d="M11 18.5h2"/>`),
		core.CatCaches:  kit.LineIcon(`<rect x="3.5" y="5" width="13" height="15" rx="2.5"/><circle cx="18" cy="5.5" r="2.5"/>`),
		core.CatBrowser: kit.LineIcon(`<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>`),
		core.CatLogs:    kit.LineIcon(`<path d="M6 3h8l4 4v14H6V3Z"/><path d="M14 3v4h4M9 12h6M9 16h6"/>`),
		core.CatTrash:   kit.LineIcon(`<path d="M4 6h16M9 6V3.5h6V6M6 6l1 15h10l1-15"/>`),
		core.CatSystem:  kit.LineIcon(`<circle cx="12" cy="12" r="3"/><path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M5.3 18.7l2.1-2.1M16.6 7.4l2.1-2.1"/>`),
		core.CatAI:      kit.LineIcon(`<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3Z"/><path d="M19 16l.7 1.8 1.8.7-1.8.7L19 21l-.7-1.8-1.8-.7 1.8-.7L19 16Z"/>`),
		core.CatComms:   kit.LineIcon(`<path d="M4 5.5h16v10H10l-4.5 3.5v-3.5H4v-10Z"/>`),
	}
	catNotes = map[string]string{
		core.CatDev:     "npm / uv / Go / CocoaPods 等开发缓存，下次构建时会重新下载",
		core.CatXcode:   "编译产物和模拟器缓存，首次构建会稍慢",
		core.CatCaches:  "应用的临时文件，下次启动时会重新生成",
		core.CatBrowser: "网页缓存，不影响书签、密码和登录状态",
		core.CatLogs:    "日志和崩溃报告，删了不影响使用",
		core.CatTrash:   "清空废纸篓，里面的文件将无法恢复",
		core.CatSystem:  "macOS 系统缓存，会自动重建",
		core.CatAI:      "AI 应用的临时缓存，对话、项目和本地模型会保留",
		core.CatComms:   "聊天 App 缓存，旧图片视频清理后需重新下载，过期内容无法找回",
	}
	closeIcon  = kit.CloseIcon
	folderIcon = kit.FolderIcon
	chevDown   = kit.LineIcon(`<path d="m6 9 6 6 6-6"/>`)
	chevUp     = kit.LineIcon(`<path d="m6 15 6-6 6 6"/>`)
	chevRight  = kit.LineIcon(`<path d="m9 6 6 6-6 6"/>`)
)

const bottomBarH = 76

// resultsView 结果清单（参照 Mole 08）：整页铺满，页头汇总被跳过的项，类别卡片可展开逐项勾选，底部固定操作栏。
func (m *Module) resultsView(c *ui.Context, t kit.Theme) {
	ui.Box(c).Fill().Children(func() {
		ui.Column(c).Fill().Children(func() {
			m.resultsHeader(c, t)
			ui.Scroll(c.Key("cards")).Grow(1).FillWidth().MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Gap(10).Padding(4, kit.PageX, 16, kit.PageX).Children(func() {
					shown := 0
					for _, cat := range m.res.Categories {
						if len(cat.Items) == 0 {
							continue
						}
						shown++
						m.categoryCard(c, t, cat)
					}
					if shown == 0 {
						ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(10).Padding(40, 0).Children(func() {
							kit.Art(c, kit.ArtCleanDone, kit.ArtS)
							ui.Text(c, "没有发现可以清理的内容").FontSize(kit.TextHeadline)
						})
					}
				})
			})
			m.bottomBar(c, t)
		})
		if m.confirm {
			m.confirmDialog(c, t)
		}
	})
}

func (m *Module) resultsHeader(c *ui.Context, t kit.Theme) {
	ui.Box(c.Key("header")).FillWidth().Shrink(0).Children(func() {
		// 拖动区只铺页头最上面一条（标题上方），下面的「去设置」才能接到点击；✕ 用绝对定位叠在上面
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(kit.PageTop).DragWindow()
		ui.Row(c).FillWidth().Padding(kit.PageTop, kit.PageX+44, 18, kit.PageX).Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, "准备开始清理").FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
				if s, fda := m.skippedSummary(); s != "" {
					ui.Text(c, s).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).MaxLines(2).FillWidth()
					if fda {
						m.settingsLink(c, "fda-head", "去设置开启完全磁盘访问")
					}
				}
			})
		})
		kit.IconButton(c.Key("close"), t, "关闭，回到首屏", closeIcon, 28).Background(t.Surface).
			Absolute().Top(kit.PageTop).Right(kit.PageX).OnClick(m.closeResults)
	})
}

// skippedSummary：「关闭 Google Chrome、Otty 后可再清理 1.9 GB · 9 项；3 项需要完全磁盘访问；另有 1 项暂时不能清理」。
// 受保护 / 在白名单里的项只在列表里置灰，不进页头（它们不会因为用户操作变得可清理）。
func (m *Module) skippedSummary() (text string, needFDA bool) {
	var apps []string
	var runSize int64
	runN, fdaN, otherN := 0, 0, 0
	for _, it := range m.items() {
		switch {
		case it.Blocked == "" || protectedReason(it.Blocked) || strings.HasPrefix(it.Blocked, core.SystemBusy):
			// 受保护的、系统进程占用的：用户关不掉，只在列表里置灰
		case it.Blocked == fdaReason:
			fdaN++
		default:
			name, ok := strings.CutSuffix(it.Blocked, " 正在运行")
			if !ok {
				name, ok = holderName(it.Blocked)
			}
			if ok {
				runN++
				runSize += it.Size
				if !slices.ContainsFunc(apps, func(a string) bool { return strings.EqualFold(a, name) }) { // Claude / claude 只写一次
					apps = append(apps, name)
				}
			} else {
				otherN++
			}
		}
	}
	var parts []string
	if runN > 0 {
		sort.Strings(apps)
		parts = append(parts, fmt.Sprintf("关闭 %s 后可再清理 %s · %d 项", strings.Join(apps, "、"), kit.HumanSize(runSize), runN))
	}
	if fdaN > 0 {
		parts = append(parts, fmt.Sprintf("%d 项需要完全磁盘访问", fdaN))
	}
	if otherN > 0 {
		parts = append(parts, fmt.Sprintf("另有 %d 项暂时不能清理（展开查看原因）", otherN))
	}
	return strings.Join(parts, "；"), fdaN > 0
}

// holderName：「被 uv run 占用：my-mcp-server（PID 1、2）；PID 3」→「uv run（my-mcp-server）」，放进页头的「关闭…」。
func holderName(reason string) (string, bool) {
	rest, ok := strings.CutPrefix(reason, "被 ")
	if !ok {
		return "", false
	}
	kind, list, ok := strings.Cut(rest, " 占用：")
	if !ok {
		return "", false
	}
	var names []string
	for _, g := range strings.Split(list, "；") {
		if n, _, ok := strings.Cut(g, "（PID"); ok {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return kind, true
	}
	return kind + "（" + strings.Join(names, "、") + "）", true
}

// fdaReason 是缺完全磁盘访问时的原因（core/clean 写的）。
const fdaReason = "需要完全磁盘访问"

func protectedReason(r string) bool {
	return strings.HasPrefix(r, "受保护") || strings.HasPrefix(r, "在白名单里")
}

// categoryCard 一个类别：三态勾选框 + 图标 + 标题「已选 n/m」+ 说明 + 「已选大小 / 总大小」+ 展开。
func (m *Module) categoryCard(c *ui.Context, t kit.Theme, cat core.CategoryResult) {
	open := m.expanded[cat.ID]
	// 「已选 n/m」和总大小都含不能勾选的项（它们置灰列在里面）
	n, picked, selectableN := len(cat.Items), 0, 0
	var pickedSize int64
	for _, it := range cat.Items {
		if selectable(it) {
			selectableN++
			if m.sel[it.ID] {
				picked++
				pickedSize += it.Size
			}
		}
	}
	ui.Column(c.Key("cat-"+cat.ID)).FillWidth().Radius(kit.RadiusL).Background(t.Surface).Border(1, ui.RGBA(255, 255, 255, 0.06)).Clip().Children(func() {
		head := ui.ButtonBase(c.Key("head")).Label("展开 "+cat.Title).FillWidth().Height(68).Padding(0, 18).Gap(14).Justify(ui.Start)
		if head.Hovered() {
			head.Background(t.Hover)
		}
		head.OnClick(func() { m.expanded[cat.ID] = !open })
		head.Children(func() {
			kit.Check(c.Key("check"), t, "勾选 "+cat.Title, m.categoryState(cat), blue, selectableN == 0).OnClick(func() { m.toggleCategory(cat) })
			ui.Icon(c, catIcons[cat.ID]).Size(20, 20).Shrink(0).TextColor(t.Muted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, cat.Title).FontSize(kit.TextTitle).FontWeight(kit.WeightSemibold).SingleLine()
					ui.Text(c, fmt.Sprintf("已选 %d/%d", picked, n)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Faint).SingleLine()
				})
				note := catNotes[cat.ID]
				if cat.Note != "" {
					note += "（" + cat.Note + "）"
				}
				ui.Text(c, note).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine().Ellipsis("…").FillWidth()
			})
			size := kit.HumanSize(pickedSize) + " / " + kit.HumanSize(cat.Size)
			if pickedSize == cat.Size {
				size = kit.HumanSize(cat.Size)
			}
			ui.Text(c, size).Font(kit.Mono).FontSize(kit.TextBody).FontWeight(kit.WeightSemibold).TextColor(blue).Shrink(0).SingleLine()
			icon := chevDown
			if open {
				icon = chevUp
			}
			ui.Icon(c, icon).Size(kit.IconL, kit.IconL).Shrink(0).TextColor(t.Muted)
		})
		if open {
			ui.Column(c.Key("rows")).FillWidth().Padding(0, 10, 10, 10).Gap(2).Children(func() {
				for _, g := range groupItems(cat.Items) {
					switch {
					case g.name == "":
						m.itemRow(c, t, g.items[0], g.items[0].Title)
					case len(g.items) == 1:
						m.itemRow(c, t, g.items[0], g.name+" · "+g.items[0].Title)
					default:
						m.groupRows(c, t, cat.ID, g)
					}
				}
			})
		}
	})
}

// itemGroup 是类别里的一组（同一个 app 的若干目录）；name 为空时只有一项、单独一行。
type itemGroup struct {
	name  string
	items []core.Item
	size  int64
}

// groupItems 按 Item.Group 分组，组和单项一起按大小降序排（组内保持原来的大小顺序）。
func groupItems(items []core.Item) []itemGroup {
	var out []itemGroup
	at := map[string]int{}
	for _, it := range items {
		if it.Group == "" {
			out = append(out, itemGroup{items: []core.Item{it}, size: it.Size})
			continue
		}
		i, ok := at[it.Group]
		if !ok {
			i = len(out)
			at[it.Group] = i
			out = append(out, itemGroup{name: it.Group})
		}
		out[i].items = append(out[i].items, it)
		out[i].size += it.Size
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].size > out[j].size })
	return out
}

// groupRows 一组：三态勾选框 + 展开箭头 + app 名 +「N 项」+ 大小 + 在 Finder 中显示（共同的上级目录）；展开后逐项列出。
func (m *Module) groupRows(c *ui.Context, t kit.Theme, catID string, g itemGroup) {
	key := "g:" + catID + ":" + g.name
	open := m.expanded[key]
	ui.Column(c.Key(key)).FillWidth().Gap(2).Children(func() {
		head := ui.ButtonBase(c.Key("head")).Label("展开 "+g.name).FillWidth().Height(52).Padding(0, 12).Gap(12).Radius(kit.RadiusM).Justify(ui.Start)
		if head.Hovered() {
			head.Background(t.Hover)
		}
		head.OnClick(func() { m.expanded[key] = !open })
		head.Children(func() {
			n := 0
			for _, it := range g.items {
				if selectable(it) {
					n++
				}
			}
			kit.Check(c.Key("check"), t, "勾选 "+g.name, m.itemsState(g.items), blue, n == 0).OnClick(func() { m.toggleItems(g.items) })
			icon := chevRight
			if open {
				icon = chevDown
			}
			ui.Icon(c, icon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(t.Muted)
			ui.Text(c, g.name).FontSize(kit.TextBody).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
			ui.Text(c, fmt.Sprintf("%d 项", len(g.items))).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Faint).Shrink(0).SingleLine()
			ui.Text(c, kit.HumanSize(g.size)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(76).Shrink(0).TextAlign(ui.End).SingleLine()
			dir := commonDir(g.items)
			kit.IconButton(c.Key("reveal"), t, "在 Finder 中显示", folderIcon, 26).Background(t.Surface).OnClick(func() { m.reveal(dir) })
		})
		if open {
			ui.Column(c.Key("items")).FillWidth().Padding(0, 0, 0, 28).Gap(2).Children(func() {
				for _, it := range g.items {
					m.itemRow(c, t, it, it.Title)
				}
			})
		}
	})
}

// commonDir 是一组项的共同上级目录。
func commonDir(items []core.Item) string {
	dir := filepath.Dir(items[0].Path)
	for _, it := range items[1:] {
		for dir != "/" && !strings.HasPrefix(it.Path, dir+"/") {
			dir = filepath.Dir(dir)
		}
	}
	return dir
}

// itemRow 一项：勾选框 + 名称 + 灰色完整路径 +「需确认」/ 不能清理的原因 + 大小 + 在 Finder 中显示。
func (m *Module) itemRow(c *ui.Context, t kit.Theme, it core.Item, title string) {
	ok := selectable(it)
	row := ui.Row(c.Key(it.ID)).FillWidth().Height(52).Padding(0, 12).Gap(12).Radius(kit.RadiusM)
	if ok && m.sel[it.ID] {
		row.Background(ui.RGBA(255, 255, 255, 0.04))
	}
	if it.Note != "" {
		row.Tooltip(it.Note)
	}
	_, pretty, _ := strings.Cut(it.ID, ":")
	row.Children(func() {
		state := kit.Unchecked
		if ok && m.sel[it.ID] {
			state = kit.Checked
		}
		id := it.ID
		kit.Check(c.Key("check"), t, "勾选 "+title, state, blue, !ok).OnClick(func() { m.sel[id] = !m.sel[id] })
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			name := ui.Text(c, title).FontSize(kit.TextBody).SingleLine().Ellipsis("…").FillWidth()
			if !ok {
				name.TextColor(t.Faint)
			}
			ui.Text(c, pretty).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine().Ellipsis("…").FillWidth()
		})
		switch {
		case !ok:
			label := it.Blocked
			if protectedReason(label) || strings.HasPrefix(label, core.SystemBusy) {
				label, _, _ = strings.Cut(label, "：") // 「受保护：……」只显示冒号前面，完整原因在提示里
			}
			// 其他原因（如「被 uv run 占用：my-mcp-server（PID …）」）整句显示，用户才知道该关哪个
			tag(c, label, t.Muted).Tooltip(it.Blocked).MaxWidth(420).Ellipsis("…")
			if it.Blocked == fdaReason {
				m.settingsLink(c, "fda", "去设置")
			}
		case it.Uncertain || !it.Default:
			tag(c, "需确认", orange).Tooltip(it.Note)
		case len(it.Command) > 0:
			tag(c, "由 "+toolName(it.Command)+" 清理", t.Muted).Tooltip("调用工具自带的清理命令，实际释放可能少于显示")
		}
		size := kit.HumanSize(it.Size)
		if it.SizeUnknown {
			size = "—"
		}
		ui.Text(c, size).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Width(76).Shrink(0).TextAlign(ui.End).SingleLine()
		path := it.Path
		kit.IconButton(c.Key("reveal"), t, "在 Finder 中显示", folderIcon, 26).Background(t.Surface).OnClick(func() { m.reveal(path) })
	})
}

// settingsLink 是打开「完全磁盘访问」设置的文字链接。
func (m *Module) settingsLink(c *ui.Context, key string, label string) {
	b := ui.ButtonBase(c.Key(key)).Label(label).TextColor(blue).FontSize(kit.TextSecondary).Shrink(0).AlignSelf(ui.Start)
	if b.Hovered() {
		b.TextColor(white)
	}
	b.Children(func() { ui.Text(c, label).SingleLine() })
	b.OnClick(m.openSettings)
}

func tag(c *ui.Context, text string, color ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(kit.TextCaption).FontWeight(kit.WeightSemibold).Padding(2, 8).Radius(kit.RadiusS).
		Background(color.Alpha(0.16)).TextColor(color).Shrink(0).SingleLine()
}

func toolName(cmd []string) string {
	// /usr/bin/env K=V 工具 参数…：跳过 env 和环境变量
	for len(cmd) > 0 && (strings.HasSuffix(cmd[0], "/env") || strings.Contains(cmd[0], "=")) {
		cmd = cmd[1:]
	}
	if len(cmd) == 0 {
		return ""
	}
	name := cmd[0][strings.LastIndex(cmd[0], "/")+1:]
	if name == "xcrun" {
		return "simctl"
	}
	return name
}

// bottomBar 底部固定栏：已选 N 项 · 全选 / 清空 / 推荐 + 右侧白色胶囊「永久清理 · X GB」。
func (m *Module) bottomBar(c *ui.Context, t kit.Theme) {
	sel := m.selected()
	ui.Row(c.Key("bottom")).FillWidth().Height(bottomBarH).Shrink(0).Padding(0, kit.PageX).Gap(14).
		Background(t.BgBottom.Alpha(0.92)).Border(1, ui.RGBA(255, 255, 255, 0.06)).BorderWidth(1, 0, 0, 0).Children(func() {
		ui.Text(c, fmt.Sprintf("已选 %d 项", len(sel))).FontSize(kit.TextBody).FontWeight(kit.WeightSemibold).SingleLine()
		link := func(label string, fn func()) {
			b := ui.ButtonBase(c.Key("link-" + label)).Label(label).TextColor(blue).FontSize(kit.TextBody)
			if b.Hovered() {
				b.TextColor(white)
			}
			b.Children(func() { ui.Text(c, label).SingleLine() })
			b.OnClick(fn)
		}
		ui.Row(c).Gap(6).Children(func() {
			link("全选", m.selectAll)
			ui.Text(c, "·").TextColor(t.Faint)
			link("清空", m.clearAll)
			ui.Text(c, "·").TextColor(t.Faint)
			link("推荐", m.recommend)
		})
		ui.Spacer(c)
		label := "永久清理 · " + kit.HumanSize(sizeOf(sel))
		b := ui.ButtonBase(c.Key("run")).Label("永久清理").Height(kit.ButtonL).Padding(0, 34).Radius(kit.ButtonL / 2).
			Background(white).TextColor(ink).Disabled(len(sel) == 0)
		if len(sel) == 0 {
			b.Opacity(0.4)
		}
		b.Children(func() { ui.Text(c, label).FontSize(kit.TextTitle).FontWeight(kit.WeightSemibold).SingleLine() })
		b.OnClick(func() { m.confirm = true })
	})
}

// confirmDialog 确认框：写明多少项、多少空间，哪些永久删除、哪些移到废纸篓；确认后才开始删。
func (m *Module) confirmDialog(c *ui.Context, t kit.Theme) {
	sel := m.selected()
	var perm, trash, tools []core.Item
	emptyTrash := false
	for _, it := range sel {
		if it.Disposal == core.ToTrash {
			trash = append(trash, it)
		} else {
			perm = append(perm, it)
		}
		if len(it.Command) > 0 {
			tools = append(tools, it)
		}
		if it.Category == core.CatTrash {
			emptyTrash = true
		}
	}
	ui.Box(c.Key("confirm")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Children(func() {
		// 遮罩：点遮罩等于取消
		ui.ButtonBase(c.Key("scrim")).Label("取消清理").Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(ui.RGBA(0, 0, 0, 0.55)).OnClick(func() { m.confirm = false })
		// 对话框也脱离文档流，才会叠在（绝对定位的）遮罩上面
		ui.Column(c.Key("dialog")).Attach(ui.AnchorCenter, ui.AnchorCenter).Width(460).Padding(26, 28).Gap(14).Radius(18).Background(ui.Hex("#1d2333")).
			Border(1, ui.RGBA(255, 255, 255, 0.08)).Shadow(0, 20, 60, 0, ui.RGBA(0, 0, 0, 0.5)).Children(func() {
			ui.Text(c, fmt.Sprintf("永久清理 %d 项？", len(sel))).FontSize(kit.TextLargeTitle).FontWeight(kit.WeightSemibold).SingleLine()
			ui.Text(c, "将释放约 "+kit.HumanSize(sizeOf(sel))).Font(kit.Mono).FontSize(kit.TextTitle).TextColor(blue).SingleLine()
			line := func(text string, color ui.Color) {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
					ui.Text(c, "•").FontSize(kit.TextBody).TextColor(color).Shrink(0)
					ui.Text(c, text).FontSize(kit.TextBody).TextColor(color).Grow(1).MinWidth(0)
				})
			}
			ui.Column(c).FillWidth().Gap(8).Margin(4, 0, 6, 0).Children(func() {
				line(fmt.Sprintf("永久删除 %d 项（%s）：都是可以重新生成的缓存，不进废纸篓，删除后无法恢复。", len(perm), kit.HumanSize(sizeOf(perm))), t.Text)
				line(fmt.Sprintf("移到废纸篓 %d 项（%s）。", len(trash), kit.HumanSize(sizeOf(trash))), t.Muted)
				if len(tools) > 0 {
					names := []string{}
					for _, it := range tools {
						if n := toolName(it.Command); !contains(names, n) {
							names = append(names, n)
						}
					}
					line(fmt.Sprintf("其中 %d 项交给 %s 自己清理，实际释放可能少于显示。", len(tools), strings.Join(names, "、")), t.Muted)
				}
				if emptyTrash {
					line("包括清空废纸篓：里面的文件会被永久删除。", orange)
				}
				line("清理时相关 app 如果正在运行，会自动跳过；过程中可以取消，当前这一项做完就停。", t.Muted)
			})
			ui.Row(c).FillWidth().Gap(10).Justify(ui.End).Children(func() {
				kit.PillButton(c.Key("no"), t, "取消", false).OnClick(func() { m.confirm = false })
				kit.PillButton(c.Key("yes"), t, "永久清理", true).Label("确认永久清理").OnClick(m.startRun)
			})
		})
	})
}
