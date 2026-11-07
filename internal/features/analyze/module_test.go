package analyze

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/analyze"
	"vole/internal/core/sysenv"
	"vole/internal/task"
)

// fakeSource 按路径返回预设结果；gates 里有的路径会阻塞到 gate 关闭或 ctx 取消。
type fakeSource struct {
	mu       sync.Mutex
	calls    map[string]int
	gates    map[string]chan struct{}
	reports  map[string]core.Report
	canceled map[string]bool
}

func newFake() *fakeSource {
	return &fakeSource{calls: map[string]int{}, gates: map[string]chan struct{}{}, reports: map[string]core.Report{}, canceled: map[string]bool{}}
}

func (f *fakeSource) Scan(ctx context.Context, req core.Request) (core.Report, error) {
	f.mu.Lock()
	f.calls[req.Path]++
	gate := f.gates[req.Path]
	r, ok := f.reports[req.Path]
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			f.mu.Lock()
			f.canceled[req.Path] = true
			f.mu.Unlock()
			return core.Report{}, ctx.Err()
		}
	}
	if !ok {
		return core.Report{}, errors.New("no such fixture")
	}
	return r, nil
}

func (f *fakeSource) count(p string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[p]
}

func (f *fakeSource) wasCanceled(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canceled[p]
}

func (f *fakeSource) gate(p string) chan struct{} {
	ch := make(chan struct{})
	f.mu.Lock()
	f.gates[p] = ch
	f.mu.Unlock()
	return ch
}

// harness 把模块、假主线程队列和 Tester 绑在一起。
type harness struct {
	t    *testing.T
	m    *Module
	src  *fakeSource
	tt   *ui.Tester
	main chan func()
}

const (
	pApps = "/Applications"
	pHome = "/Users/test"
	pLib  = "/Library"
	pBrew = "/opt/homebrew"
)

func dir(name, path string, size int64) core.Entry {
	return core.Entry{Name: name, Path: path, Size: size, IsDir: true, Status: core.StatusComplete}
}

func fixtures(f *fakeSource) {
	f.reports[pApps] = core.Report{Path: pApps, Status: core.StatusComplete, TotalSize: 23e9, Entries: []core.Entry{dir("Xcode.app", pApps+"/Xcode.app", 15e9), dir("Docker.app", pApps+"/Docker.app", 8e9)}}
	f.reports[pHome] = core.Report{Path: pHome, Status: core.StatusPartial, TotalSize: 340e9, Entries: []core.Entry{
		{Name: "Library", Path: pHome + "/Library", Size: 160e9, IsDir: true, Status: core.StatusPartial},
		dir("Code", pHome+"/Code", 88e9),
		{Name: ".yarn", Path: pHome + "/.yarn", Size: 2e9, IsDir: true, Status: core.StatusComplete, Cleanable: true},
		{Name: ".Trash", Path: pHome + "/.Trash", IsDir: true, Status: core.StatusUnavailable},
		{Name: "big.tar.gz", Path: pHome + "/big.tar.gz", Size: 5e9, Status: core.StatusComplete, LastAccess: time.Date(2025, 6, 30, 0, 0, 0, 0, time.UTC)},
	}, LargeFiles: []core.LargeFile{{Name: "Docker.raw", Path: pHome + "/Library/Docker.raw", Size: 80e9}}}
	f.reports[pLib] = core.Report{Path: pLib, Status: core.StatusPartial, TotalSize: 5e9, Entries: []core.Entry{dir("Caches", pLib+"/Caches", 5e9)}}
	f.reports[pBrew] = core.Report{Path: pBrew, Status: core.StatusComplete, TotalSize: 3e9, Entries: []core.Entry{dir("Cellar", pBrew+"/Cellar", 3e9)}}
	f.reports[pHome+"/Code"] = core.Report{Path: pHome + "/Code", Status: core.StatusComplete, TotalSize: 88e9, Entries: []core.Entry{dir("Projects", pHome+"/Code/Projects", 80e9), dir("misc", pHome+"/Code/misc", 8e9)}, LargeFiles: []core.LargeFile{{Name: "pack.pack", Path: pHome + "/Code/Projects/pack.pack", Size: 7e8}}}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, src: newFake(), main: make(chan func(), 64)}
	fixtures(h.src)
	h.m = newModule(h.src, sysenv.Info{FullDiskAccess: sysenv.AccessDenied}, []root{
		{"Applications", pApps}, {"Users/test", pHome}, {"Library", pLib}, {"opt/homebrew", pBrew},
	})
	h.m.diskUsage = func(string) (sysenv.Disk, error) { return sysenv.Disk{Total: 494e9, Free: 46e9}, nil }
	h.m.reveal = func(string) {}
	runner := task.NewRunner(func(fn func()) { h.main <- fn })
	t.Cleanup(runner.Close)
	h.m.Init(testHost{runner})
	// 套上和外壳一样的主题背景，截图才像真窗口
	th := h.m.Theme()
	h.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().TextColor(th.Text).FontSize(14).
			LinearGradient(ui.LinearGradient{From: th.BgTop, To: th.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
			Children(func() { h.m.View(c) })
	}, 1200, 700)
	h.m.now = h.tt.Now
	return h
}

type testHost struct{ r *task.Runner }

func (h testHost) Tasks() *task.Runner { return h.r }

// settle 等 n 个后台结果回到主线程，执行后重画一帧。
func (h *harness) settle(n int) {
	h.t.Helper()
	for i := 0; i < n; i++ {
		select {
		case fn := <-h.main:
			fn()
		case <-time.After(3 * time.Second):
			h.t.Fatalf("等第 %d 个后台结果超时", i+1)
		}
	}
	h.tt.Frame()
}

func (h *harness) click(label string) {
	h.t.Helper()
	if err := h.tt.Click(label); err != nil {
		h.t.Fatalf("点击 %q：%v；当前文字：%q", label, err, h.tt.Texts())
	}
}

func (h *harness) want(texts ...string) {
	h.t.Helper()
	for _, s := range texts {
		if !h.tt.HasText(s) {
			h.t.Fatalf("界面上应有 %q；当前文字：%q", s, h.tt.Texts())
		}
	}
}

// snapshot 在设置了 VOLE_SNAPSHOT_DIR 时把当前画面存成 PNG，供人工看效果。
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

func TestRootScansWithConcurrencyLimit(t *testing.T) {
	h := newHarness(t)
	home := h.src.gate(pHome)
	apps := h.src.gate(pApps)
	h.m.OnActivate()
	h.tt.Frame()

	// 最多同时 2 个：Applications、~ 在跑，Library、homebrew 排队
	if h.m.jobs[pLib].state != jobQueued || h.m.jobs[pBrew].state != jobQueued {
		t.Fatalf("超过并发上限：%+v %+v", h.m.jobs[pLib], h.m.jobs[pBrew])
	}
	h.tt.Advance(2 * time.Second) // 离屏是 CPU 渲染自转星球，别推太久
	h.want("当前 0 B + 4 项计算中", "已用 2s", "排队中")
	h.snapshot("01-root-scanning")

	close(apps) // Applications 完成 → Library 补位
	h.settle(1)
	if h.m.jobs[pLib].state != jobRunning {
		t.Fatal("Applications 完成后 Library 应开始")
	}
	h.settle(1) // Library 完成 → homebrew 补位
	h.settle(1) // homebrew 完成
	h.want("23.0 GB", "≥ 5.00 GB", "3.00 GB", "当前 31.0 GB + 1 项计算中")
	h.snapshot("02-root-partial")

	close(home)
	h.settle(1)
	h.want("4 项，371 GB", "磁盘 448 GB / 494 GB", "未获得完全磁盘访问，部分目录读不到")
	h.snapshot("03-root-done")
}

func TestDrillReusesReportsAndBreadcrumbGoesBack(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.settle(4)

	// 点进 ~：直接用根视图已有的结果，不再扫
	h.click("条目 Users/test")
	if n := h.src.count(pHome); n != 1 {
		t.Fatalf("~ 被重扫了：%d 次", n)
	}
	h.want("5 项，340 GB", "≥ 160 GB", "不完整", "可清理", "无法读取", "访问于 2025-06-30")
	h.snapshot("04-home")

	// 点 treemap 的块下钻 Code：新扫一次
	h.click("块 Code")
	if h.src.count(pHome+"/Code") != 1 {
		t.Fatal("下钻 Code 应扫一次")
	}
	h.settle(1)
	h.want("Projects", "2 项，88.0 GB")

	// 大文件
	h.click("大文件 1")
	h.want("pack.pack")
	h.snapshot("05-large-files")

	// 面包屑回到 ~，再回到根，再进 Code：全部用内存结果，不重扫
	h.click("返回 Users/test")
	h.want("5 项，340 GB")
	h.click("返回 整个磁盘")
	h.want("4 项，371 GB")
	h.click("条目 Users/test")
	h.click("条目 Code")
	if h.src.count(pHome) != 1 || h.src.count(pHome+"/Code") != 1 {
		t.Fatalf("返回/再进入不应重扫：~=%d Code=%d", h.src.count(pHome), h.src.count(pHome+"/Code"))
	}
	if len(h.main) != 0 {
		t.Fatal("不应有新的后台任务")
	}

	// 右键在 Finder 中显示
	var revealed string
	h.m.reveal = func(p string) { revealed = p }
	if err := h.tt.RightClick("条目 Projects"); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("在 Finder 中显示"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if revealed != pHome+"/Code/Projects" {
		t.Fatalf("应在 Finder 中显示 Projects，got %q", revealed)
	}
}

func TestCancelAndRetry(t *testing.T) {
	h := newHarness(t)
	h.m.OnActivate()
	h.settle(4)
	h.click("条目 Users/test")

	// Library 没有预设结果：先挂住，看计算中状态，再取消
	lib := pHome + "/Library"
	h.src.gate(lib)
	h.click("条目 Library")
	h.tt.Advance(3 * time.Second)
	h.want("计算中 · 已用 3s", "计算中…")
	h.snapshot("06-dir-scanning")

	h.click("取消扫描")
	h.want("已取消扫描", "重新扫描")
	deadline := time.Now().Add(3 * time.Second)
	for !h.src.wasCanceled(lib) {
		if time.Now().After(deadline) {
			t.Fatal("取消后 analyze 应收到 ctx 取消")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 被取消的任务回调会在主线程被丢弃：跑完队列后状态仍是已取消
	for len(h.main) > 0 {
		(<-h.main)()
	}
	h.tt.Frame()
	h.want("已取消扫描")

	// 重新扫描：这次没有 gate，fixture 不存在 → 失败提示
	h.src.mu.Lock()
	delete(h.src.gates, lib)
	h.src.mu.Unlock()
	h.click("重新扫描")
	h.settle(1)
	h.want("扫描失败", "no such fixture")

	// 返回上级时，下级正在跑的扫描会被取消
	h.src.mu.Lock()
	h.src.canceled = map[string]bool{}
	h.src.mu.Unlock()
	h.src.gate(lib)
	h.click("重新扫描")
	h.click("返回 Users/test")
	deadline = time.Now().Add(3 * time.Second)
	for h.src.count(lib) < 3 || !h.src.wasCanceled(lib) {
		if time.Now().After(deadline) {
			t.Fatal("离开目录后其扫描应被取消")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// brokenSource 模拟 bundle 里的 analyze 缺失：所有扫描都返回 ErrScannerUnavailable。
type brokenSource struct{}

func (brokenSource) Scan(context.Context, core.Request) (core.Report, error) {
	return core.Report{}, errors.Join(core.ErrScannerUnavailable, errors.New("找不到 analyze 二进制"))
}

func TestScannerUnavailableShowsErrorState(t *testing.T) {
	h := newHarness(t)
	h.m.src = brokenSource{}
	h.m.OnActivate()
	h.settle(4)
	h.want("分析组件不可用", "重新扫描")
	h.snapshot("07-broken")
	// 重试仍失败也不崩
	h.click("重新扫描")
	h.settle(4)
	h.want("分析组件不可用")
}
