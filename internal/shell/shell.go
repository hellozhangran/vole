// Package shell 是窗口外壳：背景、顶部居中胶囊 tab、右上角版本标签、模块切换。
// 它只认 module.Module，不知道任何具体功能。
package shell

import (
	_ "embed"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vole/internal/kit"
	"vole/internal/module"
	"vole/internal/task"
)

// Options 配置外壳。
type Options struct {
	Name, Version string
	Modules       []module.Module // 顺序 = tab 顺序
	DefaultID     string          // 启动时停在哪个 tab
}

// host 为单个模块提供能力。
type host struct{ tasks *task.Runner }

func (h host) Tasks() *task.Runner { return h.tasks }

// Shell 持有外壳自己的状态（当前 tab），只在主线程读写。
type Shell struct {
	opts    Options
	active  int
	runners []*task.Runner
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
func Run(opts Options) error {
	var s *Shell
	closed := false
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
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: opts.Name, Width: 1200, Height: 780, MinWidth: 900, MinHeight: 600,
			Content:              ui.View(func(c *ui.Context) { s.View(c) }),
			TitleBarStyle:        mygo.TitleBarHiddenInset,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 20},
			BackgroundColor:      "#2a1e1a",
		})
		s = newShell(opts, post)
		win.Invalidate()
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		closed = true
		if s != nil {
			s.close()
		}
	})
	mygo.App.OnWindowAllClosed(func() { mygo.App.Quit() })
	return mygo.App.Run()
}

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
			s.topBar(c, t)
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
		panic("shell: logo.png 解码失败: " + err.Error())
	}
	return b
}()

// logo 是 tab 条最左侧的圆：和 app 图标同一只田鼠。
func logo(c *ui.Context) {
	// 右侧留白：选中态的白色胶囊紧贴第一个 tab 时，不至于和 logo 挤在一起
	ui.Image(c, logoBitmap).Size(kit.TabH, kit.TabH).Shrink(0).Radius(kit.TabH / 2).MarginEnd(10)
}
