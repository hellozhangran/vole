package analyze

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/analyze"
	"vole/internal/core/sysenv"
	"vole/internal/i18n"
	"vole/internal/kit"
)

const sidebarWidth = 232

var failColor = ui.Hex("#f08a7a")

// View 画分析 tab：左侧栏（主视觉 + 统计 + 当前目录列表）+ 主区（标题栏 + treemap / 大文件）。
func (m *Module) View(c *ui.Context) {
	t := m.theme
	v := m.state()
	if v.anyRunning() {
		c.After(time.Second) // 每秒刷新「已用时」
	}
	ui.Row(c).Fill().Padding(0, kit.Gutter, kit.Gutter, 12).Gap(kit.Gutter).Children(func() {
		m.sidebar(c, t, v)
		ui.Column(c.Key("main")).Grow(1).FillHeight().MinWidth(0).Gap(10).Children(func() {
			m.header(c, t, v)
			m.body(c, t, v)
		})
	})
}

// ── 左侧栏 ──

func (m *Module) sidebar(c *ui.Context, t kit.Theme, v viewState) {
	ui.Column(c.Key("sidebar")).Width(sidebarWidth).Shrink(0).FillHeight().Children(func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(12).Padding(14, 0, 18, 0).Children(func() {
			// 扫描中是奔跑的田鼠，扫完换成拿放大镜的静止插画（不再请求重绘）
			ui.Box(c).Size(kit.Hero, kit.Hero).Center().Children(func() {
				if v.anyRunning() {
					kit.RunningVole(c, kit.Hero, kit.RunningFPS, true)
				} else {
					kit.Art(c, kit.ArtAnalyzeIdle, kit.Hero)
				}
			})
			ui.Text(c, summary(v)).Font(kit.Mono).FontSize(kit.TextBody).TextColor(t.Muted).SingleLine()
		})
		ui.Text(c, i18n.T("当前目录")).FontSize(kit.TextSecondary).FontWeight(kit.WeightSemibold).TextColor(t.Muted).Padding(0, 10, 6, 10)
		ui.Scroll(c.Key("list-" + m.current())).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				for _, it := range v.items {
					m.row(c, t, it)
				}
			})
		})
		m.footer(c, t)
	})
}

func summary(v viewState) string {
	if !v.root && v.report == nil {
		return i18n.T("计算中…")
	}
	return fmt.Sprintf(i18n.T("%d 项，%s"), len(v.items), kit.HumanSize(v.total))
}

// row 是侧栏的一行：图标 + 名称 + 大小/状态 + 下钻箭头。点击下钻，右键在 Finder 中显示。
func (m *Module) row(c *ui.Context, t kit.Theme, it item) {
	b := ui.ButtonBase(c.Key(it.path)).Label(i18n.T("条目 ")+it.name).FillWidth().Height(kit.RowH).Padding(0, 10).Gap(10).Radius(kit.RadiusM).Justify(ui.Start)
	if b.Hovered() {
		b.Background(t.Hover)
	}
	m.revealMenu(b, it.path)
	b.Children(func() {
		icon := kit.FolderIcon
		if !it.isDir {
			icon = kit.FileIcon
		}
		ui.Icon(c, icon).Size(kit.IconL, kit.IconL).Shrink(0).TextColor(t.Muted)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, it.name).FontSize(kit.TextBody).SingleLine().Ellipsis("…").FillWidth()
			m.subline(c, t, it)
		})
		if it.drillable() {
			ui.Icon(c, kit.ChevronIcon).Size(kit.IconS, kit.IconS).Shrink(0).TextColor(t.Faint)
		}
	})
	if it.drillable() {
		path := it.path
		b.OnClick(func() { m.drill(path) })
	}
}

// subline 是行内第二行：计算中 / 失败 / 大小 + 标注。
func (m *Module) subline(c *ui.Context, t kit.Theme, it item) {
	ui.Row(c).Gap(4).Children(func() {
		switch {
		case it.pending:
			ui.Text(c, i18n.T("计算中")).FontSize(kit.TextSecondary).TextColor(t.Accent).SingleLine()
			ui.Text(c, "· "+m.elapsed(c, it.started)).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
		case it.failed != "":
			ui.Text(c, i18n.T("扫描失败")).FontSize(kit.TextSecondary).TextColor(failColor).Tooltip(it.failed).SingleLine()
		case it.canceled:
			ui.Text(c, i18n.T("已取消")).FontSize(kit.TextSecondary).TextColor(t.Faint).SingleLine()
		default:
			ui.Text(c, sizeLabel(it.size, it.status)).FontSize(kit.TextSecondary).TextColor(t.Muted).SingleLine()
			tags(c, t, it)
		}
	})
}

// tags 标注：不完整 / 可清理 / 最近访问。
func tags(c *ui.Context, t kit.Theme, it item) {
	if it.status == core.StatusPartial {
		tag(c, t, i18n.T("不完整"), t.Accent).Tooltip(i18n.T("部分子目录超时或无权读取，实际可能更大"))
	}
	if it.cleanable {
		tag(c, t, i18n.T("可清理"), t.OK).Tooltip(i18n.T("可再生的缓存或依赖目录，删除后可重新生成"))
	}
	if !it.isDir && !it.lastAccess.IsZero() {
		ui.Text(c, i18n.T("访问于 ")+it.lastAccess.Local().Format("2006-01-02")).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine()
	}
}

func tag(c *ui.Context, t kit.Theme, text string, color ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(kit.TextCaption).Padding(0, 5).Radius(4).Background(color.Alpha(0.16)).TextColor(color).SingleLine()
}

// sizeLabel：无法读取 → "无法读取"；不完整 → "≥ 大小"。
func sizeLabel(size int64, s core.Status) string {
	switch s {
	case core.StatusUnavailable:
		return i18n.T("无法读取")
	case core.StatusPartial:
		return "≥ " + kit.HumanSize(size)
	}
	return kit.HumanSize(size)
}

func (m *Module) elapsed(c *ui.Context, started time.Time) string {
	if started.IsZero() {
		return i18n.T("排队中")
	}
	return i18n.TC("elapsed", "已用 ") + kit.Elapsed(c.Now().Sub(started))
}

// footer 底部提示；没有完全磁盘访问时提示去设置里授权。
func (m *Module) footer(c *ui.Context, t kit.Theme) {
	ui.Column(c).FillWidth().Gap(8).Padding(10, 10, 0, 10).Children(func() {
		if m.env.FullDiskAccess == sysenv.AccessDenied {
			ui.Row(c).FillWidth().Gap(6).Padding(8, 10).Radius(kit.RadiusM).Background(t.Surface).Children(func() {
				ui.Icon(c, kit.LockIcon).Size(kit.IconM, kit.IconM).Shrink(0).TextColor(t.Accent)
				ui.Text(c, i18n.T("未获得完全磁盘访问，部分目录读不到")).FontSize(kit.TextCaption).TextColor(t.Muted).Grow(1).MinWidth(0)
				link := ui.ButtonBase(c.Key("fda")).Label(i18n.T("去设置")).Shrink(0).FontSize(kit.TextCaption).TextColor(t.Accent)
				link.Children(func() { ui.Text(c, i18n.T("去设置")).SingleLine() })
				link.OnClick(m.openSettings)
			})
		}
		ui.Text(c, i18n.T("右键可在 Finder 中显示")).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine()
	})
}

// revealMenu 给元素挂右键菜单。M2 只做「在 Finder 中显示」；移到废纸篓属于删除，放二期。
func (m *Module) revealMenu(e ui.Element, path string) {
	e.ContextMenu(func(menu *ui.Menu) {
		if menu.Item(i18n.T("在 Finder 中显示")).Chosen() {
			m.reveal(path)
		}
	})
}

// ── 主区标题栏 ──

func (m *Module) header(c *ui.Context, t kit.Theme, v viewState) {
	ui.Row(c.Key("header")).FillWidth().Height(32).Shrink(0).Gap(10).Children(func() {
		m.breadcrumb(c, t)
		ui.Spacer(c)
		m.status(c, t, v)
	})
}

// breadcrumb 是「整个磁盘 › Users/alice › Library」，点前面的段回到那一层（不重扫）。
func (m *Module) breadcrumb(c *ui.Context, t kit.Theme) {
	ui.Row(c.Key("crumbs")).Gap(2).MinWidth(0).Shrink(1).Children(func() {
		crumb := func(depth int, name string, icon bool) {
			last := depth == len(m.stack)
			b := ui.ButtonBase(c.Key(fmt.Sprint("crumb-", depth))).Label(i18n.T("返回 ")+name).Height(26).Padding(0, 6).Gap(6).Radius(kit.RadiusS).MinWidth(0).Shrink(1)
			b.TextColor(t.Muted)
			if last {
				b.TextColor(t.Text)
			} else if b.Hovered() {
				b.Background(t.Hover).TextColor(t.Text)
			}
			b.Children(func() {
				if icon {
					ui.Icon(c, kit.DiskIcon).Size(kit.IconL, kit.IconL).Shrink(0)
				}
				txt := ui.Text(c, name).FontSize(kit.TextTitle).SingleLine().Ellipsis("…")
				if last {
					txt.Bold()
				}
			})
			if !last {
				b.OnClick(func() { m.goTo(depth) })
			}
		}
		crumb(0, i18n.T("整个磁盘"), true)
		for i, p := range m.stack {
			ui.Icon(c, kit.ChevronIcon).Size(kit.IconS, kit.IconS).Shrink(0).TextColor(t.Faint)
			crumb(i+1, m.displayName(p), false)
		}
	})
}

// status 是标题栏右侧：进度 / 计算中 + 已用时 / 磁盘容量 + 刷新或取消。
func (m *Module) status(c *ui.Context, t kit.Theme, v viewState) {
	ui.Row(c.Key("status")).Gap(10).Shrink(0).Children(func() {
		switch {
		case v.root && v.pending > 0:
			progress(c, t, float32(v.done)/float32(max(len(v.items), 1)))
			ui.Text(c, fmt.Sprintf(i18n.T("当前 %s + %d 项计算中"), kit.HumanSize(v.total), v.pending)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Accent).SingleLine()
			m.diskLabel(c, t)
			kit.IconButton(c.Key("cancel"), t, i18n.T("取消扫描"), kit.CloseIcon, kit.ControlM).OnClick(m.cancelCurrent)
		case !v.root && v.report == nil && v.job != nil && v.job.state == jobRunning:
			kit.Dot(c, t.Accent, 6)
			ui.Text(c, i18n.T("计算中 · ")+m.elapsed(c, v.job.started)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Accent).SingleLine()
			kit.IconButton(c.Key("cancel"), t, i18n.T("取消扫描"), kit.CloseIcon, kit.ControlM).OnClick(m.cancelCurrent)
		default:
			if v.report != nil && len(v.report.LargeFiles) > 0 {
				ui.Row(c.Key("seg")).Gap(2).Padding(2).Radius(kit.ControlS/2 + 2).Background(t.Surface).Children(func() {
					kit.Segment(c.Key("seg-map"), t, i18n.T("占用图"), !m.showLarge).OnClick(func() { m.showLarge = false })
					kit.Segment(c.Key("seg-large"), t, fmt.Sprintf(i18n.T("大文件 %d"), len(v.report.LargeFiles)), m.showLarge).OnClick(func() { m.showLarge = true })
				})
			}
			m.diskLabel(c, t)
			kit.IconButton(c.Key("refresh"), t, i18n.T("重新扫描"), kit.RefreshIcon, kit.ControlM).OnClick(m.refresh)
		}
	})
}

func progress(c *ui.Context, t kit.Theme, frac float32) {
	ui.Box(c).Width(90).Height(3).Radius(2).Background(t.Surface).Children(func() {
		ui.Box(c).WidthPercent(max(frac, 0.04) * 100).FillHeight().Radius(2).Background(t.Accent)
	})
}

func (m *Module) diskLabel(c *ui.Context, t kit.Theme) {
	if !m.diskOK {
		return
	}
	ui.Row(c).Gap(6).Children(func() {
		kit.Dot(c, t.Accent, 6)
		ui.Text(c, fmt.Sprintf(i18n.T("磁盘 %s / %s"), kit.HumanSize(m.disk.Used()), kit.HumanSize(m.disk.Total))).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Faint).SingleLine()
	})
}

// ── 主区内容 ──

func (m *Module) body(c *ui.Context, t kit.Theme, v viewState) {
	ui.Column(c.Key("body-" + m.current())).Grow(1).FillWidth().MinHeight(0).Children(func() {
		if err := m.brokenErr(); err != nil {
			detail := strings.TrimPrefix(strings.TrimPrefix(err.Error(), core.ErrScannerUnavailable.Error()), i18n.T("："))
			m.message(c, t, kit.ArtError, i18n.T("分析组件不可用"), strings.TrimSpace(detail)+i18n.T("。Vole.app 可能不完整或已损坏，请重新安装；开发期请检查 VOLE_ANALYZE_BIN。"), true)
			return
		}
		switch {
		case v.root && v.done == 0 && v.pending > 0:
			// 第一个结果出来前，主区用奔跑动画填住
			m.loading(c, t, fmt.Sprintf(i18n.T("正在扫描 %d 个目录"), v.pending), i18n.T("每个目录扫完就会出现在这里；Users 目录第一次可能要几分钟"))
		case v.root:
			if len(v.items) == 0 {
				m.message(c, t, kit.ArtAnalyzeIdle, i18n.T("没有可扫描的目录"), "", false)
				return
			}
			m.treemap(c, t, v)
		case v.report != nil:
			if m.showLarge {
				m.largeFiles(c, t, v.report)
			} else if hasSize(v.items) {
				m.treemap(c, t, v)
			} else if v.report.Status != core.StatusComplete && m.env.FullDiskAccess == sysenv.AccessDenied {
				// 读不到内容、又没有完全磁盘访问：多半是权限挡住了，不说「空的」
				m.fdaMessage(c, t)
			} else {
				m.message(c, t, kit.ArtAnalyzeIdle, i18n.T("这个目录是空的"), "", false)
			}
		case v.job != nil && v.job.state == jobFailed:
			m.message(c, t, kit.ArtError, i18n.T("扫描失败"), v.job.err.Error(), true)
		case v.job != nil && v.job.state == jobCanceled:
			m.message(c, t, "", i18n.T("已取消扫描"), "", true)
		default:
			m.loading(c, t, i18n.T("正在扫描 ")+m.displayName(m.current()), i18n.T("扫完才会返回结果，大目录第一次可能要几分钟；扫过的子目录会快很多"))
		}
	})
}

func hasSize(items []item) bool {
	for _, it := range items {
		if it.size > 0 {
			return true
		}
	}
	return false
}

// message 是主区居中的一段提示，可带「重新扫描」按钮。
// art 为空时不放插画。
func (m *Module) message(c *ui.Context, t kit.Theme, art kit.ArtName, title, detail string, retry bool) {
	ui.Column(c).Fill().Center().Gap(10).Padding(0, 40, 80, 40).Children(func() {
		if art != "" {
			kit.Art(c, art, kit.ArtS).Margin(0, 0, 4, 0)
		}
		ui.Text(c, title).FontSize(kit.TextHeadline).TextAlign(ui.Center)
		if detail != "" {
			ui.Text(c, detail).FontSize(kit.TextSecondary).TextColor(t.Muted).TextAlign(ui.Center).MaxWidth(460)
		}
		if retry {
			kit.PillButton(c.Key("retry"), t, i18n.T("重新扫描"), true).Margin(10, 0, 0, 0).OnClick(m.refresh)
		}
	})
}

// fdaMessage：目录读不到、且没有完全磁盘访问时的空状态，给「去设置」。
func (m *Module) fdaMessage(c *ui.Context, t kit.Theme) {
	ui.Column(c).Fill().Center().Gap(10).Padding(0, 40, 80, 40).Children(func() {
		kit.Art(c, kit.ArtError, kit.ArtS).Margin(0, 0, 4, 0)
		ui.Text(c, i18n.T("需要完全磁盘访问")).FontSize(kit.TextHeadline).TextAlign(ui.Center)
		ui.Text(c, i18n.T("这个目录受系统保护，Vole 没有权限读取。到「系统设置 › 隐私与安全性 › 完全磁盘访问」里打开 Vole 后重新扫描。")).
			FontSize(kit.TextSecondary).TextColor(t.Muted).TextAlign(ui.Center).MaxWidth(460)
		ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
			kit.PillButton(c.Key("fda-settings"), t, i18n.T("去设置"), true).OnClick(m.openSettings)
			kit.PillButton(c.Key("retry"), t, i18n.T("重新扫描"), false).OnClick(m.refresh)
		})
	})
}

// loading 是主区居中的等待状态：奔跑的田鼠 + 标题 + 说明。
func (m *Module) loading(c *ui.Context, t kit.Theme, title, detail string) {
	ui.Column(c).Fill().Center().Gap(10).Padding(0, 40, 80, 40).Children(func() {
		kit.RunningVole(c, kit.Hero, kit.RunningFPS, true)
		ui.Text(c, title).FontSize(kit.TextHeadline).TextAlign(ui.Center)
		ui.Text(c, detail).FontSize(kit.TextSecondary).TextColor(t.Muted).TextAlign(ui.Center).MaxWidth(460)
	})
}

// largeFiles 是目录视图的大文件列表（analyze 递归找出的 top 20），右键在 Finder 中显示。
func (m *Module) largeFiles(c *ui.Context, t kit.Theme, r *core.Report) {
	ui.Scroll(c.Key("large")).Fill().Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			for _, f := range r.LargeFiles {
				b := ui.ButtonBase(c.Key(f.Path)).Label(i18n.T("大文件 ")+f.Name).FillWidth().Height(kit.RowH).Padding(0, 12).Gap(10).Radius(kit.RadiusM).Justify(ui.Start)
				if b.Hovered() {
					b.Background(t.Hover)
				}
				m.revealMenu(b, f.Path)
				b.Children(func() {
					ui.Icon(c, kit.FileIcon).Size(kit.IconL, kit.IconL).Shrink(0).TextColor(t.Muted)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, f.Name).FontSize(kit.TextBody).SingleLine().Ellipsis("…").FillWidth()
						ui.Text(c, f.Path).FontSize(kit.TextCaption).TextColor(t.Faint).SingleLine().Ellipsis("…").FillWidth()
					})
					ui.Text(c, kit.HumanSize(f.Size)).Font(kit.Mono).FontSize(kit.TextSecondary).TextColor(t.Muted).Shrink(0).SingleLine()
				})
			}
		})
	})
}
