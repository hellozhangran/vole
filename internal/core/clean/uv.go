// SPDX-License-Identifier: GPL-3.0-only
// uv 的清理方式参考自 tw93/Mole lib/clean/dev.sh clean_uv_cache（:204，commit 6d1af75，GPL-3.0）；
// prune 能清多少的估算、symlink 模式检测、占用进程描述是 Vole 自写。

package clean

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// uvPrunable 估算 `uv cache prune` 能清掉多少（只读）。
// uv 把解压后的包放在 archive-v0/<id>，其他目录（wheels-v5、sdists、environments……）里的指针文件和 symlink 引用这些 id；
// prune 删的就是没人引用的 archive。这里把其余目录里的小文件和 symlink 读一遍，收集出现过的 id，
// 没出现过的 archive 大小加起来就是估算值。一个引用都找不到时（uv 换了格式）返回 ok=false，改按整个目录算。
func uvPrunable(ctx context.Context, root string) (int64, bool) {
	archive := filepath.Join(root, "archive-v0")
	entries, err := os.ReadDir(archive)
	if err != nil {
		return 0, false
	}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.Name()] = false
	}
	if len(ids) == 0 {
		return 0, true
	}
	found := 0
	mark := func(b []byte) {
		// 按 [A-Za-z0-9_-] 切成一段段，整段等于某个 id 就算被引用
		start := -1
		for i := 0; i <= len(b); i++ {
			ok := i < len(b) && (b[i] >= 'a' && b[i] <= 'z' || b[i] >= 'A' && b[i] <= 'Z' || b[i] >= '0' && b[i] <= '9' || b[i] == '_' || b[i] == '-')
			if ok && start < 0 {
				start = i
			} else if !ok && start >= 0 {
				if seen, exists := ids[string(b[start:i])]; exists && !seen {
					ids[string(b[start:i])] = true
					found++
				}
				start = -1
			}
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p == archive {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if t, err := os.Readlink(p); err == nil {
				mark([]byte(t))
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() && info.Size() < 2<<20 {
			if b, err := os.ReadFile(p); err == nil {
				mark(b)
			}
		}
		return nil
	})
	if ctx.Err() != nil || found == 0 {
		return 0, false
	}
	var total int64
	for id, seen := range ids {
		if !seen {
			n, _ := diskUsage(ctx, filepath.Join(archive, id))
			total += n
		}
	}
	return total, true
}

// uvSymlinkRisk：uv 的全局配置里 link-mode 设成 symlink 时，venv 里的包是指向缓存的链接，清空缓存会让它们失效。
// 查环境变量 UV_LINK_MODE、用户配置（$XDG_CONFIG_HOME/uv/uv.toml、~/.config/uv/uv.toml）、系统配置（/etc/uv/uv.toml）。
// 各项目自己 pyproject.toml / uv.toml 里的设置不扫（已知盲区，见 m5-notes）。
func uvSymlinkRisk(env *Env) string {
	const risk = "检测到 uv 使用 symlink 模式，清空缓存会让已有虚拟环境失效。"
	getenv := func(k string) string {
		if env.Getenv == nil {
			return ""
		}
		return env.Getenv(k)
	}
	if strings.EqualFold(strings.TrimSpace(getenv("UV_LINK_MODE")), "symlink") {
		return risk
	}
	files := []string{env.expand("~/.config/uv/uv.toml")}
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		files = append(files, filepath.Join(x, "uv", "uv.toml"))
	}
	if env.EtcDir != "" {
		files = append(files, filepath.Join(env.EtcDir, "uv", "uv.toml"))
	}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && linkModeSymlink.Match(b) {
			return risk
		}
	}
	return ""
}

// linkModeSymlink 匹配 uv.toml 里任何位置（顶层或 [pip] 下）的 link-mode = "symlink"。
var linkModeSymlink = regexp.MustCompile(`(?m)^\s*link-mode\s*=\s*["']symlink["']`)

// uvHolders 写出占着 uv 缓存的进程：「被 uv run 占用：my-mcp-server（PID 69222、69223 等 6 个）」。
// 能从 --directory / --project 取出项目目录就写目录名，取不到写 PID。
func uvHolders(procs []Proc) string {
	if len(procs) == 0 {
		return ""
	}
	type group struct {
		name string
		pids []int
	}
	var groups []group
	at := map[string]int{}
	kinds := map[string]bool{}
	for _, p := range procs {
		kind, dir := uvCommand(p.Args)
		kinds[kind] = true
		name := filepath.Base(dir)
		if dir == "" {
			name = ""
		}
		i, ok := at[name]
		if !ok {
			i = len(groups)
			at[name] = i
			groups = append(groups, group{name: name})
		}
		groups[i].pids = append(groups[i].pids, p.PID)
	}
	kind := "uv"
	if len(kinds) == 1 {
		for k := range kinds {
			kind = k
		}
	}
	var parts []string
	for _, g := range groups {
		pids := g.pids
		var ps []string
		for _, pid := range pids[:min(3, len(pids))] {
			ps = append(ps, fmt.Sprint(pid))
		}
		list := "PID " + strings.Join(ps, "、")
		if len(pids) > 3 {
			list += fmt.Sprintf(" 等 %d 个", len(pids))
		}
		if g.name == "" {
			parts = append(parts, list)
		} else {
			parts = append(parts, g.name+"（"+list+"）")
		}
	}
	return "被 " + kind + " 占用：" + strings.Join(parts, "；")
}

// uvCommand 从 uv 的命令行里取出子命令（uv run / uvx / uv）和项目目录（--directory、--project）。
func uvCommand(args string) (kind, dir string) {
	f := strings.Fields(args)
	if len(f) == 0 {
		return "uv", ""
	}
	kind = filepath.Base(f[0])
	for i := 1; i < len(f); i++ {
		a := f[i]
		for _, flag := range []string{"--directory", "--project"} {
			if v, ok := strings.CutPrefix(a, flag+"="); ok {
				dir = v
			} else if a == flag && i+1 < len(f) {
				// 目录里可能有空格：一直取到下一个参数或子命令为止
				j := i + 1
				parts := []string{f[j]}
				for j+1 < len(f) && !strings.HasPrefix(f[j+1], "-") && !uvSubcommands[f[j+1]] {
					j++
					parts = append(parts, f[j])
				}
				dir, i = strings.Join(parts, " "), j
			}
		}
		if kind == "uv" && uvSubcommands[a] {
			kind = "uv " + a
			break
		}
	}
	return kind, dir
}

var uvSubcommands = map[string]bool{"run": true, "sync": true, "pip": true, "tool": true, "add": true, "remove": true,
	"lock": true, "venv": true, "build": true, "publish": true, "python": true, "cache": true, "export": true, "tree": true}
