// SPDX-License-Identifier: GPL-3.0-only
// 关联文件查找参考自 tw93/Mole lib/core/app_protection.sh（commit 6d1af75，GPL-3.0）：find_app_files（:1330 起）、
// 名字变体（:1337）、常见名（:744）、厂商目录（:759、:889）、独立命令行工具（:1035）、LaunchAgent 归属（:839）、
// app 内嵌的 bundle id（:1124）、find_app_system_files（:2290）、崩溃报告（:2200）；分类参考 lib/uninstall/review.sh（:4）；
// 共享数据判断参考 lib/uninstall/batch.sh（:990-1044、:2006-2116）。用 Go 重写，只删用户级。
// 「Vole 补充」的几条规则（entitlements、Electron 包名、内嵌 app 名、明确的点目录）是 Vole 自己加的，依据写在下面各条规则的注释里。

package apps

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"vole/internal/core/fsops"
	"vole/internal/i18n"
)

// Kind 是关联文件的分类（Mole review.sh）。
type Kind int

const (
	KindCache  Kind = iota // ~/Library/Caches、~/Library/Logs 下的：默认勾
	KindData               // 其余（Application Support、Containers、Preferences……）：个人数据，默认不勾
	KindSystem             // 系统级（/Library 下的）：只列出，要管理员权限，Vole 不删
)

func (k Kind) String() string {
	switch k {
	case KindCache:
		return "缓存和日志"
	case KindData:
		return "个人数据"
	}
	return "系统级（不处理）"
}

// File 是 app 的一个关联文件或目录。
type File struct {
	Path    string
	Kind    Kind
	Size    int64
	Default bool   // 默认勾不勾
	Extra   string // 非空：Vole 补充规则找到的（Mole 命令行版找不到），写明依据
	Agent   bool   // 用户级 LaunchAgent（卸载前先 launchctl bootout）
	Ident   fsops.Identity
}

// Files 找一个 app 的关联文件（只读），按大小降序。installed 是当前已装的 app（判断共享数据用）。
// 同一个 bundle id 还有别的副本装着时，只返回系统级的展示项：数据是共享的，卸载只移走这一份 app。
func Files(ctx context.Context, env *Env, app App, installed []App) []File {
	f := finder{ctx: ctx, env: env, app: app, seen: map[string]bool{}}
	f.name = strings.TrimSuffix(filepath.Base(app.Path), filepath.Ext(app.Path)) // Mole 用文件名，不用显示名
	f.bidOK = reverseDNS.MatchString(app.BundleID) && !strings.Contains(app.BundleID, "..")
	shared := otherCopy(app, installed)
	family := familyInstalled(app, installed)
	if !shared {
		f.user(family)
		f.products()
		f.extras(installed)
	}
	f.system()
	return f.finish()
}

var reverseDNS = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9_-]*)+$`)

type finder struct {
	ctx   context.Context
	env   *Env
	app   App
	name  string
	bidOK bool
	seen  map[string]bool
	out   []File
}

func (f *finder) lib(parts ...string) string {
	return filepath.Join(append([]string{f.env.Home, "Library"}, parts...)...)
}

// add 收一个候选路径：必须存在、不是 symlink、过路径校验和卸载保护，重复的、根目录的、独立命令行工具的都跳过。
func (f *finder) add(p string, kind Kind, extra string) {
	p = filepath.Clean(p)
	if f.seen[p] {
		return
	}
	f.seen[p] = true
	info, err := os.Lstat(p)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	if kind != KindSystem {
		if f.rootDir(p) || independentCLI(p, f.env.Home) || protectedForUninstall(p, f.env.Home) || containerStub(p) {
			return
		}
		under := f.env.Home
		if f.env.DarwinCache != "" && strings.HasPrefix(p, f.env.DarwinCache+"/") {
			under = f.env.DarwinCache
		}
		if fsops.CheckPath(p, under) != nil {
			return
		}
	}
	file := File{Path: p, Kind: kind, Extra: extra}
	if kind == KindData || kind == KindCache {
		file.Kind = f.classify(p)
		if extra != "" {
			file.Kind = KindData // 补充规则找到的一律算个人数据
		}
	}
	file.Default = file.Kind == KindCache
	f.out = append(f.out, file)
}

// classify：只有 ~/Library/Caches、~/Library/Logs（和 /var/folders 下的临时缓存）下的算缓存和日志，其余都是个人数据（Mole review.sh:4-14）。
// Spotify、Raycast、Mullvad 的「缓存」里有离线下载、扩展数据，Mole 也按个人数据处理。
func (f *finder) classify(p string) Kind {
	for _, keep := range []string{"com.spotify.client", "com.raycast.macos", "net.mullvad.vpn"} {
		if strings.Contains(p, keep) {
			return KindData
		}
	}
	for _, root := range []string{f.lib("Caches"), f.lib("Logs")} {
		if strings.HasPrefix(p, root+"/") {
			return KindCache
		}
	}
	if f.env.DarwinCache != "" && strings.HasPrefix(p, f.env.DarwinCache+"/") {
		return KindCache
	}
	return KindData
}

// rootDir：拼出来的路径正好是 Application Support、~/.config 这类根目录时跳过（Mole :1570）。
func (f *finder) rootDir(p string) bool {
	h := f.env.Home
	switch p {
	case h, filepath.Join(h, "Library"), filepath.Join(h, ".config"), filepath.Join(h, ".cache"), filepath.Join(h, ".local"),
		filepath.Join(h, ".local", "share"), f.env.DarwinCache:
		return true
	}
	return filepath.Dir(p) == filepath.Join(h, "Library") // ~/Library/<任何一级目录> 本身
}

// independentCLI：claude、opencode、codex、gemini 这些独立命令行工具的目录不属于同名的桌面 app（Mole :1035，#993）。
func independentCLI(p, home string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Base(p), ".")) {
	case "claude", "opencode", "codex", "gemini":
	default:
		return false
	}
	switch filepath.Dir(p) {
	case home, filepath.Join(home, ".config"), filepath.Join(home, ".local", "share"), filepath.Join(home, ".cache"):
		return true
	}
	return false
}

// protectedForUninstall：卸载时仍然要保护的（Mole should_protect_path 在卸载模式下保留的部分，:425-637）。
func protectedForUninstall(p, home string) bool {
	lib := filepath.Join(home, "Library") + "/"
	for _, d := range []string{"Accounts", "Keychains", "Mail", "Calendars", "Contacts", "Mobile Documents", "Messages",
		"Application Support/AddressBook", "Application Support/MobileSync", "Group Containers/group.com.apple.notes"} {
		if p == lib+d || strings.HasPrefix(p, lib+d+"/") {
			return true
		}
	}
	for _, s := range []string{"/Mobile Documents", "com.apple.Settings", "com.apple.SystemSettings", "com.apple.systempreferences",
		"com.apple.controlcenter", "com.apple.finder", "com.apple.dock", ".orbstack", "dev.orbstack"} {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

// containerStub：只剩系统元数据的 Container 空壳，系统会留着，不报告（Mole batch.sh:713）。
func containerStub(p string) bool {
	if !strings.Contains(p, "/Library/Containers/") {
		return false
	}
	es, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	for _, e := range es {
		if e.Name() != ".com.apple.containermanagerd.metadata.plist" {
			return false
		}
	}
	return true
}

// names：app 名的变体（Mole :1337-1353）。base 是去掉 Nightly、Beta 等渠道后缀的名字。
func names(name string) (all []string, base string) {
	nospace := strings.ReplaceAll(name, " ", "")
	underscore := strings.ReplaceAll(name, " ", "_")
	hyphen := strings.ReplaceAll(name, " ", "-")
	all = []string{name, nospace, underscore, hyphen, strings.ToLower(name), strings.ToLower(nospace),
		strings.ToLower(hyphen), strings.ToLower(underscore)}
	base = name
	if m := channelSuffix.FindStringSubmatch(name); m != nil {
		base = m[1]
		all = append(all, base, strings.ToLower(base))
	}
	return dedupStrings(all), base
}

var channelSuffix = regexp.MustCompile(`^(.+)\s+(Nightly|Beta|Alpha|Dev|Canary|Preview|Insider|Edge|Stable|Release|RC|LTS|Developer Edition|Technology Preview)$`)

func dedupStrings(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" && !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// commonName：名字是常见词时，关掉按名字的模糊匹配（Mole :744）。
func commonName(n string) bool {
	switch strings.ToLower(n) {
	case "music", "notes", "photos", "finder", "safari", "preview", "calendar", "contacts", "messages", "reminders", "clock",
		"weather", "stocks", "books", "news", "podcasts", "voice", "files", "store", "system", "helper", "agent", "daemon",
		"service", "update", "sync", "backup", "cloud", "manager", "monitor", "server", "client", "worker", "runner",
		"launcher", "driver", "plugin", "extension", "widget", "utility", "electron":
		return true
	}
	return false
}

// user 找用户级的关联文件。family 为 true 时（同家族 app 还装着），按名字找到的不要，只留按 bundle id 找到的。
func (f *finder) user(family bool) {
	h, bid, lib := f.env.Home, f.app.BundleID, f.lib
	if !family && len(f.name) >= 2 {
		all, base := names(f.name)
		for _, n := range append(all, base) {
			for _, p := range []string{
				lib("Application Support", n), lib("Caches", n), lib("Logs", n), lib("Preferences", n), lib("Preferences", n+".plist"),
				lib("Saved Application State", n+".savedState"), lib("Services", n+".workflow"), lib("QuickLook", n+".qlgenerator"),
				lib("Internet Plug-Ins", n+".plugin"), lib("PreferencePanes", n+".prefPane"), lib("Input Methods", n+".app"),
				lib("Screen Savers", n+".saver"), lib("Spotlight", n+".mdimporter"), lib("Workflows", n+".workflow"),
				lib("Audio", "Plug-Ins", "Components", n+".component"), lib("Audio", "Plug-Ins", "VST", n+".vst"),
				lib("Audio", "Plug-Ins", "VST3", n+".vst3"),
			} {
				f.add(p, KindData, "")
			}
			// 按名字只认 ~/.config、~/.cache、~/.local/share 下的；不从显示名推家目录下的点目录（Mole :1365 的注释：Docker、Local 这类名字会撞上命令行工具）
			if !commonName(n) {
				for _, d := range []string{".config", ".cache", filepath.Join(".local", "share")} {
					f.add(filepath.Join(h, d, n), KindData, "")
				}
			}
		}
		if len(f.name) >= 3 && !commonName(f.name) && strings.ToLower(f.name) != "electron" {
			f.add(lib("Caches", "SentryCrash", f.name), KindCache, "")
			f.add(lib("Caches", strings.ToLower(strings.ReplaceAll(f.name, " ", "-"))+"-updater"), KindCache, "")
			f.vendorDirs()
		}
	}
	if !f.bidOK {
		return
	}
	for _, id := range append([]string{bid}, f.embeddedIDs()...) {
		f.byBundleID(id)
	}
	f.add(lib("Caches", bid+".ShipIt"), KindCache, "")
	f.add(lib("Caches", "com.plausiblelabs.crashreporter.data", bid), KindCache, "")
	f.add(lib("Caches", "com.crashlytics.data", bid), KindCache, "")
	f.add(lib("WebKit", "com.apple.WebKit.WebContent", bid), KindData, "")
	if f.env.DarwinCache != "" {
		f.add(filepath.Join(f.env.DarwinCache, bid), KindCache, "")
	}
	// Group Containers、Application Scripts、Containers 下：等于 bid、以 bid. 开头、或以 .bid 结尾（含 TEAMID.bid）（Mole :1658）
	for _, dir := range []string{"Group Containers", "Application Scripts", "Containers"} {
		f.matchEntries(lib(dir), func(n string) bool {
			return n == bid || strings.HasPrefix(n, bid+".") || strings.HasSuffix(n, "."+bid)
		})
	}
	// Preferences/ByHost 下以 bid. 开头的（Mole :1634）
	f.matchEntries(lib("Preferences", "ByHost"), func(n string) bool { return strings.HasPrefix(n, bid+".") })
	f.launchAgents()
	f.crashReports()
}

// byBundleID：按一个 bundle id 拼出的位置（Mole :1406-1435）。
func (f *finder) byBundleID(id string) {
	lib := f.lib
	for _, p := range []string{
		lib("Application Support", id), lib("Caches", id), lib("Logs", id), lib("Containers", id), lib("WebKit", id),
		lib("HTTPStorages", id), lib("HTTPStorages", id+".binarycookies"), lib("Cookies", id+".binarycookies"),
		lib("Application Scripts", id), lib("Autosave Information", id), lib("SyncedPreferences", id+".plist"),
		lib("Saved Application State", id+".savedState"), lib("Preferences", id+".plist"), lib("Preferences", id),
		lib("Group Containers", id),
	} {
		f.add(p, KindData, "")
	}
}

func (f *finder) matchEntries(dir string, match func(string) bool) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range es {
		if match(e.Name()) {
			f.add(filepath.Join(dir, e.Name()), KindData, "")
		}
	}
}

// vendorDirs：Application Support|Caches|Logs/<厂商>/<产品或 app 名>（Mole :889-942）。
func (f *finder) vendorDirs() {
	parts := strings.Split(f.app.BundleID, ".")
	if !f.bidOK || len(parts) < 3 {
		return
	}
	vendor, product := parts[len(parts)-2], parts[len(parts)-1]
	if len(vendor) < 3 || len(product) < 3 {
		return
	}
	all, _ := names(f.name)
	for _, root := range []string{"Application Support", "Caches", "Logs"} {
		for _, d := range []string{vendor, strings.ToLower(vendor)} {
			es, err := os.ReadDir(f.lib(root, d))
			if err != nil {
				continue
			}
			for _, e := range es {
				lower := strings.ToLower(e.Name())
				for _, v := range append(all, product, strings.ToLower(product)) {
					v = strings.ToLower(v)
					if lower == v || strings.HasPrefix(lower, v+" ") || strings.HasPrefix(lower, v+"-") ||
						strings.HasPrefix(lower, v+"_") || strings.HasPrefix(lower, v+".") {
						f.add(filepath.Join(f.lib(root, d), e.Name()), KindData, "")
						break
					}
				}
			}
		}
	}
}

// embeddedIDs：app 内嵌的登录项、XPC、扩展、helper app 的 bundle id（Mole :1124）。
func (f *finder) embeddedIDs() []string {
	var ids []string
	for _, b := range f.embedded() {
		if id := bundleIDOf(f.ctx, f.env, b); reverseDNS.MatchString(id) && id != f.app.BundleID && !strings.HasPrefix(id, "com.apple.") {
			ids = append(ids, id)
		}
	}
	return dedupStrings(ids)
}

// embedded：app 里面的 bundle（登录项、XPC、扩展、helper app）。
func (f *finder) embedded() []string {
	c := filepath.Join(f.app.Path, "Contents")
	var out []string
	for _, g := range []string{"Library/LoginItems/*.app", "XPCServices/*.xpc", "PlugIns/*.appex", "MacOS/*.app", "Helpers/*.app"} {
		ms, _ := filepath.Glob(filepath.Join(c, g))
		out = append(out, ms...)
	}
	return out
}

// launchAgents：~/Library/LaunchAgents 下文件名是 <bid>.plist，或 Program 指向这个 app 里面的（Mole :839、:1825）。
func (f *finder) launchAgents() {
	dir := f.lib("LaunchAgents")
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range es {
		p := filepath.Join(dir, e.Name())
		if !strings.HasSuffix(e.Name(), ".plist") || strings.HasPrefix(e.Name(), "com.apple.") {
			continue
		}
		owned := e.Name() == f.app.BundleID+".plist"
		if !owned {
			owned = agentProgramIn(f.ctx, f.env, p, f.app.Path)
		}
		if owned {
			n := len(f.out)
			f.add(p, KindData, "")
			if len(f.out) > n {
				f.out[n].Agent = true
			}
		}
	}
}

// agentProgramIn：LaunchAgent plist 的 Program（或 ProgramArguments 第一项）在 app 里面。
func agentProgramIn(ctx context.Context, env *Env, plist, app string) bool {
	m, err := readPlist(ctx, env, plist)
	if err != nil {
		return false
	}
	prog := str(m, "Program")
	if prog == "" {
		if args, ok := m["ProgramArguments"].([]any); ok && len(args) > 0 {
			prog, _ = args[0].(string)
		}
	}
	return prog != "" && strings.HasPrefix(filepath.Clean(prog), app+"/")
}

// crashReports：~/Library/Logs/DiagnosticReports 下 <可执行文件名>[ Helper] 开头的崩溃报告（Mole :2200）。
func (f *finder) crashReports() {
	prefix := f.app.Executable
	if prefix == "" {
		prefix = f.name
	}
	if len(prefix) < 3 || strings.EqualFold(prefix, "electron") {
		return
	}
	dir := f.lib("Logs", "DiagnosticReports")
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range es {
		n := e.Name()
		ext := filepath.Ext(n)
		if ext != ".ips" && ext != ".crash" && ext != ".spin" && ext != ".diag" {
			continue
		}
		for _, p := range []string{prefix, prefix + " Helper"} {
			if rest, ok := strings.CutPrefix(n, p); ok && rest != "" && strings.ContainsRune("._-", rune(rest[0])) {
				f.add(filepath.Join(dir, n), KindCache, "")
				break
			}
		}
	}
}

// system 找系统级的（只列出，Vole 不删；Mole 也只展示，batch.sh:2285）。
func (f *finder) system() {
	bid := f.app.BundleID
	for _, n := range []string{f.name, bid} {
		if n == "" || (n == bid && !f.bidOK) {
			continue
		}
		for _, d := range []string{"Application Support", "Caches", "Logs", "Preferences"} {
			f.add(filepath.Join("/Library", d, n), KindSystem, "")
		}
	}
	if !f.bidOK {
		return
	}
	for _, dir := range []string{"/Library/LaunchAgents", "/Library/LaunchDaemons", "/Library/PrivilegedHelperTools", "/private/var/db/receipts"} {
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			if strings.HasPrefix(e.Name(), bid+".") || e.Name() == bid {
				f.add(filepath.Join(dir, e.Name()), KindSystem, "")
			}
		}
	}
}

// ── Vole 补充规则（Mole 命令行版没有；全部算个人数据、默认不勾）──

// dotDirs：明确知道属于某个 app 的家目录点目录（Mole 不从名字推点目录，要逐个写明）。
var dotDirs = map[string][]string{
	"com.todesktop.230313mzl4w4u92": {".cursor"}, // Cursor：扩展和设置
}

func (f *finder) extras(installed []App) {
	others := otherNames(f.app, installed)
	// 1. 签名里声明的 App Group：~/Library/Group Containers/<group>（Docker 的 group.com.docker、百度网盘的 group.com.baidu.BaiduNetdisk-Mac）
	for _, g := range appGroups(f.ctx, f.env, f.app.Path) {
		if !sharedGroup(g, f.app, installed) {
			f.add(f.lib("Group Containers", g), KindData, i18n.T("签名里声明的 App Group：")+g)
		}
	}
	// 2. Electron 的包名：Electron 默认把数据放在 Application Support/<package.json 的 productName 或 name>（mihomo-party、baidunetdisk）
	if n := electronName(f.app.Path); n != "" && !others[strings.ToLower(n)] {
		for _, d := range []string{"Application Support", "Caches", "Logs"} {
			f.add(f.lib(d, n), KindData, i18n.T("Electron 包名：")+n)
		}
	}
	// 3. 内嵌 app 的名字：Application Support/<内嵌 app 的 CFBundleName>（Docker.app 里的 Docker Desktop.app）
	for _, b := range f.embedded() {
		if !strings.HasSuffix(b, ".app") {
			continue
		}
		info, err := readPlist(f.ctx, f.env, filepath.Join(b, "Contents", "Info.plist"))
		if err != nil {
			continue
		}
		n := str(info, "CFBundleName")
		if n == "" || len(n) < 3 || commonName(n) || strings.Contains(strings.ToLower(n), "helper") || others[strings.ToLower(n)] {
			continue
		}
		f.add(f.lib("Application Support", n), KindData, i18n.T("内嵌的 ")+filepath.Base(b))
	}
	// 4. 明确的点目录
	for _, d := range dotDirs[f.app.BundleID] {
		f.add(filepath.Join(f.env.Home, d), KindData, i18n.T("这个 app 的扩展和设置目录"))
	}
}

// otherNames：别的已装 app 的名字（小写），补充规则推出的名字和它们撞上时不要。
func otherNames(app App, installed []App) map[string]bool {
	m := map[string]bool{}
	for _, a := range installed {
		if a.Path == app.Path {
			continue
		}
		m[strings.ToLower(a.Name)] = true
		m[strings.ToLower(strings.TrimSuffix(filepath.Base(a.Path), ".app"))] = true
	}
	return m
}

// appGroups 读 app 签名里的 com.apple.security.application-groups（codesign -d --entitlements）。
func appGroups(ctx context.Context, env *Env, app string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := env.Run(ctx, "/usr/bin/codesign", "-d", "--entitlements", "-", "--xml", app)
	if err != nil || len(out) == 0 {
		return nil
	}
	tmp, err := os.CreateTemp("", "vole-ent-*.plist")
	if err != nil {
		return nil
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.Write(out)
	tmp.Close()
	m, err := readPlist(ctx, env, tmp.Name())
	if err != nil {
		return nil
	}
	var gs []string
	if arr, ok := m["com.apple.security.application-groups"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && reverseDNS.MatchString(s) {
				gs = append(gs, s)
			}
		}
	}
	return gs
}

// sharedGroup：去掉 group. / TEAMID. 前缀后，别的已装 app 的 bundle id 也在这个域下，就是共享的。
func sharedGroup(g string, app App, installed []App) bool {
	id := strings.TrimPrefix(g, "group.")
	if i := strings.Index(id, "."); i > 0 && strings.ToUpper(id[:i]) == id[:i] && len(id[:i]) == 10 {
		id = id[i+1:] // TEAMID.com.foo
	}
	for _, a := range installed {
		if a.Path != app.Path && a.BundleID != app.BundleID && (a.BundleID == id || strings.HasPrefix(a.BundleID, id+".")) {
			return true
		}
	}
	return false
}

// electronName 读 Electron app 的 package.json（app.asar 里或 Resources/app/ 下）的 productName，没有就用 name。
func electronName(app string) string {
	res := filepath.Join(app, "Contents", "Resources")
	var pj []byte
	if b, err := os.ReadFile(filepath.Join(res, "app", "package.json")); err == nil {
		pj = b
	} else {
		pj = asarFile(filepath.Join(res, "app.asar"), "package.json")
	}
	if pj == nil {
		return ""
	}
	var p struct{ Name, ProductName string }
	if json.Unmarshal(pj, &p) != nil {
		return ""
	}
	n := strings.TrimSpace(firstNonEmpty(p.ProductName, p.Name))
	if len(n) < 3 || commonName(n) || strings.ContainsAny(n, "/\\") || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "@") {
		return ""
	}
	return n
}

// asarFile 从 asar 包里读一个顶层文件（只读头部 JSON 和这一个文件，限 1MB）。
// asar 格式：16 字节头（两段 pickle 长度）+ 头部 JSON + 文件内容；文件偏移从头部之后算起。
func asarFile(archive, name string) []byte {
	f, err := os.Open(archive)
	if err != nil {
		return nil
	}
	defer f.Close()
	var hdr [16]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return nil
	}
	headerSize := binary.LittleEndian.Uint32(hdr[4:8])
	jsonSize := binary.LittleEndian.Uint32(hdr[12:16])
	if jsonSize == 0 || jsonSize > 64<<20 {
		return nil
	}
	buf := make([]byte, jsonSize)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil
	}
	var h struct {
		Files map[string]struct {
			Offset string `json:"offset"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if json.Unmarshal(buf, &h) != nil {
		return nil
	}
	e, ok := h.Files[name]
	if !ok || e.Size <= 0 || e.Size > 1<<20 {
		return nil
	}
	var off int64
	for _, c := range e.Offset {
		if c < '0' || c > '9' {
			return nil
		}
		off = off*10 + int64(c-'0')
	}
	out := make([]byte, e.Size)
	if _, err := f.ReadAt(out, 8+int64(headerSize)+off); err != nil {
		return nil
	}
	return out
}

// otherCopy：同一个 bundle id 还有别的副本装着（Mole batch.sh:2006）——数据是共享的，只卸本体。
func otherCopy(app App, installed []App) bool {
	if app.BundleID == "" {
		return false
	}
	for _, a := range installed {
		if a.Path != app.Path && a.BundleID == app.BundleID {
			return true
		}
	}
	return false
}

// familyInstalled：同家族的 app 还装着（如 Zed.app 和 Zed Nightly.app；或 bundle id 互为点号延续）（Mole batch.sh:990-1026）。
func familyInstalled(app App, installed []App) bool {
	_, base := names(strings.TrimSuffix(filepath.Base(app.Path), ".app"))
	for _, a := range installed {
		if a.Path == app.Path {
			continue
		}
		_, b := names(strings.TrimSuffix(filepath.Base(a.Path), ".app"))
		if strings.EqualFold(b, base) {
			return true
		}
		if app.BundleID != "" && a.BundleID != "" && (strings.HasPrefix(a.BundleID, app.BundleID+".") || strings.HasPrefix(app.BundleID, a.BundleID+".")) {
			return true
		}
	}
	return false
}

// finish：去掉被别的项包含的子路径，算大小和身份，按大小降序。
func (f *finder) finish() []File {
	sort.Slice(f.out, func(i, j int) bool { return f.out[i].Path < f.out[j].Path })
	var out []File
	idents := map[fsops.Identity]bool{}
	for _, file := range f.out {
		if n := len(out); n > 0 && strings.HasPrefix(strings.ToLower(file.Path), strings.ToLower(out[n-1].Path)+"/") {
			continue
		}
		if file.Kind != KindSystem {
			file.Ident, _, _ = fsops.IdentityOf(file.Path)
			// APFS 默认不区分大小写：Application Support/Cursor 和 cursor 是同一个目录，只算一次
			if idents[file.Ident] {
				continue
			}
			idents[file.Ident] = true
			file.Size, _ = fsops.DiskUsage(f.ctx, file.Path)
		}
		out = append(out, file)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	return out
}
