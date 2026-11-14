// SPDX-License-Identifier: GPL-3.0-only
// 「app 在运行就跳过」参考自 tw93/Mole lib/core/base.sh mole_pgrep_any（:75）
// 和 lib/core/file_ops.sh 的缓存归属检测（:904），用 Go 重写；不做 lsof（看全进程要 root）。

package clean

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"vole/internal/i18n"
)

// ProcSnapshot 是某一刻正在运行的进程。
type ProcSnapshot struct {
	Names     map[string]bool   // 可执行文件名
	BundleIDs map[string]string // 运行中进程所在 .app 的 bundle id（小写）→ 最外层 .app 路径（取显示名用；可为空）
	Uncertain []string          // 读不出 bundle id 的运行中 .app 路径（如运行中被原地更新、Info.plist 已不在）
	System    map[string]bool   // 系统进程（/System、/usr/libexec 等下面的）的可执行文件名：用户关不掉
	Paths     []string          // 全部进程的可执行文件路径（小写），按子串找缓存的主人（Mole file_ops.sh:481）
	Procs     []Proc            // 全部进程的 PID 和完整命令行（Mole 的 pgrep -f；被挡住时写明是哪个进程）

	displayName func(app string) string // 取 app 的显示名；为 nil 时用 .app 文件名
}

// Running 返回 names 里第一个在运行的进程名。
func (s *ProcSnapshot) Running(names []string) (string, bool) {
	for _, n := range names {
		if s.Names[n] {
			return n, true
		}
	}
	return "", false
}

// Proc 是一个进程的 PID 和完整命令行。
type Proc struct {
	PID  int
	Args string
}

// RunningArgs 返回第一个命令行里含 subs 之一的进程（Mole 的 pgrep -f）。
func (s *ProcSnapshot) RunningArgs(subs []string) (string, bool) {
	for _, sub := range subs {
		for _, p := range s.Procs {
			if strings.Contains(p.Args, sub) {
				return sub, true
			}
		}
	}
	return "", false
}

// ByName 返回可执行文件名在 names 里的进程（按命令行第一段的文件名判断）。
func (s *ProcSnapshot) ByName(names []string) []Proc {
	var out []Proc
	for _, p := range s.Procs {
		first, _, _ := strings.Cut(p.Args, " ")
		if slices.Contains(names, filepath.Base(first)) {
			out = append(out, p)
		}
	}
	return out
}

// Ownership 是缓存目录和运行中 app 的关系。
type Ownership struct {
	Name      string // 正在运行的 app 的显示名（Running 时有）
	Running   bool   // 对应的 app 正在运行
	System    bool   // 占用它的是系统进程（用户关不掉）
	Uncertain bool   // 判断不了：列出来但标「需确认」、默认不勾
}

// Owner 判断缓存目录（或文件）的主人是否在运行。规则对齐 Mole（lib/core/file_ops.sh _mole_user_cache_owner_process_state）：
//   - 名字带点（com.foo.app、WPS_7.3.7z）：有进程的路径里含这个名字（不分大小写）就算在运行；
//     名字像 bundle id 时，再看运行中的 .app 的 bundle id 相同、或是它的子 id（com.foo → com.foo.app）。
//     有读不出 bundle id 的运行中 app、且名字里含它的 app 名时，算判断不了。
//   - 不带点的名字（gopls、dotslash）：Mole 不查主人；Vole 多一道删前校验：和运行中的 app 或进程同名就挡住。
//   - 其他情况：没在运行，可以清理。
func (s *ProcSnapshot) Owner(dir string) Ownership {
	d := strings.ToLower(dir)
	if bundleLike.MatchString(dir) {
		for id, app := range s.BundleIDs {
			if d == id || strings.HasPrefix(id, d+".") {
				return Ownership{Name: s.appName(id, app), Running: true, System: systemPath(app)}
			}
		}
	}
	if strings.Contains(dir, ".") && !strings.HasPrefix(dir, ".") {
		for _, p := range s.Paths {
			if strings.Contains(p, d) {
				name := filepath.Base(p)
				return Ownership{Name: name, Running: true, System: s.System[name] || systemPath(p)}
			}
		}
	}
	if bundleLike.MatchString(dir) {
		for _, app := range s.Uncertain {
			name := strings.ToLower(strings.TrimSuffix(filepath.Base(app), ".app"))
			for _, token := range []string{strings.ReplaceAll(name, " ", ""), strings.Fields(name + " ")[0]} {
				if len(token) >= 3 && strings.Contains(d, token) {
					return Ownership{Uncertain: true}
				}
			}
		}
		return Ownership{}
	}
	// 不像 bundle id 的名字：先比 app 显示名，再比进程名（先区分大小写，再不区分）
	for id, app := range s.BundleIDs {
		name := s.appName(id, app)
		if strings.EqualFold(strings.ReplaceAll(name, " ", ""), strings.ReplaceAll(dir, " ", "")) {
			return Ownership{Name: name, Running: true, System: systemPath(app)}
		}
	}
	if s.Names[dir] {
		return Ownership{Name: dir, Running: true, System: s.System[dir]}
	}
	for n := range s.Names {
		if strings.EqualFold(n, dir) {
			return Ownership{Name: n, Running: true, System: s.System[n]}
		}
	}
	return Ownership{}
}

// systemPath：系统自带的程序（用户关不掉）。
func systemPath(p string) bool {
	for _, pre := range []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/usr/bin/", "/sbin/", "/bin/", "/Library/Apple/"} {
		if strings.HasPrefix(p, pre) || strings.HasPrefix(p, strings.ToLower(pre)) {
			return true
		}
	}
	return false
}

// appName 是 bundle id 对应 app 的显示名：优先 LaunchServices 的显示名（本地化），其次 .app 文件名，最后是 bundle id。
func (s *ProcSnapshot) appName(id, app string) string {
	if app == "" {
		return id
	}
	if s.displayName != nil {
		if n := s.displayName(app); n != "" {
			return n
		}
	}
	return strings.TrimSuffix(filepath.Base(app), ".app")
}

var (
	bundleCache sync.Map // .app 路径 → bundle id，app 运行期间不会变
	nameCache   sync.Map // .app 路径 → 显示名
)

// snapshotProcesses 用 ps 列出全部进程的可执行文件路径；.app 里的进程再读出 bundle id。
func snapshotProcesses(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error)) (*ProcSnapshot, error) {
	out, err := run(ctx, "/bin/ps", "-axo", "comm=")
	if err != nil {
		return nil, fmt.Errorf(i18n.T("读取进程列表失败：%w"), err)
	}
	s := &ProcSnapshot{Names: map[string]bool{}, BundleIDs: map[string]string{}, System: map[string]bool{}}
	if out, err := run(ctx, "/bin/ps", "-axww", "-o", "pid=,args="); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			pid, args, ok := strings.Cut(strings.TrimSpace(l), " ")
			if n, err := strconv.Atoi(pid); ok && err == nil {
				s.Procs = append(s.Procs, Proc{PID: n, Args: strings.TrimSpace(args)})
			}
		}
	}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		s.Names[filepath.Base(p)] = true
		s.Paths = append(s.Paths, strings.ToLower(p))
		if systemPath(p) {
			s.System[filepath.Base(p)] = true
		}
		// 路径上每一层 .app 都算（Helper 在外层 app 里）；显示名一律取最外层的 app
		outer := ""
		for i := 0; ; {
			j := strings.Index(p[i:], ".app/")
			if j < 0 {
				break
			}
			app := p[:i+j+len(".app")]
			i += j + len(".app/")
			if outer == "" {
				outer = app
			}
			if seen[app] {
				continue
			}
			seen[app] = true
			id, err := bundleID(ctx, run, app)
			if err != nil || id == "" {
				s.Uncertain = append(s.Uncertain, app)
				continue
			}
			s.BundleIDs[strings.ToLower(id)] = outer
		}
	}
	return s, nil
}

func bundleID(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error), app string) (string, error) {
	if v, ok := bundleCache.Load(app); ok {
		return v.(string), nil
	}
	out, err := run(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	bundleCache.Store(app, id)
	return id, nil
}

// displayNameOf 用 Spotlight 元数据（kMDItemDisplayName，Finder 里显示的本地化名字）取 app 名；取不到返回空。
func displayNameOf(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error), app string) string {
	if v, ok := nameCache.Load(app); ok {
		return v.(string)
	}
	name := ""
	if out, err := run(ctx, "/usr/bin/mdls", "-raw", "-name", "kMDItemDisplayName", app); err == nil {
		n := strings.TrimSpace(string(out))
		if n != "" && n != "(null)" {
			name = strings.TrimSuffix(n, ".app")
		}
	}
	nameCache.Store(app, name)
	return name
}
