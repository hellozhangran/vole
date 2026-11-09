// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	core "vole/internal/core/clean"
	"vole/internal/task"
)

// fakeBackend 是假的清理数据源：扫描返回固定结果，清理只记录清单、按 gate 一项一项「完成」，绝不碰磁盘。
type fakeBackend struct {
	mu       sync.Mutex
	res      *core.ScanResult
	scanGate chan struct{}
	runGate  chan struct{}
	runs     [][]core.Item
	runCtxs  []context.Context
	fail     string // 这个项 ID 返回失败
}

func (f *fakeBackend) Scan(ctx context.Context, progress func(string, bool)) (*core.ScanResult, error) {
	progress("开发者缓存", false)
	progress("浏览器缓存", false)
	if f.scanGate != nil {
		select {
		case <-f.scanGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	progress("开发者缓存", true)
	progress("浏览器缓存", true)
	return f.res, nil
}

func (f *fakeBackend) Run(ctx context.Context, items []core.Item, progress func(int, int, core.Outcome)) *core.RunReport {
	f.mu.Lock()
	f.runs = append(f.runs, items)
	f.runCtxs = append(f.runCtxs, ctx)
	f.mu.Unlock()
	rep := &core.RunReport{}
	for i, it := range items {
		if ctx.Err() != nil {
			for _, rest := range items[i:] {
				rep.Outcomes = append(rep.Outcomes, core.Outcome{Item: rest, Status: core.StatusCanceled, Reason: "用户取消"})
			}
			rep.Canceled = true
			break
		}
		if f.runGate != nil {
			<-f.runGate // 当前这一项一旦开始就做完
		}
		o := core.Outcome{Item: it, Status: core.StatusDone, Freed: it.Size}
		if it.ID == f.fail {
			o = core.Outcome{Item: it, Status: core.StatusFailed, Reason: "2 个子项删除失败：没有权限"}
		}
		rep.Outcomes = append(rep.Outcomes, o)
		rep.Freed += o.Freed
		progress(i+1, len(items), o)
	}
	return rep
}

func (f *fakeBackend) LastClean() (time.Time, int64, bool) {
	return time.Date(2026, 11, 7, 21, 40, 0, 0, time.Local), 12_200_000_000, true
}
func (f *fakeBackend) LogDir() string { return "/Users/test/Library/Logs/Vole" }

func item(cat, id, title string, size int64, def bool, blocked string, cmd ...string) core.Item {
	rule, path, _ := strings.Cut(id, ":")
	return core.Item{ID: id, RuleID: rule, Category: cat, Title: title, Path: strings.Replace(path, "~", "/Users/test", 1),
		Size: size, Default: def, Blocked: blocked, Command: cmd, Note: "测试说明"}
}

func fakeResult() *core.ScanResult {
	cats := []core.CategoryResult{
		{ID: core.CatDev, Title: "开发者缓存", Items: []core.Item{
			item(core.CatDev, "dev.uv:~/.cache/uv", "uv 缓存", 27_500_000_000, true, "uv 正在运行", "/opt/homebrew/bin/uv", "cache", "prune"),
			item(core.CatDev, "dev.uv.all:~/.cache/uv", "uv 全部缓存", 1_000_000, false, "被 uv run 占用：my-mcp-server（PID 69222、69223）", "/usr/bin/env", "UV_LOCK_TIMEOUT=3", "/opt/homebrew/bin/uv", "cache", "clean"),
			item(core.CatDev, "dev.gomod:~/go/pkg/mod", "Go module 缓存", 5_400_000_000, false, "", "/usr/local/go/bin/go", "clean", "-modcache"),
			item(core.CatDev, "dev.gobuild:~/Library/Caches/go-build", "Go 编译缓存", 4_800_000_000, true, "", "/usr/local/go/bin/go", "clean", "-cache"),
			item(core.CatDev, "dev.cocoapods:~/Library/Caches/CocoaPods", "CocoaPods 缓存", 3_600_000_000, true, ""),
			item(core.CatDev, "dev.npm:~/.npm/_cacache", "npm 缓存", 72_000_000, true, "", "/usr/local/bin/npm", "cache", "clean", "--force"),
		}},
		{ID: core.CatXcode, Title: "Xcode 与模拟器", Items: []core.Item{
			item(core.CatXcode, "xcode.simcaches:~/Library/Developer/CoreSimulator/Caches", "模拟器缓存", 3_500_000_000, true, ""),
			item(core.CatXcode, "xcode.devicesupport:~/Library/Developer/Xcode/iOS DeviceSupport/16.0", "16.0", 4_600_000_000, false, ""),
		}},
		{ID: core.CatCaches, Title: "App 缓存", Items: []core.Item{
			item(core.CatCaches, "caches.apps:~/Library/Caches/ms-playwright", "ms-playwright", 2_800_000_000, true, "在白名单里：默认不清理（删了要重新下载或重建）"),
			item(core.CatCaches, "caches.apps:~/Library/Caches/puccinialin", "puccinialin", 489_000_000, false, ""),
			item(core.CatCaches, "caches.apps:~/Library/Caches/com.youdao.YoudaoDict", "com.youdao.YoudaoDict", 450_000_000, true, "网易有道翻译 正在运行"),
			item(core.CatCaches, "caches.apps:~/Library/Caches/dev.soulbot.app", "dev.soulbot.app", 6_300_000, true, ""),
			item(core.CatCaches, "caches.apps:~/Library/Caches/com.apple.familycircled", "familycircled", 1_000_000, true, core.SystemBusy+"familycircled 正在使用"),
		}},
		{ID: core.CatBrowser, Title: "浏览器缓存", Items: []core.Item{
			item(core.CatBrowser, "browser.chrome:~/Library/Caches/Google/Chrome", "Chrome 缓存", 1_900_000_000, true, "Google Chrome 正在运行"),
			item(core.CatBrowser, "browser.safari:~/Library/Caches/com.apple.Safari", "Safari 缓存", 0, true, "需要完全磁盘访问"),
		}},
		{ID: core.CatLogs, Title: "日志", Items: []core.Item{
			item(core.CatLogs, "logs.user:~/Library/Logs/app.mangodisk.desktop", "app.mangodisk.desktop", 3_900_000, true, ""),
			item(core.CatLogs, "logs.user:~/Library/Logs/DiagnosticReports", "DiagnosticReports", 168_000, true, ""),
		}},
		{ID: core.CatTrash, Title: "废纸篓", Items: []core.Item{
			item(core.CatTrash, "trash.empty:~/.Trash", "清空废纸篓", 830_000_000, false, ""),
		}},
		{ID: core.CatAI, Title: "AI 工具", Items: []core.Item{
			item(core.CatAI, "ai.claude.web:~/Library/Application Support/Claude/Cache", "Cache", 156_000_000, false, ""),
			item(core.CatAI, "ai.claude.web:~/Library/Application Support/Claude/Code Cache", "Code Cache", 106_000_000, false, ""),
			item(core.CatAI, "ai.opencode:~/.cache/opencode", "OpenCode 缓存", 4_000, false, ""),
		}},
	}
	for i := range cats[6].Items[:2] {
		cats[6].Items[i].Group = "Claude"
	}
	cats[2].Items[1].Uncertain = true   // puccinialin：判断不了归属
	cats[3].Items[1].SizeUnknown = true // Safari：量不出来
	for i := range cats {
		for _, it := range cats[i].Items {
			cats[i].Size += it.Size // 总大小含不能勾选的项
		}
	}
	return &core.ScanResult{Categories: cats}
}

type harness struct {
	t        *testing.T
	m        *Module
	be       *fakeBackend
	tt       *ui.Tester
	main     chan func()
	revealed []string
	opened   []string
	settings int
}

type host struct{ r *task.Runner }

func (h host) Tasks() *task.Runner { return h.r }

func newHarness(t *testing.T, be Backend) *harness {
	t.Helper()
	h := &harness{t: t, main: make(chan func(), 256)}
	if fb, ok := be.(*fakeBackend); ok {
		h.be = fb
	}
	h.m = New(be)
	h.m.reveal = func(p string) { h.revealed = append(h.revealed, p) }
	h.m.openPath = func(p string) { h.opened = append(h.opened, p) }
	h.m.openSettings = func() { h.settings++ }
	r := task.NewRunner(func(fn func()) { h.main <- fn })
	t.Cleanup(r.Close)
	h.m.Init(host{r})
	h.m.OnActivate()
	th := h.m.Theme()
	h.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().TextColor(th.Text).FontSize(13).
			LinearGradient(ui.LinearGradient{From: th.BgTop, To: th.BgBottom, Angle: 180, End: 0.7, Oklab: true}).
			Children(func() { h.m.View(c) })
	}, 1194, 762)
	return h
}

// drain 处理主线程队列，直到 wait 时间内没有新回调。
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

func (h *harness) runs() [][]core.Item {
	h.be.mu.Lock()
	defer h.be.mu.Unlock()
	return h.be.runs
}

func ids(items []core.Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return strings.Join(out, ",")
}

func TestCleanFlow(t *testing.T) {
	be := &fakeBackend{res: fakeResult(), scanGate: make(chan struct{}), runGate: make(chan struct{}), fail: "logs.user:~/Library/Logs/DiagnosticReports"}
	h := newHarness(t, be)

	// 首屏
	h.tt.Frame()
	h.want("翻一翻土，给磁盘透透气", "扫描", "上次清理：11月7日 21:40，释放 12.2 GB")
	if h.m.FullPage() {
		t.Fatal("首屏要显示顶部 tab 条")
	}
	h.snapshot("01-home")

	// 扫描中
	h.click("扫描")
	h.drain(100 * time.Millisecond)
	h.want("扫描中", "正在查找开发者缓存、浏览器缓存", "取消")
	h.snapshot("02-scanning")
	close(be.scanGate)
	h.drain(200 * time.Millisecond)

	// 结果：整页、默认勾选、被跳过的汇总
	if h.m.phase != phaseResults || !h.m.FullPage() {
		t.Fatalf("应进入结果页（整页）：%v", h.m.phase)
	}
	h.want("准备开始清理", "关闭 Google Chrome、uv、uv run（my-mcp-server）、网易有道翻译 后可再清理 29.9 GB · 4 项；1 项需要完全磁盘访问",
		"已选 7 项", "已选 3/6", "已选 1/5", "已选 0/2", "已选 0/1", "永久清理 · 12.0 GB",
		"6.30 MB / 3.75 GB") // App 缓存：总大小含被挡住的项
	if strings.Contains(strings.Join(h.tt.Texts(), "|"), "familycircled 正在") {
		t.Fatal("系统进程不该出现在「关闭…后可再清理」里")
	}
	if strings.Contains(strings.Join(h.tt.Texts(), "|"), "0/0") {
		t.Fatal("不应出现 0/0")
	}
	h.click("展开 开发者缓存")
	h.want("需确认", "由 go 清理", "uv 正在运行", "~/go/pkg/mod", "被 uv run 占用：my-mcp-server（PID 69222、69223）")
	h.click("展开 开发者缓存") // 收起
	h.click("展开 App 缓存")
	h.click("展开 浏览器缓存")
	h.want("需确认", "Google Chrome 正在运行", "需要完全磁盘访问", "在白名单里", "网易有道翻译 正在运行", "—")
	h.snapshot("03-results")
	h.click("展开 App 缓存") // 收起，让浏览器缓存那一行露出来
	h.click("去设置")
	h.click("去设置开启完全磁盘访问")
	if h.settings != 2 {
		t.Fatalf("去设置：%d", h.settings)
	}
	// 按 app 分组：一组一行（N 项），展开后逐项；组的勾选框管整组
	h.click("展开 浏览器缓存") // 收起
	h.click("展开 AI 工具")
	h.tt.SetSize(1194, 1100) // 窗口拉高，AI 工具的行露出来
	h.tt.Frame()
	h.want("Claude", "2 项", "262 MB", "OpenCode 缓存")
	h.click("勾选 Claude")
	if !h.m.sel["ai.claude.web:~/Library/Application Support/Claude/Cache"] || !h.m.sel["ai.claude.web:~/Library/Application Support/Claude/Code Cache"] {
		t.Fatal("组的勾选框应勾上整组")
	}
	h.click("展开 Claude")
	h.want("Code Cache", "~/Library/Application Support/Claude/Code Cache")
	h.snapshot("03c-results-groups")
	h.click("勾选 Claude")
	h.click("展开 AI 工具") // 收起，恢复默认勾选的状态
	h.tt.SetSize(1194, 762)
	h.tt.Frame()
	h.click("展开 App 缓存")
	h.want("系统占用")
	h.click("展开 App 缓存")
	h.click("展开 浏览器缓存")
	h.snapshot("03b-results-browser")

	// 在 Finder 中显示
	h.click("在 Finder 中显示")
	if len(h.revealed) != 1 || h.revealed[0] != "/Users/test/Library/Caches/Google/Chrome" {
		t.Fatalf("Finder：%v", h.revealed)
	}

	// 确认框：先取消，什么都不删
	h.click("永久清理")
	h.want("永久清理 7 项？", "将释放约 12.0 GB", "永久删除 7 项（12.0 GB）：都是可以重新生成的缓存，不进废纸篓，删除后无法恢复。",
		"移到废纸篓 0 项（0 B）。", "其中 2 项交给 go、npm 自己清理，实际释放可能少于显示。")
	h.snapshot("04-confirm")
	h.click("取消")
	if h.m.confirm || len(h.runs()) != 0 {
		t.Fatal("取消后不应开始清理")
	}

	// 再确认：只清勾选的项
	h.click("永久清理")
	h.click("确认永久清理")
	h.drain(50 * time.Millisecond)
	if h.m.phase != phaseCleaning {
		t.Fatal("应进入清理中")
	}
	want := "dev.gobuild:~/Library/Caches/go-build,dev.cocoapods:~/Library/Caches/CocoaPods,dev.npm:~/.npm/_cacache," +
		"xcode.simcaches:~/Library/Developer/CoreSimulator/Caches,caches.apps:~/Library/Caches/dev.soulbot.app," +
		"logs.user:~/Library/Logs/app.mangodisk.desktop,logs.user:~/Library/Logs/DiagnosticReports"
	runs := h.runs()
	if len(runs) != 1 || ids(runs[0]) != want {
		t.Fatalf("清单不对：%s", ids(runs[0]))
	}
	be.runGate <- struct{}{}
	be.runGate <- struct{}{}
	h.drain(100 * time.Millisecond)
	h.want("清理中", "第 3 / 7 项 · 已释放 8.40 GB", "~/.npm/_cacache", "取消")
	h.snapshot("05-cleaning")
	for i := 0; i < 5; i++ {
		be.runGate <- struct{}{}
	}
	h.drain(200 * time.Millisecond)

	// 完成页
	if h.m.phase != phaseDone {
		t.Fatalf("应进入完成页：%v", h.m.phase)
	}
	h.want("已释放 12.0 GB", "清理了 6 项", "1 项没有清理", "失败", "2 个子项删除失败：没有权限", "查看日志", "完成")
	h.snapshot("06-done")
	h.click("查看日志")
	if len(h.opened) != 1 || h.opened[0] != "/Users/test/Library/Logs/Vole" {
		t.Fatalf("查看日志：%v", h.opened)
	}
	h.click("完成")
	if h.m.phase != phaseHome || h.m.FullPage() {
		t.Fatal("应回到首屏")
	}
}

func TestSelectionControls(t *testing.T) {
	be := &fakeBackend{res: fakeResult()}
	h := newHarness(t, be)
	h.click("扫描")
	h.drain(100 * time.Millisecond)
	count := func() int { return len(h.m.selected()) }
	if count() != 7 {
		t.Fatalf("默认勾选 7 项：%d", count())
	}
	h.click("全选")
	if count() != 14 { // 能勾的全勾上：21 项里 7 项被挡
		t.Fatalf("全选：%d", count())
	}
	for _, it := range h.m.selected() {
		if it.Blocked != "" {
			t.Fatalf("被挡住的项不能选：%s", it.ID)
		}
	}
	h.click("清空")
	if count() != 0 {
		t.Fatal("清空")
	}
	h.want("永久清理 · 0 B")
	h.click("永久清理") // 什么都没选时按钮不可用
	if h.m.confirm {
		t.Fatal("没选时不应弹确认框")
	}
	h.click("推荐")
	if count() != 7 || h.m.sel["dev.gomod:~/go/pkg/mod"] || h.m.sel["trash.empty:~/.Trash"] {
		t.Fatal("推荐 = 恢复默认分档：Go module 缓存和废纸篓不勾")
	}

	// 三态：开发者缓存部分勾 → 点一下全勾 → 再点全不勾
	dev := h.m.res.Categories[0]
	if h.m.categoryState(dev) != 2 { // Mixed
		t.Fatalf("开发者缓存应部分勾选：%v", h.m.categoryState(dev))
	}
	h.click("勾选 开发者缓存")
	if !h.m.sel["dev.gomod:~/go/pkg/mod"] || h.m.sel["dev.uv:~/.cache/uv"] {
		t.Fatal("类别全选：能勾的都勾上，被挡住的不勾")
	}
	h.click("勾选 开发者缓存")
	if h.m.sel["dev.gobuild:~/Library/Caches/go-build"] {
		t.Fatal("再点一次应全部取消")
	}
	// 逐项勾选
	h.click("展开 废纸篓")
	h.click("勾选 清空废纸篓")
	if !h.m.sel["trash.empty:~/.Trash"] {
		t.Fatal("逐项勾选")
	}
	h.click("永久清理")
	h.want("包括清空废纸篓：里面的文件会被永久删除。")
}

func TestCancelRunStopsAfterCurrentItem(t *testing.T) {
	be := &fakeBackend{res: fakeResult(), runGate: make(chan struct{})}
	h := newHarness(t, be)
	h.click("扫描")
	h.drain(100 * time.Millisecond)
	h.click("永久清理")
	h.click("确认永久清理")
	h.drain(50 * time.Millisecond)
	h.click("取消")
	h.want("正在完成当前这一项…")
	be.runGate <- struct{}{} // 当前这一项做完
	h.drain(200 * time.Millisecond)
	if h.m.phase != phaseDone {
		t.Fatalf("取消后应进入完成页：%v", h.m.phase)
	}
	if ctx := be.runCtxs[0]; !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("清理应收到取消")
	}
	h.want("清理了 1 项；已取消，剩下 6 项没有处理", "已取消")
	h.snapshot("07-done-canceled")
}

func TestStateKeptAcrossTabsAndClose(t *testing.T) {
	be := &fakeBackend{res: fakeResult()}
	h := newHarness(t, be)
	h.click("扫描")
	h.drain(100 * time.Millisecond)
	h.click("清空")
	h.m.OnDeactivate()
	h.m.OnActivate()
	h.tt.Frame()
	if h.m.phase != phaseResults || len(h.m.selected()) != 0 {
		t.Fatal("切 tab 回来结果和勾选应保留")
	}
	h.click("关闭，回到首屏")
	if h.m.phase != phaseHome || h.m.res != nil {
		t.Fatal("✕ 应回到首屏")
	}
	// 扫描中取消也回首屏
	be.scanGate = make(chan struct{})
	h.click("扫描")
	h.drain(50 * time.Millisecond)
	h.click("取消")
	if h.m.phase != phaseHome {
		t.Fatal("取消扫描应回到首屏")
	}
}

// TestRealScanUpToConfirm 用本机真实数据走到结果页和确认框，在确认框点取消（只读扫描，删除会让测试失败）。
// 只在设置 VOLE_REAL_CLEAN_UI=1 时运行。
func TestRealScanUpToConfirm(t *testing.T) {
	if os.Getenv("VOLE_REAL_CLEAN_UI") == "" {
		t.Skip("设置 VOLE_REAL_CLEAN_UI=1 才跑（只读扫描本机）")
	}
	be := noRun{t: t, Backend: NewBackend()}
	h := newHarness(t, be)
	h.tt.Frame()
	h.snapshot("real-01-home")
	h.click("扫描")
	deadline := time.Now().Add(3 * time.Minute)
	for h.m.phase != phaseResults && time.Now().Before(deadline) {
		h.drain(500 * time.Millisecond)
	}
	if h.m.phase != phaseResults {
		t.Fatalf("扫描没完成：%v %v", h.m.phase, h.m.scanErr)
	}
	for _, c := range h.m.res.Categories {
		t.Logf("%s：%d 项，%s，耗时 %s %s", c.Title, len(c.Items), kitSize(c.Size), c.Elapsed.Round(time.Second), c.Note)
	}
	h.click("展开 开发者缓存")
	h.click("展开 App 缓存")
	h.snapshot("real-02-results")
	h.click("永久清理")
	h.snapshot("real-03-confirm")
	h.click("取消")
	if h.m.confirm || h.m.phase != phaseResults {
		t.Fatal("确认框点取消后应回到结果页")
	}
}

func kitSize(n int64) string { return fmt.Sprintf("%.2f GB", float64(n)/1e9) }

// noRun 包装真实数据源：扫描照常（只读），清理一律让测试失败，确保不会在本机删东西。
type noRun struct {
	Backend
	t *testing.T
}

func (n noRun) Run(context.Context, []core.Item, func(int, int, core.Outcome)) *core.RunReport {
	n.t.Fatal("真实数据测试不允许清理")
	return &core.RunReport{}
}

// TestArtScenes：首屏「拿铲子」、完成页和「没有可清理的内容」用「灯泡」插画；设了 VOLE_SNAPSHOT_DIR 时截图。
func TestArtScenes(t *testing.T) {
	be := &fakeBackend{res: fakeResult()}
	h := newHarness(t, be)
	h.tt.Frame()
	h.want("翻一翻土，给磁盘透透气", "扫描")
	h.snapshot("clean-home")
	h.click("扫描")
	h.drain(100 * time.Millisecond)
	h.click("永久清理")
	h.click("确认永久清理")
	h.drain(300 * time.Millisecond)
	h.want("已释放 12.0 GB", "清理了 7 项")
	h.snapshot("clean-done")

	empty := &fakeBackend{res: &core.ScanResult{}}
	h2 := newHarness(t, empty)
	h2.click("扫描")
	h2.drain(100 * time.Millisecond)
	h2.want("没有发现可以清理的内容")
	h2.snapshot("clean-empty")
}
