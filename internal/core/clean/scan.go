// SPDX-License-Identifier: GPL-3.0-only
// 缓存归属判断参考自 tw93/Mole lib/core/file_ops.sh _mole_user_cache_scope（:206）和 _mole_user_cache_owner_process_state（:459）
// （commit 6d1af75，GPL-3.0），用 Go 重写；扫描、候选、保护名单的组织方式是 Vole 自己的。

package clean

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"vole/internal/i18n"
)

// ScanOptions 控制扫描。
type ScanOptions struct {
	Categories []CategoryDef // 为空 = 全部（Categories()）
	// Progress 在某个类别开始（done=false）和结束（done=true）时调用；会从多个 goroutine 同时调用。
	Progress func(categoryTitle string, done bool)
}

// Scan 只读扫描全部类别（类别之间并行），返回可清理的清单。不删除任何东西。
func Scan(ctx context.Context, env *Env) (*ScanResult, error) {
	return ScanWith(ctx, env, ScanOptions{})
}

// ScanCategories 只扫描给定的类别。
func ScanCategories(ctx context.Context, env *Env, cats []CategoryDef) (*ScanResult, error) {
	return ScanWith(ctx, env, ScanOptions{Categories: cats})
}

// ScanWith 按选项扫描（只读）。
func ScanWith(ctx context.Context, env *Env, opts ScanOptions) (*ScanResult, error) {
	cats := opts.Categories
	if len(cats) == 0 {
		cats = Categories()
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string, bool) {}
	}
	start := env.Now()
	snap, snapErr := env.Processes(ctx)
	res := &ScanResult{Categories: make([]CategoryResult, len(cats))}
	var wg sync.WaitGroup
	for i, c := range cats {
		wg.Go(func() {
			progress(c.Title, false)
			res.Categories[i] = scanCategory(ctx, env, c, snap, snapErr)
			progress(c.Title, true)
		})
	}
	wg.Wait()
	res.Elapsed = env.Now().Sub(start)
	return res, ctx.Err()
}

func scanCategory(ctx context.Context, env *Env, c CategoryDef, snap *ProcSnapshot, snapErr error) CategoryResult {
	start := env.Now()
	budget := env.ScanBudget
	if budget <= 0 {
		budget = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	out := CategoryResult{ID: c.ID, Title: c.Title}
	var notes []string
	var mu sync.Mutex
	sem := make(chan struct{}, 8) // 每个类别最多同时统计 8 项
	var wg sync.WaitGroup
	for i := range c.Rules {
		r := &c.Rules[i]
		cands, err := candidates(ctx, env, r)
		if err != nil && ctx.Err() == nil {
			notes = append(notes, r.Title+i18n.T("：")+err.Error())
		}
		for _, cand := range cands {
			blocked := guardReason(r, cand, snap, snapErr)
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				item, ok, note := measure(ctx, env, r, cand, blocked, snap, snapErr)
				mu.Lock()
				defer mu.Unlock()
				if note != "" {
					notes = append(notes, note)
				}
				if ok {
					out.Items = append(out.Items, item)
				}
			})
		}
	}
	wg.Wait()
	if ctx.Err() != nil {
		notes = append(notes, i18n.T("扫描超时或被取消，部分项的大小没有算完（显示「—」）"))
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Size > out.Items[j].Size })
	for _, it := range out.Items {
		out.Size += it.Size
	}
	out.Note = strings.Join(dedup(notes), i18n.T("；"))
	out.Elapsed = env.Now().Sub(start)
	return out
}

// measure 统计一项的大小并生成 Item；大小为 0 的不列。超时没算完的照样列出，大小写「—」。
func measure(ctx context.Context, env *Env, r *Rule, c Candidate, blocked string, snap *ProcSnapshot, snapErr error) (Item, bool, string) {
	id, _, err := identityOf(c.Path)
	if err != nil {
		return Item{}, false, ""
	}
	item := Item{
		ID: r.ID + ":" + env.pretty(c.Path), RuleID: r.ID, Category: r.Category, Title: c.Title, Group: r.Group,
		Path: c.Path, Target: c.Target, Default: r.Default, Disposal: r.Disposal,
		Command: c.Command, Note: r.Note, Blocked: blocked, Ident: id,
	}
	if c.Group != "" {
		item.Group = c.Group
	}
	if c.Confirm {
		item.Default = false
	}
	if r.Risk != nil {
		if risk := r.Risk(env); risk != "" {
			item.Note, item.Default = risk, false
		}
	}
	if c.Note != "" {
		item.Note = c.Note
	}
	if shipItException(filepath.Base(c.Path)) && snap != nil {
		// 说明里写上是哪个 app 的更新（主 app 在运行时能从进程快照里取到名字，如 Cursor）
		id := strings.ToLower(strings.TrimSuffix(filepath.Base(c.Path), ".ShipIt"))
		if app, ok := snap.BundleIDs[id]; ok {
			item.Note = strings.ReplaceAll(item.Note, "app", snap.appName(id, app))
		}
	}
	if item.Title == "" {
		item.Title = r.Title
	}
	if item.Blocked == "" {
		item.Blocked = c.Blocked
	}
	if r.BundleGuard && item.Blocked == "" {
		reason, uncertain := ownerReason(ownerName(r, c.Path), snap, snapErr)
		item.Blocked = reason
		if uncertain && reason == "" {
			item.Uncertain, item.Default = true, false
			item.Note = strings.TrimSpace(i18n.T("无法确认是否有 app 正在用它，确认后再勾选。") + item.Note)
		}
	}
	// 工具命令能估算实际释放量的（如 uv cache prune 只清不再被引用的部分），显示估算值
	if r.Tool != nil && r.Tool.Estimate != nil && len(c.Command) > 0 {
		if n, ok := r.Tool.Estimate(ctx, c.Path); ok {
			item.Size = n
			return item, n > 0, ""
		}
	}
	size, err := diskUsage(ctx, c.Path)
	item.Size = size
	if ctx.Err() != nil {
		item.SizeUnknown = true
		return item, true, ""
	}
	if err != nil && errors.Is(err, fs.ErrPermission) {
		if size == 0 {
			item.Blocked, item.SizeUnknown = i18n.T("需要完全磁盘访问"), true
			return item, true, ""
		}
		item.Note = strings.TrimSpace(item.Note + i18n.T(" 部分子目录读不到，实际可能更大。"))
	}
	if size == 0 {
		return Item{}, false, ""
	}
	return item, true, ""
}

// guardReason 返回规则的守卫进程在运行的原因；判断不了也返回原因（宁可不删）。
// 交给工具命令、且规则说明工具自己有锁时（GuardOnlyByPath），不按进程拦。
func guardReason(r *Rule, c Candidate, snap *ProcSnapshot, snapErr error) string {
	if len(r.Guard) == 0 && len(r.GuardArgs) == 0 {
		return ""
	}
	if r.Tool != nil && r.Tool.GuardOnlyByPath && len(c.Command) > 0 {
		return ""
	}
	if snapErr != nil || snap == nil {
		return i18n.T("无法确认相关程序是否在运行")
	}
	if name, ok := snap.Running(r.Guard); ok {
		if r.GuardText != nil {
			if t := r.GuardText(snap.ByName(r.Guard)); t != "" {
				return t
			}
		}
		if r.GuardReason != "" {
			return r.GuardReason
		}
		return name + i18n.T(" 正在运行")
	}
	if name, ok := snap.RunningArgs(r.GuardArgs); ok {
		if r.Group != "" {
			name = r.Group // 命令行匹配到的是 chrome-devtools-mcp 这类片段，显示成组名
		}
		return name + i18n.T(" 正在运行")
	}
	return ""
}

// ownerReason 判断缓存目录对应的 app：在运行时返回原因；判断不了时 uncertain 为 true（由调用方决定怎么处理）；
// 读不到进程列表时返回原因（宁可不删）。
func ownerReason(name string, snap *ProcSnapshot, snapErr error) (reason string, uncertain bool) {
	if snapErr != nil || snap == nil {
		return i18n.T("无法读取进程列表，无法确认对应 app 是否在运行"), false
	}
	o := snap.Owner(name)
	if o.Running && o.System {
		return SystemBusy + o.Name + i18n.T(" 正在使用"), false
	}
	if o.Running {
		return o.Name + i18n.T(" 正在运行"), false
	}
	return "", o.Uncertain
}

// SystemBusy 是被系统进程占用时原因的前缀（用户关不掉，界面上标「系统占用」）。
var SystemBusy = i18n.T("系统占用：")

// candidates 按规则选出候选项（只读）。扫描和删除前的复核都用它，保证「仍符合规则」的判断一致。
func candidates(ctx context.Context, env *Env, r *Rule) ([]Candidate, error) {
	if r.Custom != nil {
		return r.Custom(ctx, env)
	}
	if r.Tool != nil {
		if c, handled, err := toolCandidate(ctx, env, r); handled {
			return c, err
		}
	}
	var out []Candidate
	for _, pat := range r.Paths {
		full := env.expand(pat)
		matches, err := filepath.Glob(full)
		if err != nil {
			return nil, err
		}
		isGlob := strings.ContainsAny(pat, "*?[")
		type entry struct {
			c   Candidate
			mod time.Time
		}
		var es []entry
		for _, m := range matches {
			info, err := os.Lstat(m)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				continue // 规则匹配到的项本身是 symlink：不碰
			}
			name := filepath.Base(m)
			owner := ownerName(r, m)
			if excluded(r.Exclude, owner) || strings.HasPrefix(name, ".") && isGlob {
				continue
			}
			// glob 匹配出来的项要查名字（Owner > 0 时还查 app 目录名）；规则里写死的路径（如 com.apple.dt.Xcode）是明确要清它的内容
			blocked := ""
			switch {
			case isGlob && shipItException(name):
				// 设计决定：com.todesktop.* 的 .ShipIt 只是更新下载包和日志，放开，但要确认
			case isGlob && (protectedName(name) || protectedName(owner)), protectedPath(m, env.Home):
				blocked = i18n.T("受保护：这个 app 的数据按保护名单不清理")
			case whitelisted(m, env):
				blocked = i18n.T("在白名单里：默认不清理（删了要重新下载或重建）")
			}
			if blocked != "" {
				if r.ShowProtected && isGlob && !strings.HasPrefix(owner, "com.apple.") {
					es = append(es, entry{Candidate{Path: m, Target: Whole, Blocked: blocked, Group: groupOf(r, owner)}, info.ModTime()})
				}
				continue
			}
			if r.MinAge > 0 && env.Now().Sub(info.ModTime()) < r.MinAge {
				continue
			}
			t := r.Target
			if !info.IsDir() {
				t = Whole
			}
			// 和删除前完全一样的路径校验（含上级目录不能是 symlink），不合格的不列出来
			if validatePath(m, env, t == Whole && !r.AnyName) != nil {
				continue
			}
			c := Candidate{Path: m, Target: t, Group: groupOf(r, owner)}
			if isGlob && shipItException(name) {
				c.Confirm, c.Note = true, i18n.T("这是 app 的更新下载包和日志，里面可能有一个还没安装的更新；删了 app 会重新下载更新。")
			}
			es = append(es, entry{c, info.ModTime()})
		}
		if r.KeepNewest > 0 {
			sort.SliceStable(es, func(i, j int) bool { return es[i].mod.After(es[j].mod) })
			es = es[min(r.KeepNewest, len(es)):]
		}
		for _, e := range es {
			if isGlob || len(r.Paths) > 1 {
				e.c.Title = filepath.Base(e.c.Path)
			}
			out = append(out, e.c)
		}
	}
	return out, nil
}

// ownerName 是判断归属用的名字：匹配项往上第 r.Owner 层的目录名。
func ownerName(r *Rule, p string) string {
	for range r.Owner {
		p = filepath.Dir(p)
	}
	return filepath.Base(p)
}

// shipItException：com.todesktop.<id>.ShipIt（Cursor 等 ToDesktop 打包的 app 的更新目录）。
// Mole 用 com.todesktop.* 通配保护这些 app 的数据，更新目录被顺带罩进去；作者拍板只放开 .ShipIt，其余照旧受保护。
func shipItException(name string) bool {
	return strings.HasPrefix(name, "com.todesktop.") && strings.HasSuffix(name, ".ShipIt") && !strings.Contains(name, "/")
}

// groupOf：按上级 app 目录判断归属的规则（Application Support/<App>/Cache），没写 Group 时按 app 分组。
func groupOf(r *Rule, owner string) string {
	if r.Group == "" && r.Owner > 0 {
		return owner
	}
	return ""
}

// excluded：名字命中 Exclude 里的某一项（可含 glob）。
func excluded(pats []string, name string) bool {
	for _, pat := range pats {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// bundleLike 是像 bundle id 的名字：至少三段、以点分隔，如 com.foo.app。
var bundleLike = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9_-]*){2,}$`)

// toolCandidate：装了工具就交给工具命令清理它的缓存目录。handled 为 false 表示没装工具、按路径处理。
func toolCandidate(ctx context.Context, env *Env, r *Rule) ([]Candidate, bool, error) {
	t := r.Tool
	bin, err := env.LookPath(t.Bin)
	if err != nil {
		if t.Required {
			return nil, true, nil // 没装工具就不碰（如 Go module 缓存的文件是只读的，只能让 go 自己删）
		}
		return nil, false, nil
	}
	root := ""
	if len(t.RootArgs) > 0 {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := env.Run(qctx, bin, t.RootArgs...)
		cancel()
		if err == nil {
			if line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); filepath.IsAbs(strings.TrimSpace(line)) {
				root = filepath.Join(strings.TrimSpace(line), t.RootSuffix)
			}
		}
	}
	if root == "" && len(r.Paths) > 0 {
		root = env.expand(r.Paths[0])
	}
	if root == "" {
		return nil, true, nil
	}
	root = filepath.Clean(root)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, true, nil
	}
	// 工具给出的目录也要过同样的校验（必须在家目录下等），不合格就不碰
	if validatePath(root, env, false) != nil || whitelisted(root, env) {
		return nil, true, nil
	}
	cmd := append([]string{bin}, t.Args...)
	if len(t.Env) > 0 {
		cmd = append(append([]string{"/usr/bin/env"}, t.Env...), cmd...)
	}
	return []Candidate{{Path: root, Target: Contents, Command: cmd}}, true, nil
}

func dedup(ss []string) []string {
	var out []string
	for _, s := range ss {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
