// SPDX-License-Identifier: GPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"vole/internal/core/apps"
)

const appsUsage = `用法：
  vole-cli apps list [--json]                        列出已装 app（只读）
  vole-cli apps show <app> [--json]                  列出一个 app 的关联文件（只读）
  vole-cli apps remove --dry-run [--quit] [--data] <app>...   只打印会卸载什么，不动任何东西
  vole-cli apps remove --yes [--quit] [--data] <app>...       真的卸载：必须同时带 --yes 和指定的 app

<app> 可以是显示名、.app 文件名、bundle id 或 .app 的完整路径。
默认只移走 app 本体和缓存、日志；--data 才连个人数据（Application Support、Containers……）一起移走。
--quit：app 正在运行时先退出（正常退出 → 等 5 秒 → TERM → KILL，还没退出就跳过）；不带时运行中的 app 跳过。
一律移到废纸篓；root 拥有的 app 交给 Finder，会弹管理员密码框。`

func runApps(args []string) error {
	if len(args) == 0 {
		fmt.Println(appsUsage)
		return nil
	}
	env, err := apps.DefaultEnv()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch args[0] {
	case "list":
		return appsList(ctx, env, args[1:])
	case "show":
		return appsShow(ctx, env, args[1:])
	case "remove":
		return appsRemove(ctx, env, args[1:])
	}
	return fmt.Errorf("未知的 apps 子命令 %q\n%s", args[0], appsUsage)
}

func appsList(ctx context.Context, env *apps.Env, args []string) error {
	fs := flag.NewFlagSet("apps list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "读取已装 app（只读）…")
	list, err := apps.List(ctx, env, apps.ListOptions{Sizes: true})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	var total int64
	now := env.Now()
	for _, a := range list {
		total += a.Size
		fmt.Printf("%9s  %-28s %-14s %s\n", humanSize(a.Size), clip(a.Name, 28), clip(a.Version, 14), strings.Join(appTags(a, now), " · "))
	}
	fmt.Printf("\n已安装应用 %d 个 · %s\n", len(list), humanSize(total))
	return nil
}

// appTags：状态和来源（列表里「版本 · 大小 · 状态」的状态部分）。
func appTags(a apps.App, now time.Time) []string {
	var t []string
	switch {
	case a.Running && a.MenuBar:
		t = append(t, "菜单栏常驻")
	case a.Running:
		t = append(t, "活跃中")
	case a.Idle(now):
		t = append(t, ago(now.Sub(a.LastUsed))+"活跃")
	}
	if a.Intel {
		t = append(t, "Intel")
	}
	switch a.Source {
	case apps.SourceAppStore:
		t = append(t, "App Store")
	case apps.SourceHomebrew:
		t = append(t, "Homebrew("+a.CaskToken+")")
	case apps.SourceSteam:
		t = append(t, "Steam 管理")
	}
	if a.NeedsAdmin {
		t = append(t, "需要管理员密码")
	}
	if a.Blocked != "" {
		t = append(t, "不能卸："+a.Blocked)
	}
	return t
}

func ago(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 365:
		return fmt.Sprintf("%d年前", days/365)
	case days >= 30:
		return fmt.Sprintf("%d个月前", days/30)
	}
	return fmt.Sprintf("%d天前", days)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// findApp 按显示名、文件名、bundle id 或路径找 app；对不上或有歧义都报错。
func findApp(list []apps.App, q string) (apps.App, error) {
	var hits []apps.App
	for _, a := range list {
		if a.Path == q || strings.EqualFold(a.Name, q) || strings.EqualFold(a.BundleID, q) ||
			strings.EqualFold(strings.TrimSuffix(filepath.Base(a.Path), ".app"), strings.TrimSuffix(q, ".app")) {
			hits = append(hits, a)
		}
	}
	switch len(hits) {
	case 0:
		return apps.App{}, fmt.Errorf("找不到 app %q（用 vole-cli apps list 看名字）", q)
	case 1:
		return hits[0], nil
	}
	var ps []string
	for _, h := range hits {
		ps = append(ps, h.Path)
	}
	return apps.App{}, fmt.Errorf("%q 对应多个 app，请用完整路径：%s", q, strings.Join(ps, "、"))
}

func appsShow(ctx context.Context, env *apps.Env, args []string) error {
	fs := flag.NewFlagSet("apps show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：vole-cli apps show <app>")
	}
	list, err := apps.List(ctx, env, apps.ListOptions{Sizes: true})
	if err != nil {
		return err
	}
	a, err := findApp(list, fs.Arg(0))
	if err != nil {
		return err
	}
	files := apps.Files(ctx, env, a, list)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"app": a, "files": files})
	}
	fmt.Printf("%s %s（%s）\n%s\n%s · %s\n\n", a.Name, a.Version, a.BundleID, a.Path, humanSize(a.Size), strings.Join(appTags(a, env.Now()), " · "))
	var total int64
	for _, f := range files {
		box := "[ ]"
		if f.Default {
			box = "[x]"
		}
		if f.Kind == apps.KindSystem {
			box = " - "
		}
		size := humanSize(f.Size)
		if f.Kind == apps.KindSystem {
			size = ""
		}
		line := fmt.Sprintf("  %s %9s  %-10s %s", box, size, f.Kind, pretty(env.Home, f.Path))
		if f.Extra != "" {
			line += "  （Vole 补充：" + f.Extra + "）"
		}
		fmt.Println(line)
		total += f.Size
	}
	fmt.Printf("\n%d 个关联文件 · %s。[x] 默认勾选（缓存和日志）  [ ] 个人数据，默认不勾  - 系统级，Vole 不处理\n", len(files), humanSize(total))
	return nil
}

func pretty(home, p string) string {
	if rest, ok := strings.CutPrefix(p, home+"/"); ok {
		return "~/" + rest
	}
	return p
}

func appsRemove(ctx context.Context, env *apps.Env, args []string) error {
	fs := flag.NewFlagSet("apps remove", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "只打印，不动任何东西")
	yes := fs.Bool("yes", false, "确认真的卸载")
	quit := fs.Bool("quit", false, "运行中的 app 先退出")
	data := fs.Bool("data", false, "连个人数据一起移走")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dry == *yes {
		return errors.New("必须二选一：--dry-run（只打印）或 --yes（真的卸载）")
	}
	if fs.NArg() == 0 {
		return errors.New("要指定卸载哪些 app")
	}
	list, err := apps.List(ctx, env, apps.ListOptions{Sizes: true})
	if err != nil {
		return err
	}
	var plans []apps.Plan
	for _, q := range fs.Args() {
		a, err := findApp(list, q)
		if err != nil {
			return err
		}
		var pick []apps.File
		for _, f := range apps.Files(ctx, env, a, list) {
			if f.Kind == apps.KindCache || (*data && f.Kind == apps.KindData) {
				pick = append(pick, f)
			}
		}
		plans = append(plans, apps.Plan{App: a, Files: pick})
	}
	if *dry {
		fmt.Println("dry-run（不会动任何东西）：")
	}
	rep := apps.Run(ctx, env, plans, apps.RunOptions{DryRun: *dry, Quit: *quit, Progress: func(done, total int, o apps.Outcome) {
		line := fmt.Sprintf("  [%d/%d] %-6s %s（%d 项，%s）", done, total, o.Status, o.App.Name, o.Moved, humanSize(o.Size))
		if o.Reason != "" {
			line += " — " + o.Reason
		}
		fmt.Println(line)
		for _, f := range o.Failures {
			fmt.Println("        没移走：" + f)
		}
	}})
	if rep.LogErr != nil {
		fmt.Fprintln(os.Stderr, "操作日志写入失败：", rep.LogErr)
	}
	if !*dry {
		fmt.Printf("已移到废纸篓；空间要清空废纸篓后才释放。日志：%s/operations.log\n", env.LogDir)
	}
	return nil
}
