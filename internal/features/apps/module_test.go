// SPDX-License-Identifier: GPL-3.0-only

package apps

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/apps"
	"vole/internal/kit"
	"vole/internal/task"
)

// fakeBackend 是假的卸载数据源：列表和关联文件是固定的，卸载只记录清单、按 gate 一个一个「完成」，绝不碰磁盘。
type fakeBackend struct {
	mu       sync.Mutex
	apps     []core.App
	files    map[string][]core.File
	listErr  error
	listGate chan struct{}
	runGate  chan struct{}
	runs     [][]core.Plan
	quits    []bool
	lists    int
}

func (f *fakeBackend) List(ctx context.Context) ([]core.App, error) {
	f.mu.Lock()
	f.lists++
	gate := f.listGate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.apps, f.listErr
}

func (f *fakeBackend) Files(ctx context.Context, a core.App, installed []core.App) []core.File {
	return f.files[a.Path]
}

func (f *fakeBackend) Run(ctx context.Context, plans []core.Plan, quit bool, progress func(int, int, core.Outcome)) *core.Report {
	f.mu.Lock()
	f.runs = append(f.runs, plans)
	f.quits = append(f.quits, quit)
	f.mu.Unlock()
	rep := &core.Report{}
	for i, p := range plans {
		if ctx.Err() != nil {
			for _, rest := range plans[i:] {
				rep.Outcomes = append(rep.Outcomes, core.Outcome{App: rest.App, Status: core.StatusCanceled, Reason: "用户取消"})
			}
			rep.Canceled = true
			break
		}
		if f.runGate != nil {
			<-f.runGate
		}
		o := core.Outcome{App: p.App, Status: core.StatusDone, Moved: 1 + len(p.Files), Size: p.App.Size}
		for _, file := range p.Files {
			o.Size += file.Size
		}
		if p.App.NeedsAdmin {
			o = core.Outcome{App: p.App, Status: core.StatusFailed, Reason: "已取消（没有输入管理员密码）"}
		}
		rep.Outcomes = append(rep.Outcomes, o)
		progress(i+1, len(plans), o)
	}
	return rep
}

func (f *fakeBackend) TrashDir() string { return "/Users/test/.Trash" }
func (f *fakeBackend) LogDir() string   { return "/Users/test/Library/Logs/Vole" }

var now = time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)

func app(name, version string, size int64, mut func(*core.App)) core.App {
	a := core.App{Path: "/Applications/" + name + ".app", BundleID: "com.test." + strings.ToLower(strings.ReplaceAll(name, " ", "")),
		Name: name, Version: version, Size: size, SizeKnown: true, LastUsed: now.Add(-24 * time.Hour)}
	if mut != nil {
		mut(&a)
	}
	return a
}

func file(path string, kind core.Kind, size int64, extra string) core.File {
	return core.File{Path: strings.Replace(path, "~", "/Users/test", 1), Kind: kind, Size: size, Default: kind == core.KindCache, Extra: extra}
}

func fakeData() *fakeBackend {
	be := &fakeBackend{files: map[string][]core.File{}}
	be.apps = []core.App{
		app("Xcode", "16.2", 5_920_000_000, func(a *core.App) {
			a.Source, a.NeedsAdmin, a.LastUsed = core.SourceAppStore, true, now.Add(-190*24*time.Hour)
		}),
		app("Google Chrome", "156.0.8078.80", 2_290_000_000, func(a *core.App) { a.Running = true }),
		app("Docker", "4.41.2", 2_110_000_000, func(a *core.App) { a.Running, a.MenuBar = true, true }),
		app("百度网盘", "4.49.1", 848_500_000, func(a *core.App) { a.Intel, a.LastUsed = true, now.Add(-400*24*time.Hour) }),
		app("Task Manager TMOG", "1.1.0", 55_100_000, func(a *core.App) { a.Running = true }),
		app("Vole", "0.1.0", 21_500_000, func(a *core.App) { a.Blocked = "Vole 正在使用，不能卸载自己" }),
	}
	be.files["/Applications/Docker.app"] = []core.File{
		file("~/Library/Containers/com.docker.docker", core.KindData, 79_700_000_000, ""),
		file("~/Library/Application Support/Docker Desktop", core.KindData, 10_800_000, "内嵌的 Docker Desktop.app"),
		file("~/Library/Caches/com.docker.docker", core.KindCache, 3_400_000, ""),
		file("~/.docker/buildx", core.KindData, 792_000, ""),
		file("/Library/PrivilegedHelperTools/com.docker.vmnetd", core.KindSystem, 0, ""),
	}
	be.files["/Applications/Task Manager TMOG.app"] = []core.File{
		file("~/Library/Caches/com.test.taskmanagertmog", core.KindCache, 1_000_000, ""),
		file("~/Library/Preferences/com.test.taskmanagertmog.plist", core.KindData, 4_000, ""),
	}
	return be
}

type harness struct {
	t      *testing.T
	m      *Module
	be     *fakeBackend
	tt     *ui.Tester
	main   chan func()
	opened []string
}

type host struct{ r *task.Runner }

func (h host) Tasks() *task.Runner { return h.r }

func newHarness(t *testing.T, be *fakeBackend) *harness {
	t.Helper()
	h := &harness{t: t, be: be, main: make(chan func(), 256)}
	h.m = New(be)
	h.m.home = "/Users/test"
	h.m.now = func() time.Time { return now }
	h.m.reveal = func(string) {}
	h.m.openPath = func(p string) { h.opened = append(h.opened, p) }
	h.m.fileIcon = func(string) ([]byte, error) { return nil, errors.New("测试里不取图标") }
	r := task.NewRunner(func(fn func()) { h.main <- fn })
	t.Cleanup(r.Close)
	h.m.Init(host{r})
	th := h.m.Theme()
	h.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().TextColor(th.Text).FontSize(kit.TextBody).
			LinearGradient(ui.LinearGradient{From: th.BgTop, To: th.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
			Children(func() { h.m.View(c) })
	}, 1194, 676) // 窗口 762 减去顶栏
	return h
}

func (h *harness) drain(wait time.Duration) {
	for {
		select {
		case fn := <-h.main:
			fn()
		case <-time.After(wait):
			h.tt.Frame()
			return
		}
	}
}

func (h *harness) click(label string) {
	h.t.Helper()
	if err := h.tt.Click(label); err != nil {
		h.t.Fatalf("点击 %q：%v；当前文字：%q", label, err, h.tt.Texts())
	}
	h.tt.Frame()
	h.drain(50 * time.Millisecond)
}

func (h *harness) want(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if !h.tt.HasText(s) {
			h.t.Fatalf("界面上应有 %q；当前文字：%q", s, h.tt.Texts())
		}
	}
}

func (h *harness) snapshot(name string) {
	dir := os.Getenv("VOLE_SNAPSHOT_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	_ = png.Encode(f, h.tt.Image())
}

func (h *harness) order() []string {
	var out []string
	for _, a := range h.m.visible() {
		out = append(out, a.Name)
	}
	return out
}

func TestListSortSearch(t *testing.T) {
	be := fakeData()
	be.listGate = make(chan struct{})
	h := newHarness(t, be)
	h.m.OnActivate()
	h.drain(50 * time.Millisecond)
	h.want("正在读取已安装的应用…")
	h.snapshot("01-loading")
	close(be.listGate)
	h.drain(100 * time.Millisecond)

	h.want("已安装应用", "6 个 · 11.2 GB", "Xcode", "16.2", "5.92 GB", "6个月前 活跃", "App Store", "需要管理员密码",
		"活跃中", "菜单栏常驻", "Intel", "1年前 活跃", "Vole 正在使用，不能卸载自己", "大小 ↓")
	h.snapshot("02-list")
	if got := strings.Join(h.order(), ","); got != "Xcode,Google Chrome,Docker,百度网盘,Task Manager TMOG,Vole" {
		t.Fatalf("默认按大小降序：%s", got)
	}
	h.click("按名称排序")
	if got := h.order(); got[0] != "Docker" || got[len(got)-1] != "百度网盘" {
		t.Fatalf("按名称升序：%v", got)
	}
	h.click("按名称排序")
	if got := h.order(); got[0] != "百度网盘" {
		t.Fatalf("再点一次降序：%v", got)
	}
	h.click("按最近使用排序")
	if got := h.order(); got[len(got)-1] != "百度网盘" {
		t.Fatalf("最近使用降序：%v", got)
	}
	// 搜索
	h.m.query = "dock"
	h.tt.Frame()
	if got := strings.Join(h.order(), ","); got != "Docker" {
		t.Fatalf("搜索：%s", got)
	}
	h.m.query = "nothing-here"
	h.tt.Frame()
	h.want("没有找到「nothing-here」")
	h.snapshot("03-search-empty")
	h.m.query = ""
	h.tt.Frame()

	// Vole 自己不能勾
	h.click("勾选 Vole")
	if h.m.sel["/Applications/Vole.app"] {
		t.Fatal("Vole 自己不能勾")
	}
}

func TestSelectFilesConfirmRunDone(t *testing.T) {
	be := fakeData()
	be.runGate = make(chan struct{})
	h := newHarness(t, be)
	h.m.OnActivate()
	h.drain(100 * time.Millisecond)

	// 勾 Docker：找关联文件，缓存默认勾，个人数据默认不勾
	h.click("勾选 Docker")
	h.want("5 个文件 · 2.11 GB", "移除 1 项", "Docker · 1 个 App · 2.11 GB", "取消选择")
	h.click("展开 Docker 的关联文件")
	h.want("~/Library/Containers/com.docker.docker", "个人数据 · 很大，确认后再勾", "79.7 GB", "Vole 补充：内嵌的 Docker Desktop.app",
		"缓存和日志", "系统级 · 需要管理员权限，不处理", "/Library/PrivilegedHelperTools/com.docker.vmnetd")
	h.snapshot("04-files-expanded")
	fs := h.m.files["/Applications/Docker.app"]
	if !fs.sel["/Users/test/Library/Caches/com.docker.docker"] || fs.sel["/Users/test/Library/Containers/com.docker.docker"] {
		t.Fatal("缓存默认勾、个人数据默认不勾")
	}
	h.click("勾选 /Library/PrivilegedHelperTools/com.docker.vmnetd")
	if fs.sel["/Library/PrivilegedHelperTools/com.docker.vmnetd"] {
		t.Fatal("系统级不能勾")
	}
	h.click("勾选 ~/.docker/buildx")
	h.click("展开 Docker 的关联文件") // 收起

	// 再勾 Xcode（要管理员密码）、Task Manager（正在运行）
	h.click("勾选 Xcode")
	h.click("勾选 Task Manager TMOG")
	h.want("移除 3 项", "Xcode 等 · 3 个 App")
	h.snapshot("05-selected")

	// 确认框：先取消，什么都不动
	h.click("移除")
	h.want("移除 3 个 App？", "Docker：app 本体 + 2 个关联文件", "包括 1 项个人数据", "正在运行：会先正常退出",
		"需要管理员权限：系统会弹出密码框", "全部移到废纸篓", "退出并移除")
	h.snapshot("06-confirm")
	h.click("取消")
	if h.m.confirm || len(be.runs) != 0 {
		t.Fatal("取消后不应开始")
	}

	// 确认：清单只含勾选的
	h.click("移除")
	h.click("确认退出并移除")
	h.drain(50 * time.Millisecond)
	if h.m.phase != phaseRunning || len(be.runs) != 1 || !be.quits[0] {
		t.Fatalf("应开始卸载并允许退出：%v %v", h.m.phase, be.quits)
	}
	var docker core.Plan
	for _, p := range be.runs[0] {
		if p.App.Name == "Docker" {
			docker = p
		}
	}
	var picked []string
	for _, f := range docker.Files {
		picked = append(picked, f.Path)
	}
	if got := strings.Join(picked, ","); got != "/Users/test/Library/Caches/com.docker.docker,/Users/test/.docker/buildx" {
		t.Fatalf("只卸勾选的关联文件：%s", got)
	}
	be.runGate <- struct{}{}
	h.drain(100 * time.Millisecond)
	h.want("正在移除", "第 2 / 3 个")
	h.snapshot("07-running")
	be.runGate <- struct{}{}
	be.runGate <- struct{}{}
	h.drain(200 * time.Millisecond)

	// 完成页
	if h.m.phase != phaseDone {
		t.Fatalf("应进入完成页：%v", h.m.phase)
	}
	h.want("已移到废纸篓", "移除了 2 个 App", "1 个 App 没有完全移除", "已取消（没有输入管理员密码）", "打开废纸篓")
	h.snapshot("08-done")
	h.click("打开废纸篓")
	if len(h.opened) != 1 || h.opened[0] != "/Users/test/.Trash" {
		t.Fatalf("打开废纸篓：%v", h.opened)
	}
	lists := be.lists
	h.click("完成")
	h.drain(100 * time.Millisecond)
	if h.m.phase != phaseList || len(h.m.sel) != 0 || be.lists != lists+1 {
		t.Fatal("完成后回到列表并重读")
	}
}

func TestCancelAndStateKept(t *testing.T) {
	be := fakeData()
	be.runGate = make(chan struct{})
	h := newHarness(t, be)
	h.m.OnActivate()
	h.drain(100 * time.Millisecond)
	h.click("勾选 Docker")
	// 切走再回来：勾选还在，不重读
	h.m.OnDeactivate()
	h.m.OnActivate()
	h.tt.Frame()
	if !h.m.sel["/Applications/Docker.app"] || be.lists != 1 {
		t.Fatal("切 tab 回来状态应保留")
	}
	h.click("勾选 百度网盘")
	h.click("移除")
	h.click("确认退出并移除")
	h.drain(50 * time.Millisecond)
	h.click("取消")
	h.want("正在完成当前这个 App…")
	be.runGate <- struct{}{}
	h.drain(200 * time.Millisecond)
	h.want("已取消，剩下 1 个没有处理")
}

func TestLoadError(t *testing.T) {
	be := fakeData()
	be.apps, be.listErr = nil, errors.New("读不了 /Applications")
	h := newHarness(t, be)
	h.m.OnActivate()
	h.drain(100 * time.Millisecond)
	h.want("读不到已安装的应用", "读不了 /Applications", "重新读取")
	h.snapshot("09-error")
	be.apps, be.listErr = fakeData().apps, nil
	h.click("重新读取")
	h.drain(100 * time.Millisecond)
	h.want("6 个 · 11.2 GB")
}

// TestRealListUpToConfirm 用本机真实数据走到列表、展开关联文件、确认框，在确认框点取消（只读，卸载会让测试失败）。
// 只在设置 VOLE_REAL_APPS_UI=1 时运行。
func TestRealListUpToConfirm(t *testing.T) {
	if os.Getenv("VOLE_REAL_APPS_UI") == "" {
		t.Skip("设置 VOLE_REAL_APPS_UI=1 才跑（只读本机）")
	}
	h := newRealHarness(t)
	h.m.OnActivate()
	for i := 0; i < 60 && h.m.phase == phaseLoading; i++ {
		h.drain(500 * time.Millisecond)
	}
	if h.m.phase != phaseList || h.m.loadErr != nil {
		t.Fatalf("列表没读出来：%v", h.m.loadErr)
	}
	h.snapshot("real-01-list")
	h.click("勾选 Docker")
	for i := 0; i < 40 && h.m.filesLoading(); i++ {
		h.drain(500 * time.Millisecond)
	}
	h.click("展开 Docker 的关联文件")
	h.snapshot("real-02-files")
	h.click("移除")
	h.snapshot("real-03-confirm")
	h.click("取消")
	if h.m.confirm {
		t.Fatal("确认框点取消后应关闭")
	}
}

// noRun 包装真实数据源：列表和关联文件照常（只读），卸载一律让测试失败。
type noRun struct {
	Backend
	t *testing.T
}

func (n noRun) Run(context.Context, []core.Plan, bool, func(int, int, core.Outcome)) *core.Report {
	n.t.Fatal("真实数据测试不允许卸载")
	return &core.Report{}
}

func newRealHarness(t *testing.T) *harness {
	h := &harness{t: t, main: make(chan func(), 256)}
	h.m = New(noRun{Backend: NewBackend(), t: t})
	h.m.reveal = func(string) {}
	h.m.openPath = func(string) {}
	h.m.fileIcon = func(string) ([]byte, error) { return nil, errors.New("测试里不取图标") }
	r := task.NewRunner(func(fn func()) { h.main <- fn })
	t.Cleanup(r.Close)
	h.m.Init(host{r})
	th := h.m.Theme()
	h.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().TextColor(th.Text).FontSize(kit.TextBody).
			LinearGradient(ui.LinearGradient{From: th.BgTop, To: th.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
			Children(func() { h.m.View(c) })
	}, 1194, 676)
	return h
}
