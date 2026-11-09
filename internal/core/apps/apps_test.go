// SPDX-License-Identifier: GPL-3.0-only

package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fake 是测试环境：家目录、应用程序目录、Caskroom 都在临时目录里；plutil 用真的（只读临时目录里的文件），
// 其余外部命令（mdls、codesign、osascript、launchctl、lsregister、brew）全是假的，进程列表和信号也是假的。
type fake struct {
	*Env
	mu      sync.Mutex
	procs   []Proc
	ran     [][]string
	groups  map[string][]string // app 路径 → 签名里的 App Group
	onQuit  func(bundleID string)
	signals []string
}

func newFake(t *testing.T) *fake {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real, _ := os.UserHomeDir()
	if real != "" && (root == real || strings.HasPrefix(real, root) || strings.HasPrefix(root, real+"/")) {
		t.Fatalf("测试目录不安全：%s", root)
	}
	f := &fake{groups: map[string][]string{}}
	home := filepath.Join(root, "home")
	_ = os.MkdirAll(filepath.Join(home, "Library"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "Applications"), 0o755)
	f.Env = &Env{
		Home: home, Roots: []string{filepath.Join(root, "Applications"), filepath.Join(home, "Applications")},
		CaskRoots: []string{filepath.Join(root, "Caskroom")}, LogDir: filepath.Join(root, "logs"),
		DarwinCache: filepath.Join(root, "dcache"), Languages: []string{"zh-Hans", "en"}, SelfID: SelfBundleID,
		Now: time.Now, QuitWait: 300 * time.Millisecond, KillWait: 200 * time.Millisecond,
		LookPath: func(name string) (string, error) { return "", exec.ErrNotFound },
	}
	f.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		f.mu.Lock()
		f.ran = append(f.ran, append([]string{name}, args...))
		f.mu.Unlock()
		switch filepath.Base(name) {
		case "plutil":
			return runCommand(ctx, name, args...)
		case "codesign":
			app := args[len(args)-1]
			gs := f.groups[app]
			if len(gs) == 0 {
				return nil, errors.New("no entitlements")
			}
			var b strings.Builder
			b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.application-groups</key><array>`)
			for _, g := range gs {
				b.WriteString("<string>" + g + "</string>")
			}
			b.WriteString(`</array></dict></plist>`)
			return []byte(b.String()), nil
		case "osascript":
			script := args[len(args)-1]
			if f.onQuit != nil && strings.Contains(script, "to quit") {
				id := strings.Split(script, `"`)[1]
				f.onQuit(id)
			}
			return nil, nil
		}
		return nil, errors.New("测试里不跑 " + name)
	}
	f.Processes = func(context.Context) ([]Proc, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return append([]Proc(nil), f.procs...), nil
	}
	f.Signal = func(pid int, sig syscall.Signal) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.signals = append(f.signals, fmt.Sprintf("%d:%v", pid, sig))
		return nil
	}
	return f
}

func (f *fake) apps() string { return f.Roots[0] }

func write(t *testing.T, p, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// plist 写一个 XML plist。
func plist(kv map[string]any) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`)
	for k, v := range kv {
		b.WriteString("<key>" + k + "</key>")
		switch v := v.(type) {
		case bool:
			if v {
				b.WriteString("<true/>")
			} else {
				b.WriteString("<false/>")
			}
		case []string:
			b.WriteString("<array>")
			for _, s := range v {
				b.WriteString("<string>" + s + "</string>")
			}
			b.WriteString("</array>")
		default:
			b.WriteString(fmt.Sprintf("<string>%v</string>", v))
		}
	}
	b.WriteString(`</dict></plist>`)
	return b.String()
}

// mkApp 造一个假 .app：Info.plist + 一个可执行文件。
func mkApp(t *testing.T, path, bid string, extra map[string]any) string {
	t.Helper()
	exe := strings.TrimSuffix(filepath.Base(path), ".app")
	kv := map[string]any{"CFBundleIdentifier": bid, "CFBundleExecutable": exe, "CFBundleShortVersionString": "1.0"}
	for k, v := range extra {
		kv[k] = v
	}
	write(t, filepath.Join(path, "Contents", "Info.plist"), plist(kv))
	write(t, filepath.Join(path, "Contents", "MacOS", exe), strings.Repeat("x", 5000))
	return path
}

func byName(list []App) map[string]App {
	m := map[string]App{}
	for _, a := range list {
		m[filepath.Base(a.Path)] = a
	}
	return m
}

func TestListRules(t *testing.T) {
	f := newFake(t)
	A := f.apps()
	mkApp(t, A+"/Foo.app", "com.foo.app", nil)
	mkApp(t, A+"/Foo.app/Contents/Library/LoginItems/FooHelper.app", "com.foo.helper", nil) // 嵌套：不单列
	mkApp(t, A+"/.staging/Bar.app", "com.bar.app", nil)                                     // 隐藏目录：跳过
	mkApp(t, A+"/a/b/c/Deep.app", "com.deep.app", nil)                                      // 深度超过 3：不扫
	mkApp(t, A+"/Utilities/Bg.app", "com.bg.app", map[string]any{"LSBackgroundOnly": true}) // 后台专用、不在第一层：不列
	mkApp(t, A+"/Finder.app", "com.apple.finder", nil)                                      // 系统关键：不列
	mkApp(t, A+"/Xcode.app", "com.apple.dt.Xcode", nil)                                     // Apple 可卸载：列
	mkApp(t, A+"/Vole.app", SelfBundleID, nil)                                              // 自己：列出但不能卸
	mkApp(t, A+"/Falcon.app", "com.crowdstrike.falcon.App", nil)                            // 官方卸载器：列出但不能卸
	mkApp(t, A+"/Menu.app", "com.menu.app", map[string]any{"LSUIElement": true})
	mas := mkApp(t, A+"/Store.app", "com.store.app", nil)
	write(t, mas+"/Contents/_MASReceipt/receipt", "r")
	steam := mkApp(t, f.Home+"/Applications/Game.app", "", nil)
	write(t, steam+"/Contents/MacOS/Game", "#!/bin/sh\nopen steam://run/12345\n")
	mkApp(t, f.CaskRoots[0]+"/brewapp/1.0/Brew.app", "com.brew.app", nil)
	mkApp(t, A+"/Brew.app", "com.brew.app", nil)
	// 本地化显示名：zh-Hans.lproj
	loc := mkApp(t, A+"/WeChat.app", "com.tencent.xinWeChat", map[string]any{"CFBundleName": "WeChat"})
	write(t, loc+"/Contents/Resources/zh-Hans.lproj/InfoPlist.strings", `"CFBundleDisplayName" = "微信";`)
	write(t, loc+"/Contents/Resources/en.lproj/InfoPlist.strings", `"CFBundleDisplayName" = "WeChat";`)
	// 同 bundle id + 同文件名的副本：只留 /Applications 那份
	mkApp(t, f.Home+"/Applications/Foo.app", "com.foo.app", nil)
	f.procs = []Proc{{PID: 10, Path: A + "/Menu.app/Contents/MacOS/Menu"}, {PID: 11, Path: A + "/Foo.app/Contents/MacOS/Foo"}}

	list, err := List(context.Background(), f.Env, ListOptions{Sizes: true})
	if err != nil {
		t.Fatal(err)
	}
	m := byName(list)
	for _, gone := range []string{"FooHelper.app", "Bar.app", "Deep.app", "Bg.app", "Finder.app"} {
		if _, ok := m[gone]; ok {
			t.Fatalf("%s 不该出现", gone)
		}
	}
	if m["Foo.app"].Path != A+"/Foo.app" || !m["Foo.app"].Running || !m["Foo.app"].SizeKnown || m["Foo.app"].Size == 0 {
		t.Fatalf("Foo：%+v", m["Foo.app"])
	}
	n := 0
	for _, a := range list {
		if a.BundleID == "com.foo.app" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("副本应去重：%d", n)
	}
	if _, ok := m["Xcode.app"]; !ok {
		t.Fatal("Xcode（Apple 可卸载名单）应列出")
	}
	if !strings.Contains(m["Vole.app"].Blocked, "不能卸载自己") || !strings.Contains(m["Falcon.app"].Blocked, "CrowdStrike") {
		t.Fatalf("自己和官方卸载器：%q %q", m["Vole.app"].Blocked, m["Falcon.app"].Blocked)
	}
	if !m["Menu.app"].MenuBar || !m["Menu.app"].Running {
		t.Fatal("菜单栏常驻")
	}
	if m["Store.app"].Source != SourceAppStore || m["Game.app"].Source != SourceSteam || m["Game.app"].Size != 0 {
		t.Fatalf("来源：%v %v", m["Store.app"].Source, m["Game.app"].Source)
	}
	if b := m["Brew.app"]; b.Source != SourceHomebrew || b.CaskToken != "brewapp" {
		t.Fatalf("Homebrew：%+v", b)
	}
	if m["WeChat.app"].Name != "微信" {
		t.Fatalf("本地化名：%q", m["WeChat.app"].Name)
	}
	if m["Foo.app"].LastUsed.IsZero() || m["Foo.app"].Installed.IsZero() {
		t.Fatal("上次使用和安装日期应有兜底值")
	}
}

func TestIntelAndAdmin(t *testing.T) {
	f := newFake(t)
	A := f.apps()
	app := mkApp(t, A+"/Old.app", "com.old.app", nil)
	if out, err := exec.Command("/usr/bin/lipo", "-thin", "x86_64", "/usr/bin/true", "-output", app+"/Contents/MacOS/Old").CombinedOutput(); err != nil {
		t.Skipf("没有 x86_64 切片可用：%s", out)
	}
	mkApp(t, A+"/Locked/Root.app", "com.root.app", nil)
	if err := os.Chmod(A+"/Locked", 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(A+"/Locked", 0o755) })
	m := byName(must(List(context.Background(), f.Env, ListOptions{})))
	if !m["Old.app"].Intel {
		t.Fatal("只有 x86_64 应标 Intel")
	}
	if !m["Root.app"].NeedsAdmin || m["Old.app"].NeedsAdmin {
		t.Fatalf("所在目录不可写才要管理员：%v %v", m["Root.app"].NeedsAdmin, m["Old.app"].NeedsAdmin)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func paths(files []File, home string) map[string]File {
	m := map[string]File{}
	for _, f := range files {
		m[strings.ToLower(strings.Replace(f.Path, home, "~", 1))] = f // APFS 不区分大小写，按小写比
	}
	return m
}

func TestFilesRules(t *testing.T) {
	f := newFake(t)
	A, h := f.apps(), f.Home
	app := mkApp(t, A+"/Foo Studio.app", "com.foo.studio", nil)
	mkApp(t, app+"/Contents/Library/LoginItems/Launcher.app", "com.foo.studio.launcher", nil)
	write(t, app+"/Contents/Resources/app/package.json", `{"name":"foo-studio-electron"}`)
	mkApp(t, app+"/Contents/MacOS/Foo Engine.app", "com.foo.engine", map[string]any{"CFBundleName": "Foo Engine"})
	f.groups[app] = []string{"group.com.foo"}
	L := h + "/Library"
	for _, p := range []string{
		"Application Support/com.foo.studio/db", "Application Support/Foo Studio/x", "Application Support/foo-studio/x",
		"Caches/com.foo.studio/c", "Logs/Foo Studio/l.log", "Preferences/com.foo.studio.plist",
		"Preferences/ByHost/com.foo.studio.ABC.plist", "Group Containers/ABCDE12345.com.foo.studio/g",
		"Containers/com.foo.studio.launcher/Data/x", "Saved Application State/com.foo.studio.savedState/s",
		"Application Support/foo-studio-electron/e", "Application Support/Foo Engine/e", "Group Containers/group.com.foo/g",
		"Logs/DiagnosticReports/Foo Studio_2026-11-01.ips", "Logs/DiagnosticReports/Other_2026.ips",
		"Application Support/Unrelated/x",
	} {
		write(t, L+"/"+p, "data")
	}
	write(t, L+"/LaunchAgents/com.foo.agent.plist", plist(map[string]any{"Label": "com.foo.agent", "Program": app + "/Contents/MacOS/Foo Studio"}))
	write(t, L+"/LaunchAgents/com.other.plist", plist(map[string]any{"Label": "com.other", "Program": "/usr/local/bin/other"}))
	write(t, f.DarwinCache+"/com.foo.studio/x", "c")
	write(t, h+"/.config/foo studio/c", "c")
	// 只剩元数据的 Container 空壳：不报
	write(t, L+"/Containers/com.foo.studio/.com.apple.containermanagerd.metadata.plist", "m")

	list := must(List(context.Background(), f.Env, ListOptions{}))
	foo := byName(list)["Foo Studio.app"]
	m := paths(Files(context.Background(), f.Env, foo, list), h)
	want := map[string]Kind{
		"~/Library/Application Support/com.foo.studio": KindData, "~/Library/Application Support/Foo Studio": KindData,
		"~/Library/Application Support/foo-studio": KindData, "~/Library/Caches/com.foo.studio": KindCache,
		"~/Library/Logs/Foo Studio": KindCache, "~/Library/Preferences/com.foo.studio.plist": KindData,
		"~/Library/Preferences/ByHost/com.foo.studio.ABC.plist": KindData, "~/Library/Group Containers/ABCDE12345.com.foo.studio": KindData,
		"~/Library/Containers/com.foo.studio.launcher": KindData, "~/Library/Saved Application State/com.foo.studio.savedState": KindData,
		"~/Library/LaunchAgents/com.foo.agent.plist": KindData, "~/Library/Logs/DiagnosticReports/Foo Studio_2026-11-01.ips": KindCache,
		"~/.config/foo studio": KindData,
		// Vole 补充
		"~/Library/Application Support/foo-studio-electron": KindData, "~/Library/Application Support/Foo Engine": KindData,
		"~/Library/Group Containers/group.com.foo": KindData,
	}
	for p, k := range want {
		got, ok := m[strings.ToLower(p)]
		if !ok {
			t.Errorf("应找到 %s", p)
			continue
		}
		if got.Kind != k || got.Default != (k == KindCache) {
			t.Errorf("%s 分类：%v 默认 %v", p, got.Kind, got.Default)
		}
	}
	for _, p := range []string{"~/Library/Application Support/foo-studio-electron", "~/Library/Application Support/Foo Engine", "~/Library/Group Containers/group.com.foo"} {
		if m[strings.ToLower(p)].Extra == "" {
			t.Errorf("%s 应写明 Vole 补充的依据", p)
		}
	}
	if !m["~/library/launchagents/com.foo.agent.plist"].Agent {
		t.Error("Program 指向 app 里面的 LaunchAgent 应标出")
	}
	if dc, ok := m[strings.ToLower(f.DarwinCache+"/com.foo.studio")]; !ok || dc.Kind != KindCache {
		t.Error("/var/folders 下的临时缓存应算缓存")
	}
	for _, p := range []string{"~/Library/LaunchAgents/com.other.plist", "~/Library/Logs/DiagnosticReports/Other_2026.ips",
		"~/Library/Application Support/Unrelated", "~/Library/Containers/com.foo.studio"} {
		if _, ok := m[strings.ToLower(p)]; ok {
			t.Errorf("不该找到 %s", p)
		}
	}
}

func TestFilesSafety(t *testing.T) {
	f := newFake(t)
	A, h := f.apps(), f.Home
	L := h + "/Library"
	// 独立命令行工具：卸 Claude.app 不碰 ~/.claude、~/.config/claude
	mkApp(t, A+"/Claude.app", "com.anthropic.claudefordesktop", nil)
	write(t, h+"/.config/claude/x", "cli")
	write(t, L+"/Application Support/Claude/x", "app")
	// 常见名：Helper.app 不按名字去 ~/.config 找
	mkApp(t, A+"/Helper.app", "com.x.helper", nil)
	write(t, h+"/.config/helper/x", "x")
	// 同家族：Zed.app 和 Zed Nightly.app 都装着，按名字找到的不要，按 bundle id 的留
	mkApp(t, A+"/Zed.app", "dev.zed.Zed", nil)
	mkApp(t, A+"/Zed Nightly.app", "dev.zed.Zed-Nightly", nil)
	write(t, L+"/Application Support/Zed/x", "shared")
	write(t, L+"/Caches/dev.zed.Zed-Nightly/x", "own")
	// 同 bundle id 还有别的副本（不同文件名，不去重）：只留系统级展示，数据不动
	mkApp(t, A+"/Copy.app", "com.copy.app", nil)
	mkApp(t, A+"/Copy 2.app", "com.copy.app", nil)
	write(t, L+"/Application Support/com.copy.app/x", "shared")
	// 受保护：Keychains
	mkApp(t, A+"/Keys.app", "com.keys.app", nil)
	write(t, L+"/Keychains/com.keys.app/x", "secret")
	// symlink 不收
	mkApp(t, A+"/Link.app", "com.link.app", nil)
	_ = os.MkdirAll(filepath.Join(f.Home, "outside"), 0o755)
	_ = os.MkdirAll(L+"/Caches", 0o755)
	_ = os.Symlink(filepath.Join(f.Home, "outside"), L+"/Caches/com.link.app")

	list := must(List(context.Background(), f.Env, ListOptions{}))
	apps := byName(list)
	get := func(name string) map[string]File {
		return paths(Files(context.Background(), f.Env, apps[name], list), h)
	}
	claude := get("Claude.app")
	if _, ok := claude["~/.config/claude"]; ok {
		t.Error("~/.config/claude 是 Claude Code 命令行的，不该算进 Claude.app")
	}
	if _, ok := claude["~/library/application support/claude"]; !ok {
		t.Error("Claude.app 自己的数据应找到")
	}
	if _, ok := get("Helper.app")["~/.config/helper"]; ok {
		t.Error("常见名不按名字找 ~/.config")
	}
	nightly := get("Zed Nightly.app")
	if _, ok := nightly["~/library/application support/zed"]; ok {
		t.Error("同家族 app 还装着：按名字找到的共享目录不要")
	}
	if _, ok := nightly["~/library/caches/dev.zed.zed-nightly"]; !ok {
		t.Error("按 bundle id 找到的应保留")
	}
	if len(get("Copy.app")) != 0 {
		t.Error("同 bundle id 还有副本：数据不动")
	}
	if _, ok := get("Keys.app")["~/library/keychains/com.keys.app"]; ok {
		t.Error("Keychains 受保护")
	}
	if _, ok := get("Link.app")["~/library/caches/com.link.app"]; ok {
		t.Error("symlink 不收")
	}
}

func TestElectronNameFromAsar(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "E.app")
	// 手工拼一个最小的 asar：16 字节头 + 头部 JSON + package.json 内容
	pj := `{"name":"mihomo-party"}`
	hdrJSON := fmt.Sprintf(`{"files":{"package.json":{"size":%d,"offset":"0"}}}`, len(pj))
	jsonLen := uint32(len(hdrJSON))
	headerSize := 8 + jsonLen // pickle：4 字节长度 + 4 字节 json 长度 + json
	var b []byte
	le := func(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
	b = append(b, le(4)...)
	b = append(b, le(headerSize)...)
	b = append(b, le(headerSize-4)...)
	b = append(b, le(jsonLen)...)
	b = append(b, hdrJSON...)
	b = append(b, pj...)
	_ = os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o755)
	_ = os.WriteFile(filepath.Join(app, "Contents", "Resources", "app.asar"), b, 0o644)
	if n := electronName(app); n != "mihomo-party" {
		t.Fatalf("%q", n)
	}
}

func TestRunMovesToTrashAndQuits(t *testing.T) {
	f := newFake(t)
	A, h := f.apps(), f.Home
	L := h + "/Library"
	app := mkApp(t, A+"/Foo.app", "com.foo.app", nil)
	write(t, L+"/Caches/com.foo.app/c", "cache")
	write(t, L+"/Application Support/com.foo.app/db", "data")
	write(t, L+"/LaunchAgents/com.foo.app.plist", plist(map[string]any{"Label": "com.foo.app"}))
	keep := write(t, h+"/Documents/important.txt", "keep")

	list := must(List(context.Background(), f.Env, ListOptions{Sizes: true}))
	foo := byName(list)["Foo.app"]
	files := Files(context.Background(), f.Env, foo, list)
	var pick []File
	for _, file := range files {
		if file.Default || file.Agent {
			pick = append(pick, file)
		}
	}
	// 正在运行，用户没同意退出：跳过
	f.procs = []Proc{{PID: 42, Path: app + "/Contents/MacOS/Foo"}}
	rep := Run(context.Background(), f.Env, []Plan{{foo, pick}}, RunOptions{})
	if o := rep.Outcomes[0]; o.Status != StatusSkipped || o.Reason != "正在运行" {
		t.Fatalf("%+v", o)
	}
	// dry-run：什么都不动
	rep = Run(context.Background(), f.Env, []Plan{{foo, pick}}, RunOptions{DryRun: true, Quit: true})
	if o := rep.Outcomes[0]; o.Status != StatusWould || !strings.Contains(o.Reason, "先退出") || !exists(app) {
		t.Fatalf("%+v", o)
	}
	// 同意退出：正常退出生效
	f.onQuit = func(id string) {
		if id == "com.foo.app" {
			f.mu.Lock()
			f.procs = nil
			f.mu.Unlock()
		}
	}
	rep = Run(context.Background(), f.Env, []Plan{{foo, pick}}, RunOptions{Quit: true})
	o := rep.Outcomes[0]
	if o.Status != StatusDone || exists(app) || exists(L+"/Caches/com.foo.app") || exists(L+"/LaunchAgents/com.foo.app.plist") {
		t.Fatalf("应移到废纸篓：%+v", o)
	}
	if !exists(h+"/.Trash/Foo.app") || !exists(h+"/.Trash/com.foo.app") {
		t.Fatal("应在临时家目录的废纸篓里")
	}
	if !exists(L+"/Application Support/com.foo.app") || !exists(keep) {
		t.Fatal("没勾的个人数据和无关文件不能动")
	}
	if len(f.signals) != 0 {
		t.Fatalf("正常退出了就不该发信号：%v", f.signals)
	}
	ran := fmt.Sprint(f.ran)
	if !strings.Contains(ran, "bootout") || !strings.Contains(ran, "lsregister -u") {
		t.Fatalf("应卸载 LaunchAgent、注销 LaunchServices：%s", ran)
	}
	if b, _ := os.ReadFile(filepath.Join(f.LogDir, "operations.log")); !strings.Contains(string(b), "uninstall\t已移到废纸篓") {
		t.Fatalf("操作日志：%s", b)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestQuitEscalatesAndSkipsWhenStuck(t *testing.T) {
	f := newFake(t)
	app := mkApp(t, f.apps()+"/Stuck.app", "com.stuck.app", nil)
	list := must(List(context.Background(), f.Env, ListOptions{}))
	a := byName(list)["Stuck.app"]
	f.procs = []Proc{{PID: 7, Path: app + "/Contents/MacOS/Stuck"}} // 怎么都不退
	start := time.Now()
	o := Run(context.Background(), f.Env, []Plan{{App: a}}, RunOptions{Quit: true}).Outcomes[0]
	if o.Status != StatusSkipped || !strings.Contains(o.Reason, "没能退出") || !exists(app) {
		t.Fatalf("退不掉应跳过不删：%+v", o)
	}
	if got := strings.Join(f.signals, ","); got != "7:terminated,7:killed" {
		t.Fatalf("应先 TERM 再 KILL：%s", got)
	}
	if time.Since(start) < f.QuitWait+2*f.KillWait {
		t.Fatal("每一步都要等够时间")
	}
}

func TestRunRechecksAndAdmin(t *testing.T) {
	f := newFake(t)
	A := f.apps()
	app := mkApp(t, A+"/Foo.app", "com.foo.app", nil)
	list := must(List(context.Background(), f.Env, ListOptions{}))
	foo := byName(list)["Foo.app"]
	// 扫描后被换成别的 app
	_ = os.RemoveAll(app)
	mkApp(t, app, "com.evil.app", nil)
	if o := Run(context.Background(), f.Env, []Plan{{App: foo}}, RunOptions{}).Outcomes[0]; o.Status != StatusSkipped {
		t.Fatalf("身份变了应跳过：%+v", o)
	}
	// 自己不能卸
	self := mkApp(t, A+"/Vole.app", SelfBundleID, nil)
	list = must(List(context.Background(), f.Env, ListOptions{}))
	if o := Run(context.Background(), f.Env, []Plan{{App: byName(list)["Vole.app"]}}, RunOptions{}).Outcomes[0]; o.Status != StatusSkipped || !exists(self) {
		t.Fatalf("%+v", o)
	}
	// 要管理员权限、又不允许用 Finder：失败，不动
	mkApp(t, A+"/Locked/Root.app", "com.root.app", nil)
	_ = os.Chmod(A+"/Locked", 0o555)
	t.Cleanup(func() { _ = os.Chmod(A+"/Locked", 0o755) })
	list = must(List(context.Background(), f.Env, ListOptions{}))
	root := byName(list)["Root.app"]
	if !root.NeedsAdmin {
		t.Fatal("应需要管理员权限")
	}
	if o := Run(context.Background(), f.Env, []Plan{{App: root}}, RunOptions{}).Outcomes[0]; o.Status != StatusFailed || !strings.Contains(o.Reason, "管理员") {
		t.Fatalf("%+v", o)
	}
}

func TestFinderErrors(t *testing.T) {
	for in, want := range map[string]string{
		"移到废纸篓失败：execution error: Not authorized to send Apple events to Finder. (-1743)": "自动化",
		"移到废纸篓失败：execution error: User canceled. (-128)":                                  "已取消",
		"移到废纸篓超时": "超时",
	} {
		if got := finderError(errors.New(in)).Error(); !strings.Contains(got, want) {
			t.Fatalf("%s → %s", in, got)
		}
	}
}

func TestHomebrewGoesThroughBrew(t *testing.T) {
	f := newFake(t)
	A := f.apps()
	app := mkApp(t, A+"/Brew.app", "com.brew.app", nil)
	mkApp(t, f.CaskRoots[0]+"/brewapp/1.0/Brew.app", "com.brew.app", nil)
	f.LookPath = func(name string) (string, error) { return "/fake/brew", nil }
	run := f.Run
	f.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "/fake/brew" {
			if strings.Join(args, " ") != "uninstall --cask brewapp" {
				return nil, errors.New("不该加 --zap：" + strings.Join(args, " "))
			}
			return nil, os.RemoveAll(app)
		}
		return run(ctx, name, args...)
	}
	list := must(List(context.Background(), f.Env, ListOptions{}))
	o := Run(context.Background(), f.Env, []Plan{{App: byName(list)["Brew.app"]}}, RunOptions{}).Outcomes[0]
	if o.Status != StatusDone || exists(app) {
		t.Fatalf("%+v", o)
	}
}

func TestProductRules(t *testing.T) {
	f := newFake(t)
	A, h := f.apps(), f.Home
	L := h + "/Library"
	mkApp(t, A+"/Docker.app", "com.docker.docker", nil)
	write(t, h+"/.docker/buildx/x", "cache")
	write(t, h+"/.docker/config.json", "auth") // 登录凭据：不碰
	mkApp(t, A+"/Visual Studio Code.app", "com.microsoft.VSCode", nil)
	write(t, L+"/Application Support/Code/User/settings.json", "{}")
	write(t, h+"/.vscode/extensions/x", "x")
	mkApp(t, A+"/GoLand.app", "com.jetbrains.goland", nil)
	write(t, L+"/Application Support/JetBrains/GoLand2025.2/x", "x")
	write(t, L+"/Application Support/JetBrains/GoLandBackup/x", "x") // 不是「产品名 + 版本」：不碰
	list := must(List(context.Background(), f.Env, ListOptions{}))
	m := byName(list)
	get := func(n string) map[string]File { return paths(Files(context.Background(), f.Env, m[n], list), h) }
	docker := get("Docker.app")
	if _, ok := docker["~/.docker/buildx"]; !ok {
		t.Error("Docker 的 buildx 缓存应找到")
	}
	if _, ok := docker["~/.docker"]; ok {
		t.Error("~/.docker 整个不能算（有登录凭据）")
	}
	code := get("Visual Studio Code.app")
	for _, p := range []string{"~/library/application support/code", "~/.vscode"} {
		if _, ok := code[p]; !ok {
			t.Errorf("VS Code 应找到 %s", p)
		}
	}
	jb := get("GoLand.app")
	if _, ok := jb["~/library/application support/jetbrains/goland2025.2"]; !ok {
		t.Error("JetBrains 的版本目录应找到")
	}
	if _, ok := jb["~/library/application support/jetbrains/golandbackup"]; ok {
		t.Error("同前缀但不是版本号的目录不碰")
	}
}
