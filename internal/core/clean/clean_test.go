// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"vole/internal/core/fsops"
)

// 所有测试都在临时目录里造一个假的家目录；Env 的每一项都指向它或假实现，绝不碰本机真实目录。

type fakeEnv struct {
	*Env
	procs   *ProcSnapshot
	procErr error
	tools   map[string]string // 工具名 → 假路径
	ran     [][]string        // 记录调用过的命令
	runFn   func(name string, args []string) ([]byte, error)
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real, _ := os.UserHomeDir()
	if real != "" && (home == real || strings.HasPrefix(real, home) || strings.HasPrefix(home, real+"/Library") && !strings.Contains(home, "/T/")) {
		t.Fatalf("测试家目录不安全：%s", home)
	}
	f := &fakeEnv{procs: &ProcSnapshot{Names: map[string]bool{}, BundleIDs: map[string]string{}}, tools: map[string]string{}}
	f.Env = &Env{
		Home:   home,
		LogDir: filepath.Join(t.TempDir(), "logs"),
		Now:    time.Now,
		LookPath: func(name string) (string, error) {
			if p, ok := f.tools[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			f.ran = append(f.ran, append([]string{name}, args...))
			if f.runFn != nil {
				return f.runFn(name, args)
			}
			return nil, errors.New("测试里没有这个命令")
		},
		Processes: func(context.Context) (*ProcSnapshot, error) { return f.procs, f.procErr },
	}
	return f
}

// file 在假家目录里写一个文件（size 字节），自动建上级目录。
func (f *fakeEnv) file(t *testing.T, rel string, size int) string {
	t.Helper()
	p := filepath.Join(f.Home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func scanAll(t *testing.T, f *fakeEnv) *ScanResult {
	t.Helper()
	res, err := Scan(context.Background(), f.Env)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func find(res *ScanResult, id string) (Item, bool) {
	for _, it := range res.Items() {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

func mustFind(t *testing.T, res *ScanResult, id string) Item {
	t.Helper()
	it, ok := find(res, id)
	if !ok {
		var ids []string
		for _, it := range res.Items() {
			ids = append(ids, it.ID)
		}
		t.Fatalf("应有 %s；实际：%v", id, ids)
	}
	return it
}

// 造一棵覆盖 MVP 各类别的假缓存树。
func (f *fakeEnv) tree(t *testing.T) {
	t.Helper()
	f.file(t, ".cache/uv/archive/a.whl", 300_000)
	f.file(t, ".npm/_cacache/content/x", 200_000)
	f.file(t, "Library/Caches/go-build/aa/bb", 150_000)
	f.file(t, "Library/Caches/CocoaPods/Pods/spec", 50_000)
	f.file(t, "go/pkg/mod/cache/x", 50_000) // 没装 go：不该出现
	f.file(t, "Library/Developer/Xcode/DerivedData/App-abc/Build/x", 120_000)
	f.file(t, "Library/Caches/com.apple.dt.Xcode/x", 10_000)
	f.file(t, "Library/Caches/com.foo.app/data/blob", 80_000)
	f.file(t, "Library/Caches/com.running.app/blob", 40_000)
	f.file(t, "Library/Caches/com.apple.Something/blob", 40_000) // 系统缓存：受保护
	f.file(t, "Library/Caches/com.1password.1password/blob", 40_000)
	f.file(t, "Library/Caches/ms-playwright/chromium/x", 40_000)      // 默认白名单
	f.file(t, "Library/Caches/plaindir/blob", 40_000)                 // 名字不像 bundle id：列出来、需确认
	f.file(t, "Library/Caches/WPS_Office_7.3(8966)_arm64.7z", 40_000) // 文件也一样
	f.file(t, "Library/Caches/gopls/x", 40_000)                       // 和运行中的进程同名：挡住
	f.file(t, "Library/Caches/.hidden/x", 40_000)                     // 点开头：不碰
	f.file(t, "Library/Caches/GeoServices/tile", 40_000)              // 归系统缓存
	f.file(t, "Library/Application Support/Slack/Cache/x", 40_000)    // 归通讯工具
	f.file(t, "Library/Application Support/Foo/Code Cache/x", 40_000) // Electron 缓存，Foo 判断不了：需确认
	f.file(t, "Library/Application Support/Foo/Local Storage/x", 40_000)
	f.file(t, "Library/Caches/Google/Chrome/Default/Cache/x", 60_000)
	f.file(t, "Library/Logs/app.log", 20_000)
	f.file(t, "Library/Logs/SomeApp/run.log", 20_000)
	f.file(t, "Library/Logs/com.apple.xpc.launchd/x.log", 20_000) // 系统日志：受保护
	f.file(t, "Library/Logs/Vole/operations.log", 20_000)         // Vole 自己的日志：受保护
	f.file(t, ".Trash/old.zip", 30_000)
	for i, v := range []string{"16.0", "17.0", "18.0"} {
		p := f.file(t, "Library/Developer/Xcode/iOS DeviceSupport/"+v+"/Symbols/x", 70_000)
		at := time.Now().Add(time.Duration(i-3) * 24 * time.Hour)
		_ = os.Chtimes(filepath.Dir(filepath.Dir(p)), at, at)
	}
}

func TestScanFindsMVPCategories(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	f.procs.Names["Google Chrome"] = true
	f.procs.Names["gopls"] = true
	f.procs.BundleIDs["com.running.app"] = "/Applications/Running.app"
	res := scanAll(t, f)

	if len(res.Categories) != 9 {
		t.Fatalf("应有 9 个类别：%d", len(res.Categories))
	}
	for _, id := range []string{"dev.uv:~/.cache/uv", "dev.npm:~/.npm/_cacache", "dev.gobuild:~/Library/Caches/go-build",
		"dev.cocoapods:~/Library/Caches/CocoaPods", "xcode.derived:~/Library/Developer/Xcode/DerivedData",
		"xcode.cache:~/Library/Caches/com.apple.dt.Xcode", "caches.apps:~/Library/Caches/com.foo.app",
		"logs.user:~/Library/Logs/app.log", "logs.user:~/Library/Logs/SomeApp", "trash.empty:~/.Trash"} {
		it := mustFind(t, res, id)
		if it.Size <= 0 {
			t.Fatalf("%s 大小应 > 0", id)
		}
	}
	for _, id := range []string{"dev.gomod:~/go/pkg/mod", "caches.apps:~/Library/Caches/com.apple.Something",
		"caches.apps:~/Library/Caches/.hidden", "caches.apps:~/Library/Caches/GeoServices",
		"caches.appsupport:~/Library/Application Support/Slack/Cache", "caches.appsupport:~/Library/Application Support/Foo/Local Storage",
		"logs.user:~/Library/Logs/com.apple.xpc.launchd", "logs.user:~/Library/Logs/Vole"} {
		if _, ok := find(res, id); ok {
			t.Fatalf("不该出现 %s", id)
		}
	}
	// 默认勾选分档
	if mustFind(t, res, "trash.empty:~/.Trash").Default || !mustFind(t, res, "dev.uv:~/.cache/uv").Default {
		t.Fatal("废纸篓默认不勾、uv 默认勾")
	}
	// DeviceSupport 保留最新 2 个，只列出最旧的，默认不勾
	ds := mustFind(t, res, "xcode.devicesupport:~/Library/Developer/Xcode/iOS DeviceSupport/16.0")
	if ds.Default || ds.Target != Whole {
		t.Fatalf("DeviceSupport：%+v", ds)
	}
	if _, ok := find(res, "xcode.devicesupport:~/Library/Developer/Xcode/iOS DeviceSupport/18.0"); ok {
		t.Fatal("最新的版本应保留")
	}
	// 运行中的 app：列出来但不能勾
	if b := mustFind(t, res, "browser.chrome:~/Library/Caches/Google/Chrome").Blocked; b != "Google Chrome 正在运行" {
		t.Fatalf("Chrome 应被挡：%q", b)
	}
	if b := mustFind(t, res, "caches.apps:~/Library/Caches/com.running.app").Blocked; b != "Running 正在运行" {
		t.Fatalf("运行中的 app 缓存应被挡，并写 app 名：%q", b)
	}
	if b := mustFind(t, res, "caches.apps:~/Library/Caches/gopls").Blocked; b != "gopls 正在运行" {
		t.Fatalf("和运行中进程同名的缓存应被挡：%q", b)
	}
	// 对不上运行中进程的：和 Mole 一样当作没在运行，默认勾
	for _, id := range []string{"caches.apps:~/Library/Caches/plaindir", "caches.apps:~/Library/Caches/WPS_Office_7.3(8966)_arm64.7z",
		"caches.appsupport:~/Library/Application Support/Foo/Code Cache"} {
		it := mustFind(t, res, id)
		if it.Uncertain || !it.Default || it.Blocked != "" {
			t.Fatalf("%s 应默认勾选：%+v", id, it)
		}
	}
	if !mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app").Default {
		t.Fatal("没在运行的 bundle id 缓存默认勾")
	}
	// 保护名单、白名单里的第三方缓存：列出来、置灰、写明原因
	if b := mustFind(t, res, "caches.apps:~/Library/Caches/com.1password.1password").Blocked; !strings.HasPrefix(b, "受保护") {
		t.Fatalf("保护名单：%q", b)
	}
	if b := mustFind(t, res, "caches.apps:~/Library/Caches/ms-playwright").Blocked; b == "" {
		t.Fatal("ms-playwright 在保护名单 / 白名单里，应置灰")
	}
	// 其他类别
	mustFind(t, res, "system.caches:~/Library/Caches/GeoServices")
	if it := mustFind(t, res, "comms.appsupport:~/Library/Application Support/Slack/Cache"); it.Default {
		t.Fatal("通讯工具默认不勾")
	}
	// 类别总大小含被挡住的项
	for _, c := range res.Categories {
		var sum int64
		for _, it := range c.Items {
			sum += it.Size
		}
		if c.Size != sum {
			t.Fatalf("%s 总大小应含全部项：%d != %d", c.ID, c.Size, sum)
		}
	}
}

func TestScanAndRunNeverTouchOtherFiles(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	res := scanAll(t, f)
	keep := f.file(t, "Documents/important.txt", 10)

	pick := []Item{mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app"), mustFind(t, res, "logs.user:~/Library/Logs/app.log"),
		mustFind(t, res, "xcode.devicesupport:~/Library/Developer/Xcode/iOS DeviceSupport/16.0")}

	// dry-run：全部检查都走一遍，但什么都不删
	dry := Run(context.Background(), f.Env, pick, RunOptions{DryRun: true})
	for _, o := range dry.Outcomes {
		if o.Status != StatusWould {
			t.Fatalf("dry-run 应为将清理：%+v", o)
		}
	}
	if !exists(filepath.Join(f.Home, "Library/Caches/com.foo.app/data/blob")) || !exists(filepath.Join(f.Home, "Library/Logs/app.log")) {
		t.Fatal("dry-run 删了东西")
	}

	rep := Run(context.Background(), f.Env, pick, RunOptions{})
	for _, o := range rep.Outcomes {
		if o.Status != StatusDone {
			t.Fatalf("应已清理：%+v", o)
		}
	}
	if rep.Freed <= 0 {
		t.Fatal("释放量应 > 0")
	}
	// 删内容、留目录
	if !exists(filepath.Join(f.Home, "Library/Caches/com.foo.app")) || exists(filepath.Join(f.Home, "Library/Caches/com.foo.app/data")) {
		t.Fatal("应删内容、保留目录")
	}
	if exists(filepath.Join(f.Home, "Library/Developer/Xcode/iOS DeviceSupport/16.0")) {
		t.Fatal("旧版本整个删掉")
	}
	// 没勾的都还在
	for _, rel := range []string{".cache/uv/archive/a.whl", "Library/Caches/com.running.app/blob", "Library/Caches/com.apple.Something/blob",
		"Library/Logs/SomeApp/run.log", ".Trash/old.zip", "Library/Developer/Xcode/iOS DeviceSupport/17.0/Symbols/x"} {
		if !exists(filepath.Join(f.Home, rel)) {
			t.Fatalf("没勾的 %s 被删了", rel)
		}
	}
	if !exists(keep) {
		t.Fatal("清单之外的文件被删了")
	}
	// 操作日志
	data, err := os.ReadFile(filepath.Join(f.LogDir, "operations.log"))
	if err != nil || strings.Count(string(data), "\t已清理\t") != 3 || !strings.Contains(string(data), "\t将清理\t") {
		t.Fatalf("操作日志不对：%v\n%s", err, data)
	}
}

func TestSymlinksNeverFollowed(t *testing.T) {
	f := newFakeEnv(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(secret, make([]byte, 5000), 0o644)

	// 1. 缓存目录本身是指向外部的 symlink：不列出
	_ = os.MkdirAll(filepath.Join(f.Home, "Library/Caches"), 0o755)
	_ = os.Symlink(outside, filepath.Join(f.Home, "Library/Caches/com.evil.app"))
	// 2. 缓存目录里的子项是指向外部的 symlink：只删链接本身
	f.file(t, "Library/Caches/com.foo.app/blob", 10_000)
	_ = os.Symlink(outside, filepath.Join(f.Home, "Library/Caches/com.foo.app/link"))
	// 3. 上级目录是 symlink：~/Library/Logs 指向外部
	logsOut := t.TempDir()
	_ = os.WriteFile(filepath.Join(logsOut, "x.log"), make([]byte, 5000), 0o644)
	_ = os.Symlink(logsOut, filepath.Join(f.Home, "Library/Logs"))

	res := scanAll(t, f)
	if _, ok := find(res, "caches.apps:~/Library/Caches/com.evil.app"); ok {
		t.Fatal("symlink 的缓存目录不该列出")
	}
	if _, ok := find(res, "logs.user:~/Library/Logs/x.log"); ok {
		t.Fatal("上级目录是 symlink 的日志不该列出")
	}
	rep := Run(context.Background(), f.Env, []Item{mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app")}, RunOptions{})
	if rep.Outcomes[0].Status != StatusDone {
		t.Fatalf("%+v", rep.Outcomes[0])
	}
	if exists(filepath.Join(f.Home, "Library/Caches/com.foo.app/link")) || !exists(secret) {
		t.Fatal("应只删链接本身，外部文件必须还在")
	}

	// 伪造：把 ~/Library/Logs 指向外部后，硬塞一项过去 → 拒绝
	forged := Item{ID: "x", RuleID: "logs.user", Path: filepath.Join(f.Home, "Library/Logs/x.log"), Target: Whole}
	forged.Ident, _, _ = identityOf(forged.Path)
	rep = Run(context.Background(), f.Env, []Item{forged}, RunOptions{})
	if rep.Outcomes[0].Status != StatusSkipped || !exists(filepath.Join(logsOut, "x.log")) {
		t.Fatalf("经过 symlink 的路径必须拒绝：%+v", rep.Outcomes[0])
	}
}

func TestReplacedAndForgedItemsSkipped(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	res := scanAll(t, f)
	it := mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app")

	// 扫描后被替换（删掉重建，inode 变了）
	dir := filepath.Join(f.Home, "Library/Caches/com.foo.app")
	_ = os.RemoveAll(dir)
	f.file(t, "Library/Caches/com.foo.app/new-data", 1000)
	rep := Run(context.Background(), f.Env, []Item{it}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusSkipped || !strings.Contains(o.Reason, "替换") || !exists(filepath.Join(dir, "new-data")) {
		t.Fatalf("被替换的路径必须跳过：%+v", o)
	}

	// 伪造的项：不在规则里、在保护名单里、关键路径、未知规则
	doc := f.file(t, "Documents/thesis.docx", 1000)
	ident, _, _ := identityOf(doc)
	prot := filepath.Join(f.Home, "Library/Caches/com.apple.Something")
	pident, _, _ := identityOf(prot)
	homeIdent, _, _ := identityOf(f.Home)
	forged := []Item{
		{RuleID: "caches.apps", Path: doc, Target: Whole, Ident: ident},
		{RuleID: "caches.apps", Path: prot, Target: Contents, Ident: pident},
		{RuleID: "logs.user", Path: f.Home, Target: Contents, Ident: homeIdent},
		{RuleID: "no.such.rule", Path: doc, Target: Whole, Ident: ident},
		{RuleID: "caches.apps", Path: filepath.Join(f.Home, "Library/Caches/../../Documents/thesis.docx"), Target: Whole, Ident: ident},
	}
	rep = Run(context.Background(), f.Env, forged, RunOptions{})
	for _, o := range rep.Outcomes {
		if o.Status != StatusSkipped {
			t.Fatalf("伪造的项必须跳过：%+v", o)
		}
	}
	if !exists(doc) || !exists(filepath.Join(prot, "blob")) {
		t.Fatal("伪造的项被删了")
	}
}

func TestAppStartedAfterScanIsSkipped(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	res := scanAll(t, f)
	items := []Item{mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app"), mustFind(t, res, "browser.chrome:~/Library/Caches/Google/Chrome")}

	// 扫描后 app 启动了
	f.procs.BundleIDs["com.foo.app.helper"] = ""
	f.procs.Names["Google Chrome"] = true
	rep := Run(context.Background(), f.Env, items, RunOptions{})
	for _, o := range rep.Outcomes {
		if o.Status != StatusSkipped || !strings.Contains(o.Reason, "正在运行") {
			t.Fatalf("运行中应跳过：%+v", o)
		}
	}
	// 进程列表读不出来：判断不了也跳过
	f.procs.BundleIDs, f.procs.Names = map[string]string{}, map[string]bool{}
	f.procErr = errors.New("ps 失败")
	rep = Run(context.Background(), f.Env, items, RunOptions{})
	for _, o := range rep.Outcomes {
		if o.Status != StatusSkipped || !strings.Contains(o.Reason, "无法确认") {
			t.Fatalf("判断不了应跳过：%+v", o)
		}
	}
	// 有读不出 bundle id 的运行中 app：名字相关的缓存跳过，无关的照常
	f.procErr = nil
	f.procs.Uncertain = []string{"/Applications/Foo Studio.app"}
	rep = Run(context.Background(), f.Env, items[:1], RunOptions{DryRun: true})
	if o := rep.Outcomes[0]; o.Status != StatusSkipped || !strings.Contains(o.Reason, "无法确认") {
		t.Fatalf("和不确定的 app 同名的缓存应跳过：%+v", o)
	}
	f.procs.Uncertain = []string{"/Applications/Orca.app"}
	rep = Run(context.Background(), f.Env, items[:1], RunOptions{DryRun: true})
	if o := rep.Outcomes[0]; o.Status != StatusWould {
		t.Fatalf("和不确定的 app 无关的缓存应照常：%+v", o)
	}
	if !exists(filepath.Join(f.Home, "Library/Caches/com.foo.app/data/blob")) {
		t.Fatal("被跳过的项不应删除")
	}
}

func TestCancelStopsAfterCurrentItem(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	res := scanAll(t, f)
	items := []Item{mustFind(t, res, "logs.user:~/Library/Logs/app.log"), mustFind(t, res, "logs.user:~/Library/Logs/SomeApp"),
		mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app")}
	ctx, cancel := context.WithCancel(context.Background())
	rep := Run(ctx, f.Env, items, RunOptions{Progress: func(done, total int, o Outcome) {
		if done == 1 {
			cancel()
		}
	}})
	if !rep.Canceled || rep.Outcomes[0].Status != StatusDone || rep.Outcomes[1].Status != StatusCanceled || rep.Outcomes[2].Status != StatusCanceled {
		t.Fatalf("取消：%+v", rep.Outcomes)
	}
	if exists(filepath.Join(f.Home, "Library/Logs/app.log")) || !exists(filepath.Join(f.Home, "Library/Logs/SomeApp/run.log")) {
		t.Fatal("第一项应已删，后面的应保留")
	}
}

func TestPartialFailureContinues(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, "Library/Caches/com.foo.app/ok/blob", 10_000)
	locked := filepath.Dir(f.file(t, "Library/Caches/com.foo.app/locked/inner/blob", 10_000))
	f.file(t, "Library/Logs/app.log", 10_000)
	// inner 目录去掉写权限、且模拟属主不是当前用户：Vole 不会去改它的权限，里面的文件删不掉
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	old := fsops.CurrentUID
	fsops.CurrentUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { fsops.CurrentUID = old; _ = os.Chmod(locked, 0o755) })

	res := scanAll(t, f)
	items := []Item{mustFind(t, res, "caches.apps:~/Library/Caches/com.foo.app"), mustFind(t, res, "logs.user:~/Library/Logs/app.log")}
	rep := Run(context.Background(), f.Env, items, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusFailed || !strings.Contains(o.Reason, "删除失败") {
		t.Fatalf("部分失败：%+v", o)
	}
	if exists(filepath.Join(f.Home, "Library/Caches/com.foo.app/ok")) {
		t.Fatal("能删的子项应已删")
	}
	if rep.Outcomes[1].Status != StatusDone {
		t.Fatalf("出错后应继续下一项：%+v", rep.Outcomes[1])
	}
}

func TestToolCommands(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".cache/uv/archive/a.whl", 300_000)
	f.file(t, ".cache/uv/archive/b.whl", 300_000)
	f.file(t, "go/pkg/mod/cache/x", 50_000)
	f.tools["uv"] = "/fake/bin/uv"
	f.runFn = func(name string, args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "cache dir":
			return []byte(filepath.Join(f.Home, ".cache/uv") + "\n"), nil
		case "UV_LOCK_TIMEOUT=3 /fake/bin/uv cache prune": // 经 /usr/bin/env 带上锁超时
			return nil, os.Remove(filepath.Join(f.Home, ".cache/uv/archive/a.whl")) // 工具只清掉一部分
		}
		return nil, errors.New("unexpected")
	}
	res := scanAll(t, f)
	uv := mustFind(t, res, "dev.uv:~/.cache/uv")
	if !slices.Equal(uv.Command, []string{"/usr/bin/env", "UV_LOCK_TIMEOUT=3", "/fake/bin/uv", "cache", "prune"}) {
		t.Fatalf("装了 uv 应走工具命令：%v", uv.Command)
	}
	// 没装 go：Go module 缓存（Required）不列出
	if _, ok := find(res, "dev.gomod:~/go/pkg/mod"); ok {
		t.Fatal("没装 go 时不该列出 module 缓存")
	}
	dry := Run(context.Background(), f.Env, []Item{uv}, RunOptions{DryRun: true})
	if o := dry.Outcomes[0]; o.Status != StatusWould || o.Reason != "运行 /usr/bin/env UV_LOCK_TIMEOUT=3 /fake/bin/uv cache prune" {
		t.Fatalf("dry-run：%+v", o)
	}
	rep := Run(context.Background(), f.Env, []Item{uv}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusDone || o.Freed <= 0 || o.Freed >= uv.Size {
		t.Fatalf("工具清理：%+v（扫描时 %d）", o, uv.Size)
	}
	if !exists(filepath.Join(f.Home, ".cache/uv/archive/b.whl")) {
		t.Fatal("工具没删的部分应保留，Vole 不另外删")
	}

	// 工具给出的缓存目录在家目录外：不碰
	f.runFn = func(name string, args []string) ([]byte, error) { return []byte("/opt/elsewhere/uv\n"), nil }
	res = scanAll(t, f)
	if _, ok := find(res, "dev.uv:/opt/elsewhere/uv"); ok {
		t.Fatal("家目录外的缓存不该列出")
	}
}

func TestUnavailableSimulators(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, "Library/Developer/CoreSimulator/Devices/AAAA-1111/data/x", 40_000)
	f.tools["xcrun"] = "/fake/xcrun"
	f.runFn = func(name string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "simctl list devices unavailable -j" {
			return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-16-0":[{"udid":"AAAA-1111","name":"iPhone 14","isAvailable":false},{"udid":"../../evil","name":"x","isAvailable":false}]}}`), nil
		}
		return nil, nil
	}
	res := scanAll(t, f)
	it := mustFind(t, res, "xcode.unavailable:~/Library/Developer/CoreSimulator/Devices/AAAA-1111")
	if it.Title != "iPhone 14" || !slices.Equal(it.Command, []string{"/fake/xcrun", "simctl", "delete", "AAAA-1111"}) {
		t.Fatalf("%+v", it)
	}
	if n := len(itemsOf(res, CatXcode)); n != 1 {
		t.Fatal("带 .. 的 UDID 不该列出")
	}
}

func TestEmptyTrashIgnoresNames(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".Trash/com.apple.Something.app/x", 10_000) // 废纸篓里的东西不按名字保护
	f.file(t, ".Trash/report.pdf", 10_000)
	res := scanAll(t, f)
	tr := mustFind(t, res, "trash.empty:~/.Trash")
	rep := Run(context.Background(), f.Env, []Item{tr}, RunOptions{})
	if rep.Outcomes[0].Status != StatusDone {
		t.Fatalf("%+v", rep.Outcomes[0])
	}
	entries, _ := os.ReadDir(filepath.Join(f.Home, ".Trash"))
	if len(entries) != 0 || !exists(filepath.Join(f.Home, ".Trash")) {
		t.Fatal("应清空废纸篓里的全部内容、保留废纸篓目录")
	}
}

func TestMoveToTrash(t *testing.T) {
	f := newFakeEnv(t)
	a := f.file(t, "Projects/app/node_modules/x", 100)
	nm := filepath.Dir(a)
	if err := MoveToTrash(context.Background(), f.Env, nm); err != nil {
		t.Fatal(err)
	}
	if exists(nm) || !exists(filepath.Join(f.Home, ".Trash/node_modules/x")) {
		t.Fatal("应移到 ~/.Trash")
	}
	// 废纸篓里已有同名项：不覆盖，换个名字
	b := filepath.Dir(f.file(t, "Projects/other/node_modules/y", 100))
	if err := MoveToTrash(context.Background(), f.Env, b); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(f.Home, ".Trash"))
	if len(entries) != 2 || !exists(filepath.Join(f.Home, ".Trash/node_modules/x")) {
		t.Fatalf("同名时不应覆盖：%v", entries)
	}
	// 受保护的路径拒绝
	if err := MoveToTrash(context.Background(), f.Env, f.Home); err == nil {
		t.Fatal("家目录本身必须拒绝")
	}
}

func TestValidatePath(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, "Library/Caches/com.foo.app/x", 1)
	ok := filepath.Join(f.Home, "Library/Caches/com.foo.app/x")
	if err := validatePath(ok, f.Env, true); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"相对路径":   "Library/Caches/x",
		"含 ..":   f.Home + "/Library/../Documents",
		"控制字符":   f.Home + "/Library/Caches/a\nb",
		"家目录外":   "/tmp/x",
		"家目录本身":  f.Home,
		"系统路径":   "/System/Library",
		"钥匙串":    f.Home + "/Library/Keychains/login.keychain-db",
		"iCloud": f.Home + "/Library/Mobile Documents/x",
	}
	for name, p := range bad {
		if validatePath(p, f.Env, true) == nil {
			t.Errorf("%s 应拒绝：%s", name, p)
		}
	}
}

func TestEscapeLogValue(t *testing.T) {
	if got := escapeLogValue("a\nb\tc\x01d\\e"); got != `a\nb\tc\x01d\e` {
		t.Fatalf("%q", got)
	}
}

func TestDiskUsageCountsHardlinksOnce(t *testing.T) {
	f := newFakeEnv(t)
	p := f.file(t, "d/a", 100_000)
	_ = os.Link(p, filepath.Join(f.Home, "d/b"))
	one, _ := diskUsage(context.Background(), p)
	both, _ := diskUsage(context.Background(), filepath.Join(f.Home, "d"))
	if both > one+8192 {
		t.Fatalf("硬链接应只算一次：文件 %d，目录 %d", one, both)
	}
}

func TestScanProgressAndLastClean(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	var mu sync.Mutex
	started, done := map[string]bool{}, map[string]bool{}
	_, err := ScanWith(context.Background(), f.Env, ScanOptions{Progress: func(title string, finished bool) {
		mu.Lock()
		defer mu.Unlock()
		if finished {
			done[title] = true
		} else {
			started[title] = true
		}
	}})
	if err != nil || len(started) != 9 || len(done) != 9 || !done["开发者缓存"] {
		t.Fatalf("进度回调：%v %v %v", err, started, done)
	}

	if _, _, ok := LastClean(f.Env); ok {
		t.Fatal("没有日志时不应有上次清理")
	}
	res := scanAll(t, f)
	item := mustFind(t, res, "logs.user:~/Library/Logs/app.log")
	Run(context.Background(), f.Env, []Item{item}, RunOptions{DryRun: true}) // dry-run 不算
	if _, _, ok := LastClean(f.Env); ok {
		t.Fatal("dry-run 不算上次清理")
	}
	rep := Run(context.Background(), f.Env, []Item{item}, RunOptions{})
	at, freed, ok := LastClean(f.Env)
	if !ok || freed != rep.Freed/1024*1024 || time.Since(at) > time.Minute {
		t.Fatalf("上次清理：%v %d %v（释放 %d）", at, freed, ok, rep.Freed)
	}
}

func itemsOf(res *ScanResult, cat string) []Item {
	for _, c := range res.Categories {
		if c.ID == cat {
			return c.Items
		}
	}
	return nil
}

func TestUncertainAndProtectedItemsAtRun(t *testing.T) {
	f := newFakeEnv(t)
	f.tree(t)
	res := scanAll(t, f)
	plain := mustFind(t, res, "caches.apps:~/Library/Caches/plaindir")
	prot := mustFind(t, res, "caches.apps:~/Library/Caches/com.1password.1password")

	// 需确认的项：用户勾了就清（只在临时目录里删）
	rep := Run(context.Background(), f.Env, []Item{plain}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusDone || exists(filepath.Join(plain.Path, "blob")) {
		t.Fatalf("需确认的项勾了应清理：%+v", o)
	}
	// 受保护的项：即使被塞进清单（伪造 Blocked 为空）也跳过
	prot.Blocked = ""
	rep = Run(context.Background(), f.Env, []Item{prot}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusSkipped || !exists(filepath.Join(prot.Path, "blob")) {
		t.Fatalf("受保护的项必须跳过：%+v", o)
	}
	// 扫描后有了同名进程：挡住
	f.file(t, "Library/Caches/plaindir/blob2", 1000)
	res = scanAll(t, f)
	plain = mustFind(t, res, "caches.apps:~/Library/Caches/plaindir")
	f.procs.Names["plaindir"] = true
	rep = Run(context.Background(), f.Env, []Item{plain}, RunOptions{DryRun: true})
	if o := rep.Outcomes[0]; o.Status != StatusSkipped || !strings.Contains(o.Reason, "正在运行") {
		t.Fatalf("扫描后启动了应跳过：%+v", o)
	}
}

func TestWeChatMediaByMonth(t *testing.T) {
	f := newFakeEnv(t)
	base := "Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/wxid_x/msg/"
	thisMonth := time.Now().Format("2006-01")
	for _, rel := range []string{"video/2025-03/a.mp4", "attach/abcdef123456/2025-04/Img/b.dat", "video/" + thisMonth + "/new.mp4",
		"file/2025-03/doc.pdf", "video/notamonth/x"} {
		f.file(t, base+rel, 10_000) // 修改时间都是刚才：按目录名里的月份判断，不看修改时间
	}
	res := scanAll(t, f)
	v := mustFind(t, res, "comms.wechat.media:~/"+base+"video/2025-03")
	a := mustFind(t, res, "comms.wechat.media:~/"+base+"attach/abcdef123456/2025-04")
	if v.Title != "视频 2025-03" || a.Title != "图片 2025-04 · 会话 abcdef" || v.Default || v.Target != Whole {
		t.Fatalf("%+v %+v", v, a)
	}
	if n := len(itemsOf(res, CatComms)); n != 2 {
		t.Fatalf("只列本月以前的月份目录，不碰 msg/file：%d", n)
	}
	f.procs.Names["WeChat"] = true
	if b := mustFind(t, scanAll(t, f), v.ID).Blocked; b != "WeChat 正在运行" {
		t.Fatalf("微信在运行应挡住：%q", b)
	}
}

func TestOwnerUsesAppDisplayName(t *testing.T) {
	s := &ProcSnapshot{Names: map[string]bool{"Otty": true}, BundleIDs: map[string]string{"com.youdao.youdaodict": "/Applications/网易有道翻译.app"},
		displayName: func(app string) string {
			if strings.Contains(app, "有道") {
				return "网易有道翻译"
			}
			return ""
		}}
	if o := s.Owner("com.youdao.YoudaoDict"); !o.Running || o.Name != "网易有道翻译" {
		t.Fatalf("%+v", o)
	}
	if o := s.Owner("otty"); !o.Running || o.Name != "Otty" {
		t.Fatalf("非 bundle id 名字按进程名：%+v", o)
	}
	if o := s.Owner("puccinialin"); o.Running || o.Uncertain {
		t.Fatalf("对不上就算没在运行（同 Mole）：%+v", o)
	}
	// 子 id 的缓存（如 ShipIt）不因主 app 在运行而挡住（同 Mole：按名字找进程）
	if o := s.Owner("com.youdao.YoudaoDict.ShipIt"); o.Running {
		t.Fatalf("%+v", o)
	}
	// 进程路径里含缓存名：在运行；系统进程标系统占用
	s.Paths = []string{"/system/library/privateframeworks/familycircle.framework/versions/a/resources/com.apple.familycircled"}
	s.System = map[string]bool{"com.apple.familycircled": true}
	if o := s.Owner("com.apple.familycircled"); !o.Running || !o.System {
		t.Fatalf("系统进程：%+v", o)
	}
	if o := s.Owner("com.foo.bar"); o.Running || o.Uncertain {
		t.Fatalf("bundle id 没在运行：%+v", o)
	}
}

func TestUvPrunableEstimate(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".cache/uv/archive-v0/AAAAAAAAAAAAAAAAAAAA1/pkg/x.py", 200_000) // 被 wheels 指针引用
	f.file(t, ".cache/uv/archive-v0/BBBBBBBBBBBBBBBBBBBB2/pkg/x.py", 100_000) // 没人引用：prune 会删
	p := f.file(t, ".cache/uv/wheels-v5/pypi/foo/1.0.http", 0)
	_ = os.WriteFile(p, []byte("\x94\xb5AAAAAAAAAAAAAAAAAAAA1\x90foo-1.0.whl"), 0o644)
	n, ok := uvPrunable(context.Background(), filepath.Join(f.Home, ".cache/uv"))
	if !ok || n < 100_000 || n > 150_000 {
		t.Fatalf("估算只算没被引用的 archive：%d %v", n, ok)
	}
	// 一个引用都找不到（uv 换了格式）：不估算
	_ = os.WriteFile(p, []byte("nothing"), 0o644)
	if _, ok := uvPrunable(context.Background(), filepath.Join(f.Home, ".cache/uv")); ok {
		t.Fatal("找不到引用时应放弃估算")
	}
}

func TestUvGuardOnlyWithoutTool(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".cache/uv/archive-v0/x/y", 100_000)
	f.procs.Names["uvx"] = true
	// 没装 uv：有 uv 进程就不按路径删
	if b := mustFind(t, scanAll(t, f), "dev.uv:~/.cache/uv").Blocked; b != "uvx 正在运行" {
		t.Fatalf("没装 uv 时应按进程拦：%q", b)
	}
	// 装了 uv：交给 uv cache prune，不按进程拦（uv 自己有锁）
	f.tools["uv"] = "/fake/bin/uv"
	f.runFn = func(name string, args []string) ([]byte, error) { return nil, errors.New("no") }
	if b := mustFind(t, scanAll(t, f), "dev.uv:~/.cache/uv").Blocked; b != "" {
		t.Fatalf("装了 uv 时不按进程拦：%q", b)
	}
}

func TestObsoleteExtensionsAndMCP(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".cursor/extensions/foo.bar-1.0.0/x", 50_000)
	f.file(t, ".cursor/extensions/foo.bar-2.0.0/x", 50_000)
	f.file(t, ".cursor/extensions/keep-1.0/x", 50_000)
	p := f.file(t, ".cursor/extensions/.obsolete", 0)
	_ = os.WriteFile(p, []byte(`{"foo.bar-1.0.0":true,"keep-1.0":false,"../evil":true}`), 0o644)
	f.file(t, ".cache/chrome-devtools-mcp/chrome-profile/Default/Cache/x", 50_000)
	f.file(t, ".cache/chrome-devtools-mcp/chrome-profile/Default/Cookies", 50_000)
	res := scanAll(t, f)
	ext := mustFind(t, res, "dev.obsoleteext:~/.cursor/extensions/foo.bar-1.0.0")
	if ext.Group != "过时的 Cursor 扩展" || !ext.Default || ext.Target != Whole {
		t.Fatalf("%+v", ext)
	}
	if n := len(itemsOf(res, CatDev)); n != 1 {
		t.Fatalf("只列 .obsolete 里值为 true 的：%d", n)
	}
	mcp := mustFind(t, res, "ai.mcp:~/.cache/chrome-devtools-mcp/chrome-profile/Default/Cache")
	if mcp.Group != "Chrome DevTools MCP" || mcp.Blocked != "" {
		t.Fatalf("%+v", mcp)
	}
	f.procs.Procs = []Proc{{PID: 42, Args: "node /opt/homebrew/bin/chrome-devtools-mcp --isolated"}}
	if b := mustFind(t, scanAll(t, f), mcp.ID).Blocked; b == "" {
		t.Fatal("MCP 在运行（按命令行匹配）应挡住")
	}
}

func TestCursorShipItNeedsConfirm(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, "Library/Caches/com.todesktop.230313mzl4w4u92.ShipIt/update.abc/Cursor.zip", 100_000)
	f.file(t, "Library/Caches/com.todesktop.230313mzl4w4u92/data", 100_000)
	f.procs.BundleIDs["com.todesktop.230313mzl4w4u92"] = "/Applications/Cursor.app"
	res := scanAll(t, f)
	ship := mustFind(t, res, "caches.apps:~/Library/Caches/com.todesktop.230313mzl4w4u92.ShipIt")
	if ship.Blocked != "" || ship.Default || !strings.Contains(ship.Note, "删了 Cursor 会重新下载更新") {
		t.Fatalf("ShipIt 应列出、需确认、默认不勾，说明写明 app：%+v", ship)
	}
	if b := mustFind(t, res, "caches.apps:~/Library/Caches/com.todesktop.230313mzl4w4u92").Blocked; !strings.HasPrefix(b, "受保护") {
		t.Fatalf("com.todesktop. 的其余数据仍受保护：%q", b)
	}
	// 勾了就清（临时目录）
	rep := Run(context.Background(), f.Env, []Item{ship}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusDone || exists(filepath.Join(ship.Path, "update.abc")) {
		t.Fatalf("%+v", o)
	}
}

func TestUvCleanAll(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".cache/uv/archive-v0/x/y", 100_000)
	f.tools["uv"] = "/fake/bin/uv"
	busy := false
	f.runFn = func(name string, args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "cache dir":
			return []byte(filepath.Join(f.Home, ".cache/uv") + "\n"), nil
		case "UV_LOCK_TIMEOUT=3 /fake/bin/uv cache clean":
			if busy {
				return nil, errors.New("exit status 2：error: Could not acquire lock for `cache`")
			}
			return nil, os.RemoveAll(filepath.Join(f.Home, ".cache/uv/archive-v0"))
		}
		return nil, errors.New("unexpected")
	}
	all := mustFind(t, scanAll(t, f), "dev.uv.all:~/.cache/uv")
	if all.Default || all.Blocked != "" || all.Size < 100_000 || !strings.Contains(all.Note, "已装好的虚拟环境不受影响") {
		t.Fatalf("uv 全部缓存：需确认、默认不勾、大小是整个目录：%+v", all)
	}
	// 有 uv 进程：跳过并写明原因
	f.procs.Names["uvx"] = true
	if b := mustFind(t, scanAll(t, f), all.ID).Blocked; b != "uv 正在使用缓存" {
		t.Fatalf("%q", b)
	}
	f.procs.Names["uvx"] = false
	// 锁被占着（新版 uv 按 UV_LOCK_TIMEOUT 放弃）：跳过，不算失败
	busy = true
	if o := Run(context.Background(), f.Env, []Item{all}, RunOptions{}).Outcomes[0]; o.Status != StatusSkipped || o.Reason != "uv 正在使用缓存" {
		t.Fatalf("%+v", o)
	}
	busy = false
	if o := Run(context.Background(), f.Env, []Item{all}, RunOptions{}).Outcomes[0]; o.Status != StatusDone {
		t.Fatalf("%+v", o)
	}
}

func TestCodexStaleStaging(t *testing.T) {
	f := newFakeEnv(t)
	root := "Library/Caches/com.openai.codex/org.sparkle-project.Sparkle/Installation/"
	f.file(t, root+"old1/x/ChatGPT.app/Contents/Info.plist", 100)  // 旧版本：清
	f.file(t, root+"old1/update.zip", 100_000)                     //
	f.file(t, root+"new1/y/ChatGPT.app/Contents/Info.plist", 100)  // 还没安装的新版本：不碰
	f.file(t, root+"new1/update.zip", 100_000)                     //
	f.file(t, root+"same/ChatGPT.app/Contents/Info.plist", 100)    // 和已装的同一版本：已经装上了，清
	f.file(t, root+"same/update.zip", 100_000)                     //
	f.file(t, "Applications/ChatGPT.app/Contents/Info.plist", 100) // 已装：22391
	builds := map[string]string{"old1": "20000", "new1": "25471", "same": "22391", "Applications": "22391"}
	f.runFn = func(name string, args []string) ([]byte, error) {
		if name != "/usr/bin/plutil" {
			return nil, errors.New("unexpected " + name)
		}
		plist := args[len(args)-1]
		if !strings.HasPrefix(plist, f.Home) {
			return nil, errors.New("不读真实的 /Applications")
		}
		if args[1] == "CFBundleIdentifier" {
			return []byte("com.openai.codex\n"), nil
		}
		for k, v := range builds {
			if strings.Contains(plist, "/"+k+"/") {
				return []byte(v + "\n"), nil
			}
		}
		return nil, errors.New("no")
	}
	res := scanAll(t, f)
	for _, id := range []string{"old1", "same"} {
		it := mustFind(t, res, "ai.codex.staging:~/"+root+id)
		if !it.Default || it.Group != "Codex" || it.Target != Whole {
			t.Fatalf("%+v", it)
		}
	}
	if _, ok := find(res, "ai.codex.staging:~/"+root+"new1"); ok {
		t.Fatal("还没安装的新版本绝不能列出")
	}
	// 已装版本读不出来：版本号判断不了，30 天内的都不碰
	builds["Applications"] = "bad"
	if _, ok := find(scanAll(t, f), "ai.codex.staging:~/"+root+"old1"); ok {
		t.Fatal("读不出已装版本时，30 天内的更新包不该列出")
	}
}

func TestUvHoldersAndSymlinkRisk(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, ".cache/uv/archive-v0/x/y", 100_000)
	f.tools["uv"] = "/fake/bin/uv"
	f.runFn = func(name string, args []string) ([]byte, error) {
		if strings.Join(args, " ") == "cache dir" {
			return []byte(filepath.Join(f.Home, ".cache/uv") + "\n"), nil
		}
		return nil, errors.New("no")
	}
	envs := map[string]string{}
	f.Getenv = func(k string) string { return envs[k] }
	f.EtcDir = filepath.Join(t.TempDir(), "etc")

	// 被 uv run 占着：写出项目目录名和 PID
	f.procs.Names["uv"] = true
	f.procs.Procs = []Proc{
		{PID: 69222, Args: "uv --directory /Users/x/Code/my-mcp-server run main.py"},
		{PID: 69223, Args: "uv --directory /Users/x/Code/my-mcp-server run file.py"},
		{PID: 85185, Args: "uv --directory /Users/x/Code/my-mcp-server run main.py"},
		{PID: 85197, Args: "uv --directory=/Users/x/Code/my-mcp-server run file.py"},
		{PID: 1, Args: "/usr/bin/python3 -m uv"}, // 不是 uv 进程
	}
	it := mustFind(t, scanAll(t, f), "dev.uv.all:~/.cache/uv")
	if want := "被 uv run 占用：my-mcp-server（PID 69222、69223、85185 等 4 个）"; it.Blocked != want {
		t.Fatalf("%q", it.Blocked)
	}
	// 取不到目录就写 PID；目录里有空格也能取出来
	if got := uvHolders([]Proc{{PID: 7, Args: "uv run main.py"}}); got != "被 uv run 占用：PID 7" {
		t.Fatalf("%q", got)
	}
	if _, dir := uvCommand("uv --project /a/My Proj run x.py"); dir != "/a/My Proj" {
		t.Fatalf("%q", dir)
	}
	if got := uvHolders([]Proc{{PID: 8, Args: "/opt/homebrew/bin/uvx ruff"}}); got != "被 uvx 占用：PID 8" {
		t.Fatalf("%q", got)
	}
	f.procs.Names["uv"], f.procs.Procs = false, nil

	// symlink 模式：说明换成风险提示，仍默认不勾
	normal := mustFind(t, scanAll(t, f), "dev.uv.all:~/.cache/uv")
	if strings.Contains(normal.Note, "symlink") {
		t.Fatal("没设 symlink 时不该提示")
	}
	check := func(what string) {
		t.Helper()
		it := mustFind(t, scanAll(t, f), "dev.uv.all:~/.cache/uv")
		if it.Note != "检测到 uv 使用 symlink 模式，清空缓存会让已有虚拟环境失效。" || it.Default {
			t.Fatalf("%s：%+v", what, it)
		}
	}
	envs["UV_LINK_MODE"] = "symlink"
	check("环境变量")
	delete(envs, "UV_LINK_MODE")
	p := f.file(t, ".config/uv/uv.toml", 0)
	_ = os.WriteFile(p, []byte("[pip]\nlink-mode = \"symlink\"\n"), 0o644)
	check("~/.config/uv/uv.toml")
	_ = os.Remove(p)
	xdg := t.TempDir()
	envs["XDG_CONFIG_HOME"] = xdg
	_ = os.MkdirAll(filepath.Join(xdg, "uv"), 0o755)
	_ = os.WriteFile(filepath.Join(xdg, "uv", "uv.toml"), []byte("link-mode = 'symlink'\n"), 0o644)
	check("XDG_CONFIG_HOME")
	delete(envs, "XDG_CONFIG_HOME")
	_ = os.MkdirAll(filepath.Join(f.EtcDir, "uv"), 0o755)
	_ = os.WriteFile(filepath.Join(f.EtcDir, "uv", "uv.toml"), []byte("link-mode = \"symlink\"\n"), 0o644)
	check("/etc/uv/uv.toml")
	_ = os.WriteFile(filepath.Join(f.EtcDir, "uv", "uv.toml"), []byte("# link-mode = \"symlink\"\nlink-mode = \"clone\"\n"), 0o644)
	if strings.Contains(mustFind(t, scanAll(t, f), "dev.uv.all:~/.cache/uv").Note, "symlink") {
		t.Fatal("注释掉的、或设成 clone 的不该提示")
	}
}

// readOnlyTree 造一棵只读目录树（同 dotslash 解压出的工具：目录都是 dr-xr-xr-x），测试结束前恢复写权限，好让临时目录能删掉。
func readOnlyTree(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for i := len(dirs) - 1; i >= 0; i-- { // 先改深的，再改浅的
		if err := os.Chmod(filepath.Join(root, dirs[i]), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
}

func TestRemoveReadOnlyTree(t *testing.T) {
	f := newFakeEnv(t)
	// 只读的嵌套目录 + 指向外部（也是只读）目录的 symlink
	f.file(t, "Library/Caches/dotslash/46/a2fd/Tool.app/Contents/MacOS/tool", 50_000)
	f.file(t, "Library/Caches/dotslash/46/a2fd/Tool.app/Contents/Info.plist", 1_000)
	outside := f.file(t, "Outside/keep.txt", 100)
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(f.Home, "Library/Caches/dotslash/46/a2fd/link")); err != nil {
		t.Fatal(err)
	}
	f.file(t, "Library/Caches/sibling/ro/x", 100) // 同级的别的只读目录：不该被改权限
	readOnlyTree(t, f.Home, "Outside", "Library/Caches/sibling/ro",
		"Library/Caches/dotslash/46", "Library/Caches/dotslash/46/a2fd", "Library/Caches/dotslash/46/a2fd/Tool.app",
		"Library/Caches/dotslash/46/a2fd/Tool.app/Contents", "Library/Caches/dotslash/46/a2fd/Tool.app/Contents/MacOS")

	res := scanAll(t, f)
	it := mustFind(t, res, "caches.apps:~/Library/Caches/dotslash")
	rep := Run(context.Background(), f.Env, []Item{it}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusDone {
		t.Fatalf("只读目录树应能删干净：%+v", o)
	}
	if entries, _ := os.ReadDir(it.Path); len(entries) != 0 {
		t.Fatalf("dotslash 里还剩：%v", entries)
	}
	// 不跟随 symlink、不越界：外部目录和同级目录原样（文件在、权限仍是只读）
	if !exists(outside) {
		t.Fatal("symlink 指向的外部文件被删了")
	}
	for _, d := range []string{"Outside", "Library/Caches/sibling/ro"} {
		if info, _ := os.Stat(filepath.Join(f.Home, d)); info.Mode().Perm() != 0o555 {
			t.Fatalf("%s 的权限被改了：%v", d, info.Mode().Perm())
		}
	}
}

func TestRemoveReadOnlyWholeAndItemDir(t *testing.T) {
	root := t.TempDir()
	// 「删整项」：只读子目录
	whole := filepath.Join(root, "whole")
	_ = os.MkdirAll(filepath.Join(whole, "a", "b"), 0o755)
	_ = os.WriteFile(filepath.Join(whole, "a", "b", "f"), []byte("x"), 0o444)
	readOnlyTree(t, root, "whole/a", "whole/a/b")
	if err := removeAllWritable(whole); err != nil || exists(whole) {
		t.Fatalf("应删干净：%v", err)
	}
	// 「删内容、留目录」：项本身的目录是只读的，允许给它加写权限
	item := filepath.Join(root, "item")
	_ = os.MkdirAll(item, 0o755)
	_ = os.WriteFile(filepath.Join(item, "f"), []byte("x"), 0o644)
	readOnlyTree(t, root, "item")
	if err := removeAllWritable(filepath.Join(item, "f"), item); err != nil {
		t.Fatalf("%v", err)
	}
	// 项的上级目录是只读的：不越界去改它，照样报没有权限
	parent := filepath.Join(root, "parent")
	_ = os.MkdirAll(filepath.Join(parent, "child"), 0o755)
	readOnlyTree(t, root, "parent")
	if err := removeAllWritable(filepath.Join(parent, "child")); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("上级只读应报没有权限：%v", err)
	}
	if info, _ := os.Stat(parent); info.Mode().Perm() != 0o555 {
		t.Fatal("上级目录的权限被改了")
	}
}

func TestRemoveReadOnlyNotOwnedStays(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "other")
	_ = os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(d, "sub", "f"), []byte("x"), 0o644)
	readOnlyTree(t, root, "other/sub")
	// 模拟属主不是当前用户
	old := fsops.CurrentUID
	fsops.CurrentUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { fsops.CurrentUID = old })
	err := removeAllWritable(d)
	if !errors.Is(err, fs.ErrPermission) || reasonOf(err) != "没有权限" {
		t.Fatalf("别人的目录应照样报没有权限：%v", err)
	}
	if info, _ := os.Stat(filepath.Join(d, "sub")); info.Mode().Perm() != 0o555 {
		t.Fatal("别人的目录不该被改权限")
	}
}

func TestPipPurgeEstimateAndEmptyCache(t *testing.T) {
	f := newFakeEnv(t)
	f.file(t, "Library/Caches/pip/selfcheck/abc.json", 20_000) // purge 不删：不算
	f.tools["pip3"] = "/usr/bin/pip3"
	out := ""
	f.runFn = func(name string, args []string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "cache dir":
			return []byte(filepath.Join(f.Home, "Library/Caches/pip") + "\n"), nil
		case "cache purge":
			if out != "" {
				return nil, errors.New(out)
			}
			return nil, nil
		}
		return nil, errors.New("unexpected")
	}
	// 只剩 selfcheck：purge 清不掉，不列
	if _, ok := find(scanAll(t, f), "dev.pip:~/Library/Caches/pip"); ok {
		t.Fatal("只剩 purge 不删的东西时不该列出 pip")
	}
	// 有下载缓存和本地编译的 wheel：只算这些
	f.file(t, "Library/Caches/pip/http-v2/a/b/c.body", 100_000)
	f.file(t, "Library/Caches/pip/wheels/x/y/foo-1.0-py3-none-any.whl", 50_000)
	f.file(t, "Library/Caches/pip/wheels/x/y/origin.json", 1_000) // 不是 .whl：不算
	it := mustFind(t, scanAll(t, f), "dev.pip:~/Library/Caches/pip")
	if it.Size < 150_000 || it.Size > 170_000 {
		t.Fatalf("大小应只算 purge 能清的：%d", it.Size)
	}
	// 旧版 pip 没东西可删：返回 1、ERROR: No matching packages → 算已清理
	out = "exit status 1：ERROR: No matching packages"
	if o := Run(context.Background(), f.Env, []Item{it}, RunOptions{}).Outcomes[0]; o.Status != StatusDone || o.Reason != "没有可清理的内容" {
		t.Fatalf("%+v", o)
	}
	// 其他错误照样报失败
	out = "exit status 2：ERROR: Permission denied"
	if o := Run(context.Background(), f.Env, []Item{it}, RunOptions{}).Outcomes[0]; o.Status != StatusFailed {
		t.Fatalf("%+v", o)
	}
}
