// SPDX-License-Identifier: GPL-3.0-only
// 已装 app 清单参考自 tw93/Mole bin/uninstall.sh（commit 6d1af75，GPL-3.0）：扫描根目录（:525）、跳过规则（:557）、
// 后台专用 app（:625、:656）、去重（:1132）、显示名（:175）、上次使用的兜底（:1347）；Homebrew 判断参考 lib/uninstall/brew.sh（:308）、
// Steam 启动壳参考 lib/uninstall/steam.sh。用 Go 重写。

package apps

import (
	"context"
	"debug/macho"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"vole/internal/core/fsops"
	"vole/internal/i18n"
)

// ListOptions 控制清单。
type ListOptions struct {
	Sizes bool // 算每个 app 的大小（du 口径，本机 41 个约 3 秒）
}

// List 列出已装的 app（只读），按大小降序。系统关键 app 不列；Vole 自己和要用厂商卸载器的列出但 Blocked。
func List(ctx context.Context, env *Env, opts ListOptions) ([]App, error) {
	paths := discover(env)
	var procs []Proc
	if env.Processes != nil {
		procs, _ = env.Processes(ctx)
	}
	apps := make([]App, 0, len(paths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, d := range paths {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			a, ok := inspect(ctx, env, d.path, d.root, opts)
			if !ok {
				return
			}
			a.Running = runningIn(procs, a.Path)
			mu.Lock()
			apps = append(apps, a)
			mu.Unlock()
		})
	}
	wg.Wait()
	apps = dedupe(apps, env)
	sort.SliceStable(apps, func(i, j int) bool {
		if apps[i].Size != apps[j].Size {
			return apps[i].Size > apps[j].Size
		}
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	return apps, ctx.Err()
}

type found struct{ path, root string }

// discover 在扫描根目录下找 .app：深度 3，不跟随 symlink，跳过以 . 开头的目录（更新器暂存）和嵌套在别的 app 里的 app。
func discover(env *Env) []found {
	var out []found
	for _, root := range env.Roots {
		root = filepath.Clean(root)
		base := strings.Count(root, string(filepath.Separator))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root {
					return fs.SkipDir
				}
				return nil
			}
			if p == root {
				return nil
			}
			name := d.Name()
			isApp := strings.HasSuffix(strings.ToLower(name), ".app")
			if strings.HasPrefix(name, ".") && !isApp {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if isApp && (d.IsDir() || d.Type()&fs.ModeSymlink != 0) {
				if d.Type()&fs.ModeSymlink != 0 && symlinkToSystem(p) {
					return nil
				}
				out = append(out, found{p, root})
				if d.IsDir() {
					return fs.SkipDir // 不进 .app 里面：嵌套的 app 不单列
				}
				return nil
			}
			if d.IsDir() && strings.Count(p, string(filepath.Separator))-base >= 3 {
				return fs.SkipDir
			}
			return nil
		})
	}
	return out
}

// symlinkToSystem：指向系统路径的 symlink 不当成 app（Mole :557）。
func symlinkToSystem(p string) bool {
	t, err := filepath.EvalSymlinks(p)
	if err != nil {
		return true
	}
	for _, pre := range []string{"/System/", "/usr/bin/", "/usr/lib/", "/bin/", "/sbin/", "/private/etc/"} {
		if strings.HasPrefix(t+"/", pre) {
			return true
		}
	}
	return false
}

// inspect 读一个 .app 的信息；不该出现在列表里的返回 false。
func inspect(ctx context.Context, env *Env, p, root string, opts ListOptions) (App, bool) {
	a := App{Path: p}
	contents := filepath.Join(p, "Contents")
	info, err := readPlist(ctx, env, filepath.Join(contents, "Info.plist"))
	if err != nil {
		info = map[string]any{}
	}
	a.BundleID = str(info, "CFBundleIdentifier")
	a.Version = str(info, "CFBundleShortVersionString")
	a.Executable = str(info, "CFBundleExecutable")
	a.MenuBar = boolOf(info, "LSUIElement")
	file := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))

	// 后台专用 app：不在扫描根目录直接下一层就不列（Mole :625、:656）
	if boolOf(info, "LSBackgroundOnly") && filepath.Dir(p) != root {
		return a, false
	}
	if systemApp(a.BundleID) {
		return a, false
	}

	a.Name = localizedName(ctx, env, p, info)
	if a.Name == "" {
		a.Name = firstNonEmpty(str(info, "CFBundleDisplayName"), str(info, "CFBundleName"), file)
	}
	// 文件名是「显示名 + 带数字的后缀」时用文件名，保住版本区分（如 Xcode-16.2）（Mole :288）
	if strings.HasPrefix(file, a.Name) && len(file) > len(a.Name) && strings.ContainsAny(file[len(a.Name):], "0123456789") {
		a.Name = file
	}

	switch {
	case a.BundleID == env.SelfID && env.SelfID != "":
		a.Blocked = i18n.T("Vole 正在使用，不能卸载自己")
	case officialVendor(a.BundleID, a.Name, file) != "":
		a.Blocked = i18n.Tf("请用 %s 自己的卸载器", officialVendor(a.BundleID, a.Name, file))
	}

	if id, _, err := fsops.IdentityOf(p); err == nil {
		a.Ident = id
	}
	a.Source, a.CaskToken = sourceOf(env, p, a.Executable)
	a.Intel = intelOnly(filepath.Join(contents, "MacOS", a.Executable))
	a.NeedsAdmin = needsAdmin(p)
	a.LastUsed, a.Installed = datesOf(ctx, env, p, a.Executable)
	if opts.Sizes && a.Source != SourceSteam {
		if n, err := fsops.DiskUsage(ctx, p); err == nil || n > 0 {
			a.Size, a.SizeKnown = n, true
		}
	}
	return a, true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// needsAdmin：移到废纸篓要改 app 所在目录，也要改 app 目录本身（目录换上级时要更新它的 ..），
// 两者有一个当前用户不可写，就只能交给 Finder（系统弹管理员密码框）。Mole 移到废纸篓模式只看父目录（batch.sh:2161）；
// Vole 多看一层，因为 App Store 装的 app 属主是 root，直接改名会失败。
func needsAdmin(p string) bool {
	return unix.Access(filepath.Dir(p), unix.W_OK) != nil || unix.Access(p, unix.W_OK) != nil
}

// sourceOf：App Store（有 _MASReceipt）、Steam 启动壳、Homebrew cask（在 Caskroom 里能唯一对上）。
func sourceOf(env *Env, p, exe string) (Source, string) {
	if _, err := os.Stat(filepath.Join(p, "Contents", "_MASReceipt", "receipt")); err == nil {
		return SourceAppStore, ""
	}
	if steamShell(filepath.Join(p, "Contents", "MacOS", exe)) {
		return SourceSteam, ""
	}
	if tok := caskToken(env, p); tok != "" {
		return SourceHomebrew, tok
	}
	return SourceOther, ""
}

var steamURL = regexp.MustCompile(`^\s*(exec\s+)?(/usr/bin/)?open\s+["']?steam://(run|rungameid|launch)/[0-9]+`)

// steamShell：Steam 生成的启动壳——可执行文件是不超过 4KB 的 sh/bash/zsh 脚本，只有一条 open steam://run/<id>（Mole steam.sh:19）。
func steamShell(exe string) bool {
	info, err := os.Stat(exe)
	if err != nil || info.Size() > 4096 {
		return false
	}
	b, err := os.ReadFile(exe)
	if err != nil || !strings.HasPrefix(string(b), "#!") {
		return false
	}
	lines := strings.Split(string(b), "\n")
	if !strings.Contains(lines[0], "sh") {
		return false
	}
	n := 0
	ok := false
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		n++
		ok = steamURL.MatchString(l)
	}
	return n == 1 && ok
}

// caskToken：app 解析后的真实路径落在 Caskroom/<token>/ 下，或 Caskroom 里只有一个 token 装了同名的 .app（Mole brew.sh:308、:321）。
// Mole 还会问 brew list / brew info 做核对，这里只看文件系统，卸载时再交给 brew 自己确认。
func caskToken(env *Env, p string) string {
	real, _ := filepath.EvalSymlinks(p)
	base := filepath.Base(p)
	var tokens []string
	for _, root := range env.CaskRoots {
		if rel, ok := strings.CutPrefix(real, root+"/"); ok {
			if tok, _, _ := strings.Cut(rel, "/"); tok != "" {
				return tok
			}
		}
		ms, _ := filepath.Glob(filepath.Join(root, "*", "*", base))
		for _, m := range ms {
			tok := filepath.Base(filepath.Dir(filepath.Dir(m)))
			if !contains(tokens, tok) {
				tokens = append(tokens, tok)
			}
		}
	}
	if len(tokens) == 1 {
		return tokens[0]
	}
	return ""
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// intelOnly：可执行文件里只有 x86_64（用 Go 读 Mach-O，不跑命令）。
func intelOnly(exe string) bool {
	if fat, err := macho.OpenFat(exe); err == nil {
		defer fat.Close()
		for _, a := range fat.Arches {
			if a.Cpu == macho.CpuArm64 {
				return false
			}
		}
		return len(fat.Arches) > 0
	}
	f, err := macho.Open(exe)
	if err != nil {
		return false
	}
	defer f.Close()
	return f.Cpu == macho.CpuAmd64
}

var validDate = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// datesOf：上次使用和安装日期。
//   - 先问 Spotlight（kMDItemLastUsedDate、kMDItemDateAdded，同 Mole）；
//   - 取不到（本机 Spotlight 对 /Applications 全部取不到）时：上次使用用可执行文件的访问时间，再退到 app 的修改时间（Mole :1347）；
//     安装日期用 app 目录的创建时间，早于 2001 年（zip 解压出来的常是 1980）就用状态变更时间。
func datesOf(ctx context.Context, env *Env, p, exe string) (lastUsed, installed time.Time) {
	qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	out, err := env.Run(qctx, "/usr/bin/mdls", "-raw", "-nullMarker", "", "-name", "kMDItemLastUsedDate", "-name", "kMDItemDateAdded", p)
	cancel()
	if err == nil {
		parts := strings.Split(string(out), "\x00")
		if len(parts) >= 2 {
			lastUsed, installed = mdDate(parts[0]), mdDate(parts[1])
		}
	}
	var st unix.Stat_t
	if lastUsed.IsZero() && exe != "" {
		if unix.Stat(filepath.Join(p, "Contents", "MacOS", exe), &st) == nil {
			lastUsed = valid(time.Unix(st.Atim.Unix()))
		}
	}
	if unix.Lstat(p, &st) == nil {
		if lastUsed.IsZero() {
			lastUsed = valid(time.Unix(st.Mtim.Unix()))
		}
		if installed.IsZero() {
			installed = valid(time.Unix(st.Btim.Unix()))
		}
		if installed.IsZero() {
			installed = valid(time.Unix(st.Ctim.Unix()))
		}
	}
	return lastUsed, installed
}

func mdDate(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05 -0700", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return valid(t)
}

func valid(t time.Time) time.Time {
	if t.Before(validDate) {
		return time.Time{}
	}
	return t
}

// runningIn：有进程的可执行文件在这个 .app 里。
func runningIn(procs []Proc, app string) bool {
	return len(pidsIn(procs, app)) > 0
}

func pidsIn(procs []Proc, app string) []int {
	var out []int
	prefix := app + "/"
	for _, p := range procs {
		if strings.HasPrefix(p.Path, prefix) {
			out = append(out, p.PID)
		}
	}
	return out
}

// dedupe：bundle id + 文件名相同的副本只留一份（所以 Xcode 和 Xcode-beta 都保留）；
// 优先级：/Applications 直接子项 > ~/Applications 直接子项 > 其他（Mole :1132）。bundle id 未知的不去重。
func dedupe(apps []App, env *Env) []App {
	rank := func(a App) int {
		switch filepath.Dir(a.Path) {
		case "/Applications":
			return 1
		case filepath.Join(env.Home, "Applications"):
			return 2
		}
		if len(env.Roots) > 0 && filepath.Dir(a.Path) == filepath.Clean(env.Roots[0]) {
			return 1
		}
		return 3
	}
	best := map[string]int{}
	var out []App
	for _, a := range apps {
		if a.BundleID == "" {
			out = append(out, a)
			continue
		}
		key := a.BundleID + "|" + filepath.Base(a.Path)
		if i, ok := best[key]; ok {
			if rank(a) < rank(out[i]) {
				out[i] = a
			}
			continue
		}
		best[key] = len(out)
		out = append(out, a)
	}
	return out
}

// bundleIDOf 重新读一个 .app 的 bundle id（删前复核用）。
func bundleIDOf(ctx context.Context, env *Env, p string) string {
	info, err := readPlist(ctx, env, filepath.Join(p, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	return str(info, "CFBundleIdentifier")
}
