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
	"strings"

	"vole/internal/core/clean"
	"vole/internal/i18n"
)

var cleanUsage = i18n.T(`用法：
  vole-cli clean scan [--json]                       只读扫描，列出各类别、各项和大小
  vole-cli clean run --dry-run (--category ID | --item ID)...   只打印会清理什么，不删
  vole-cli clean run --yes (--category ID | --item ID)...       真的清理：必须同时带 --yes 和要清理的类别或项

--category 只选该类别里默认勾选、且没有被挡住的项；--item 用 scan 列出的项 ID（可重复）。
不提供「全部清理」的参数。`)

// multi 是可重复的字符串参数。
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func runClean(args []string) error {
	if len(args) == 0 {
		fmt.Println(cleanUsage)
		return nil
	}
	switch args[0] {
	case "scan":
		return runCleanScan(args[1:])
	case "run":
		return runCleanRun(args[1:])
	}
	return fmt.Errorf(i18n.T("未知子命令 %q\n%s"), args[0], cleanUsage)
}

func cleanScan(ctx context.Context) (*clean.Env, *clean.ScanResult, error) {
	env, err := clean.DefaultEnv()
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintln(os.Stderr, i18n.T("扫描中（只读）…"))
	res, err := clean.Scan(ctx, env)
	return env, res, err
}

func runCleanScan(args []string) error {
	fs := flag.NewFlagSet("clean scan", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, i18n.T("输出 JSON"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_, res, err := cleanScan(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	var total, picked, all int64
	for _, c := range res.Categories {
		all += c.Size
		fmt.Printf(i18n.T("\n■ %s（%s，%s）  共 %s（含不能清理的）\n"), c.Title, c.ID, c.Elapsed.Round(10e6), humanSize(c.Size))
		if c.Note != "" {
			fmt.Printf(i18n.T("  注：%s\n"), c.Note)
		}
		for _, it := range c.Items {
			box := "[ ]"
			if it.Default {
				box = "[x]"
			}
			if it.Blocked != "" {
				box = " - "
			}
			how := it.Disposal.String()
			if len(it.Command) > 0 {
				how = i18n.T("命令 ") + strings.Join(it.Command[1:], " ")
			}
			size := humanSize(it.Size)
			if it.SizeUnknown {
				size = "—"
			}
			if it.Uncertain {
				how += i18n.T(" · 需确认（判断不了是否有 app 在用）")
			}
			fmt.Printf("  %s %9s  %-58s %s\n", box, size, it.ID, how)
			if it.Blocked != "" {
				fmt.Printf(i18n.T("      不能清理：%s\n"), it.Blocked)
			}
			if it.Blocked == "" {
				total += it.Size
				if it.Default {
					picked += it.Size
				}
			}
		}
	}
	fmt.Printf(i18n.T("\n共扫到 %s，其中可清理 %s、默认勾选 %s；扫描耗时 %s。[x] 默认勾选  [ ] 默认不勾  - 不能清理\n"), humanSize(all), humanSize(total), humanSize(picked), res.Elapsed.Round(10e6))
	return nil
}

func runCleanRun(args []string) error {
	fs := flag.NewFlagSet("clean run", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, i18n.T("只打印会清理什么，不删"))
	yes := fs.Bool("yes", false, i18n.T("确认真的清理"))
	var cats, ids multi
	fs.Var(&cats, "category", i18n.T("要清理的类别 ID（可重复）"))
	fs.Var(&ids, "item", i18n.T("要清理的项 ID（可重复）"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(cats) == 0 && len(ids) == 0 {
		return errors.New(i18n.T("必须用 --category 或 --item 指定要清理的内容（不提供全部清理）"))
	}
	if !*dry && !*yes {
		return errors.New(i18n.T("真的清理必须加 --yes；只想看会清理什么请加 --dry-run"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	env, res, err := cleanScan(ctx)
	if err != nil {
		return err
	}

	var plan []clean.Item
	seen := map[string]bool{}
	add := func(it clean.Item) {
		if !seen[it.ID] {
			seen[it.ID] = true
			plan = append(plan, it)
		}
	}
	for _, c := range cats {
		found := false
		for _, cr := range res.Categories {
			if cr.ID != c {
				continue
			}
			found = true
			for _, it := range cr.Items {
				if it.Default && it.Blocked == "" {
					add(it)
				}
			}
		}
		if !found {
			return fmt.Errorf(i18n.T("没有类别 %q"), c)
		}
	}
	for _, id := range ids {
		var hit *clean.Item
		for _, it := range res.Items() {
			if it.ID == id {
				hit = &it
				break
			}
		}
		if hit == nil {
			return fmt.Errorf(i18n.T("扫描结果里没有项 %q（先跑 clean scan 看 ID）"), id)
		}
		if hit.Blocked != "" {
			return fmt.Errorf(i18n.T("%s 不能清理：%s"), id, hit.Blocked)
		}
		add(*hit)
	}
	if len(plan) == 0 {
		fmt.Println(i18n.T("没有可清理的项。"))
		return nil
	}

	mode := i18n.T("清理")
	if *dry {
		mode = i18n.T("dry-run（不会删除任何东西）")
	}
	fmt.Printf(i18n.T("%s：%d 项\n"), mode, len(plan))
	rep := clean.Run(ctx, env, plan, clean.RunOptions{DryRun: *dry, Progress: func(done, total int, o clean.Outcome) {
		line := fmt.Sprintf("  [%d/%d] %-4s %9s  %s", done, total, i18n.T(string(o.Status)), humanSize(o.Item.Size), o.Item.Path)
		if o.Reason != "" {
			line += "  —  " + o.Reason
		}
		if o.Freed > 0 {
			line += fmt.Sprintf(i18n.T("（释放 %s）"), humanSize(o.Freed))
		}
		fmt.Println(line)
	}})
	if rep.Canceled {
		fmt.Println(i18n.T("已取消：剩下的项没有处理。"))
	}
	if !*dry {
		fmt.Printf(i18n.T("共释放 %s。日志：%s/operations.log\n"), humanSize(rep.Freed), env.LogDir)
	}
	if rep.LogErr != nil {
		fmt.Fprintln(os.Stderr, i18n.T("警告：操作日志写入失败："), rep.LogErr)
	}
	return nil
}
