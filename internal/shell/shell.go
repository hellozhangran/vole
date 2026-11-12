// Package shell 是窗口外壳：背景、顶部居中胶囊 tab、右上角版本标签、模块切换。
// 它只认 module.Module，不知道任何具体功能。
package shell

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vole/internal/i18n"
	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/task"
)

// Options 配置外壳。
type Options struct {
	Name, Version string
	Modules       []module.Module // 顺序 = tab 顺序
	DefaultID     string          // 启动时停在哪个 tab
	MenuBar       *MenuBar        // 非空：常驻菜单栏，关主窗口不退出
}

// MenuBar 是菜单栏图标和它弹出的面板。
type MenuBar struct {
	Icon  []byte // template 图标（黑色 + 透明，32px = 16pt @2x）
	Width int    // 面板宽度（pt）；高度由 Panel.Height 决定
	// Panel 用外壳给的 Controller 建面板（面板里的「打开 Vole」「退出」要回调外壳）。
	Panel func(ctl Controller) Panel
}

// Panel 是菜单栏面板：弹出时 Show（开始采集），收起时 Hide（停掉）。
type Panel interface {
	Init(tasks *task.Runner)
	Show()
	Hide()
	View(c *ui.Context)
	Height() int // 面板现在该有的高度（pt），随内容变；外壳每次更新后按它调大小
}

// Controller 是面板能让外壳做的事。
type Controller interface {
	ShowMain(tab string) // 打开（必要时重新显示）主窗口；tab 非空时切过去
	Quit()
}

// host 为单个模块提供能力。
type host struct{ tasks *task.Runner }

func (h host) Tasks() *task.Runner { return h.tasks }

// Shell 持有外壳自己的状态（当前 tab），只在主线程读写。
type Shell struct {
	opts    Options
	active  int
	runners []*task.Runner

	win       *mygo.Window
	visible   bool         // 主窗口显示着
	fly       *mygo.Window // 菜单栏面板
	tray      *mygo.Tray
	noMenuBar bool // 菜单栏图标没建成，退回「关窗即退出」
}

// newShell 创建外壳并初始化所有模块；post 把函数排到主线程（窗口里是 win.Update，测试里是队列）。
func newShell(opts Options, post func(func())) *Shell {
	s := &Shell{opts: opts}
	for i, m := range opts.Modules {
		r := task.NewRunner(post)
		s.runners = append(s.runners, r)
		m.Init(host{tasks: r})
		if m.ID() == opts.DefaultID {
			s.active = i
		}
	}
	if len(opts.Modules) > 0 {
		opts.Modules[s.active].OnActivate()
	}
	return s
}

// switchTo 切 tab：先让旧模块停下只在可见时才跑的东西，再激活新模块。
func (s *Shell) switchTo(i int, m module.Module) {
	s.opts.Modules[s.active].OnDeactivate()
	s.active = i
	m.OnActivate()
}

// close 取消所有模块的后台任务（会杀掉还在跑的扫描进程）。
func (s *Shell) close() {
	for _, r := range s.runners {
		r.Close()
	}
}

// Run 打开主窗口，阻塞到应用退出。
// 有菜单栏时：关主窗口只是隐藏（同时隐藏 Dock 图标，切成 accessory），⌘Q 或面板里的「退出」才真正退出；
// 开机自启时主窗口不显示，只留菜单栏图标。
func Run(opts Options) error {
	var s *Shell
	closed := false
	quitting := false
	mygo.App.SetName(opts.Name)
	mygo.App.WhenReady(func() {
		var win *mygo.Window
		post := func(fn func()) {
			win.Update(func() {
				if !closed {
					fn()
				}
			})
		}
		atLogin := opts.MenuBar != nil && mygo.App.WasOpenedAtLogin()
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: opts.Name, Width: 1200, Height: 780, MinWidth: 900, MinHeight: 600,
			Content:              ui.View(func(c *ui.Context) { s.View(c) }),
			TitleBarStyle:        mygo.TitleBarHiddenInset,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 20},
			BackgroundColor:      "#2a1e1a",
			Hidden:               atLogin,
		})
		s = newShell(opts, post)
		s.win, s.visible = win, !atLogin
		if atLogin {
			s.opts.Modules[s.active].OnDeactivate() // 不显示主窗口时，只在可见时才跑的东西不跑
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		}
		win.Invalidate()
		if opts.MenuBar == nil {
			return
		}
		win.OnClose(func(e *mygo.CloseEvent) {
			if quitting {
				return
			}
			e.PreventDefault()
			s.hideMain()
		})
		// 主窗口隐藏时再打开 app（Finder 里双击、Spotlight 打开）：重新显示
		mygo.App.OnActivate(func(hasVisibleWindows bool) {
			if !s.visible {
				s.ShowMain("")
			}
		})
		if err := s.startMenuBar(&quitting); err != nil {
			// 菜单栏起不来时退回普通窗口 app：关窗就退出
			fmt.Fprintln(os.Stderr, i18n.T("菜单栏图标创建失败："), err)
			s.noMenuBar = true
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		quitting = true
		closed = true
		if s != nil {
			s.close()
		}
	})
	mygo.App.OnWindowAllClosed(func() {
		if opts.MenuBar == nil || (s != nil && s.noMenuBar) {
			mygo.App.Quit()
		}
	})
	return mygo.App.Run()
}

// startMenuBar 建菜单栏图标和面板（面板一开始隐藏，点图标弹出 / 收起）。
func (s *Shell) startMenuBar(quitting *bool) error {
	mb := s.opts.MenuBar
	tray, err := mygo.NewTray(mygo.TrayOptions{ID: "vole", Icon: mb.Icon, IconIsTemplate: true, ToolTip: s.opts.Name})
	if err != nil {
		return err
	}
	panel := mb.Panel(s)
	height := panel.Height()
	var fly *mygo.Window
	fly = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: tray, Placement: mygo.PlacementBottom, Gap: 4,
		Width: mb.Width, Height: height, Focusable: true, Shadow: true, CornerRadius: 14, Hidden: true,
		Content: ui.View(func(c *ui.Context) {
			panel.View(c)
			// 内容变了（比如确认没有电池、读到了清理记录）就跟着改高度。不在绘制中途改窗口，排到下一轮；
			// 只在高度不一致时才排，改完就一致了，不会反复触发。SetSize 会按图标位置重新摆。
			if panel.Height() != height {
				fly.Update(func() {
					if h := panel.Height(); h != height {
						height = h
						fly.SetSize(mb.Width, h)
					}
				})
			}
		}),
	})
	r := task.NewRunner(func(fn func()) { fly.Update(fn) })
	s.runners = append(s.runners, r)
	panel.Init(r)
	s.fly = fly
	fly.OnShow(panel.Show)
	fly.OnHide(panel.Hide)
	// 点面板外面、按 Esc 时系统会关面板：改成隐藏，下次点图标还用同一个
	fly.OnClose(func(e *mygo.CloseEvent) {
		if *quitting {
			return
		}
		e.PreventDefault()
		fly.Hide()
	})
	tray.OnClick(func() {
		if fly.IsVisible() {
			fly.Hide()
		} else {
			s.showPanel()
		}
	})
	s.tray = tray
	return nil
}

// showPanel 弹出面板。弹出前先按菜单栏图标「现在」的位置重新摆一次：
// mygo 的面板只在创建时按 tray.Bounds() 定一次位置，Show 不会重新定位，没有父窗口也不会因为父窗口移动而重排
// （mygo internal/darwin/window.go 的 PlaceFlyout 只在创建、SetAnchor、SetSize 时调）。Vole 启动时就建好面板，
// 那时图标可能被刘海挡住（Bounds 不对），用户之后按住 ⌘ 拖动图标换位置也一样，面板就会弹在别处（作者实测在左下角）。
// SetAnchor 对挂在图标上的面板会忽略传入的矩形、重新取 tray.Bounds()，这里只借它触发重新定位。
// 图标真被刘海完全挡住时 Bounds 宽高为 0，mygo 会把面板放到主屏中央。以后可以给 mygo 报：Show 时应重新定位。
func (s *Shell) showPanel() {
	s.fly.SetAnchor(mygo.Rectangle{})
	s.fly.Show()
}

// hideMain 把主窗口藏起来（关窗时），并隐藏 Dock 图标；当前模块停掉只在可见时才跑的东西。
func (s *Shell) hideMain() {
	if !s.visible {
		return
	}
	s.visible = false
	s.opts.Modules[s.active].OnDeactivate()
	s.win.Hide()
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
}

// ShowMain 显示主窗口（隐藏着就重新显示，恢复 Dock 图标），tab 非空时切过去。面板会先收起。
func (s *Shell) ShowMain(tab string) {
	if s.fly != nil && s.fly.IsVisible() {
		s.fly.Hide()
	}
	target := s.active
	for i, m := range s.opts.Modules {
		if m.ID() == tab {
			target = i
		}
	}
	if !s.visible {
		s.visible = true
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
		s.active = target
		s.opts.Modules[s.active].OnActivate()
	} else if target != s.active {
		s.switchTo(target, s.opts.Modules[target])
	}
	s.win.Show()
	mygo.App.Focus()
	s.win.Invalidate()
}

// Quit 退出 Vole（面板里的「退出」）。
func (s *Shell) Quit() { mygo.App.Quit() }

// View 画整个窗口：主题背景 + 顶栏 + 当前模块。
func (s *Shell) View(c *ui.Context) {
	if s == nil || len(s.opts.Modules) == 0 {
		return
	}
	m := s.opts.Modules[s.active]
	t := m.Theme()
	theme := *c.Theme()
	theme.Dark, theme.Text, theme.TextMuted, theme.Accent = true, t.Text, t.Muted, t.Accent
	// 提示框（Tooltip）用 Inverse 做底、InverseText 做字；不设时分别取 Text 和 Background。
	// 只改 Text 不改 Background 时，系统浅色外观下就成了浅底白字，所以这里成套设成深色主题。
	theme.Background = t.BgBottom
	theme.Inverse, theme.InverseText = ui.Hex("#2b2724"), t.Text
	c.SetTheme(&theme)

	ui.Column(c).Fill().TextColor(t.Text).FontSize(kit.TextBody).
		LinearGradient(ui.LinearGradient{From: t.BgTop, To: t.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
		Children(func() {
			// 模块要求整页显示时（如清理的结果页）不画顶栏，整个窗口交给模块
			if fp, ok := m.(module.FullPage); !ok || !fp.FullPage() {
				s.topBar(c, t)
			}
			// 每个模块用自己的 key，切 tab 时各自的控件状态（滚动位置等）互不干扰
			ui.Box(c.Key("module-" + m.ID())).Grow(1).FillWidth().MinHeight(0).Children(func() { m.View(c) })
		})
}

// topBar 是可拖动窗口的顶栏：居中胶囊 tab 条 + 右上版本标签。
func (s *Shell) topBar(c *ui.Context, t kit.Theme) {
	// 拖动区铺满顶栏；tab 条和版本标签用 Attach/Absolute 叠在它上面，才能接到点击
	ui.Box(c.Key("topbar")).FillWidth().Height(kit.TopBarH).Shrink(0).Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).DragWindow()
		ui.Row(c.Key("tabs")).Attach(ui.AnchorTop, ui.AnchorTop).Top(35).Height(kit.TabH + 8).Padding(4).Gap(2).Radius(kit.TabH/2 + 4).Background(t.TabBar).Children(func() {
			logo(c)
			for i, m := range s.opts.Modules {
				tab := ui.ButtonBase(c.Key("tab-"+m.ID())).Label(m.Title()).Height(kit.TabH).Padding(0, 18).Radius(kit.TabH / 2).FontSize(kit.TextBody).TextColor(t.Muted)
				if i == s.active {
					tab.Background(ui.Hex("#ffffff")).TextColor(ui.Hex("#111111"))
				} else if tab.Hovered() {
					tab.TextColor(t.Text)
				}
				tab.Children(func() { ui.Text(c, m.Title()).FontWeight(kit.WeightSemibold).SingleLine() })
				if i != s.active {
					idx, mod := i, m
					tab.OnClick(func() { s.switchTo(idx, mod) })
				}
			}
		})
		ui.Row(c.Key("version")).Absolute().Top(41).Right(kit.Gutter).Height(26).Padding(0, 10).Gap(6).Radius(13).Background(t.Surface).Children(func() {
			kit.Dot(c, t.OK, 6)
			ui.Text(c, s.opts.Name+" "+s.opts.Version).Font(kit.Mono).FontSize(kit.TextCaption).TextColor(t.Muted).SingleLine()
		})
	})
}

// logoPNG 是从 app 图标（resources/icon.png）裁出的圆形田鼠头像，160px，透明圆外。
//
//go:embed logo.png
var logoPNG []byte

// logoBitmap 启动时解码一次；图是编译期嵌入的，解码失败只可能是文件坏了，直接 panic 暴露。
var logoBitmap = func() *ui.Bitmap {
	b, err := ui.DecodeBitmap(logoPNG)
	if err != nil {
		panic(i18n.T("shell: logo.png 解码失败: ") + err.Error())
	}
	return b
}()

// logo 是 tab 条最左侧的圆：和 app 图标同一只田鼠。
func logo(c *ui.Context) {
	// 右侧留白：选中态的白色胶囊紧贴第一个 tab 时，不至于和 logo 挤在一起
	ui.Image(c, logoBitmap).Size(kit.TabH, kit.TabH).Shrink(0).Radius(kit.TabH / 2).MarginEnd(10)
}
