// SPDX-License-Identifier: GPL-3.0-only
// 删前复核的做法参考自 tw93/Mole bin/clean.sh safe_clean_guarded（:1498）和 lib/core/file_ops.sh safe_remove（:1473）
// （commit 6d1af75，GPL-3.0），用 Go 重写。

package clean

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Status 是一项的处理结果。
type Status string

const (
	StatusDone     Status = "已清理"
	StatusWould    Status = "将清理" // dry-run
	StatusSkipped  Status = "跳过"
	StatusFailed   Status = "失败"
	StatusCanceled Status = "已取消"
)

// Outcome 是一项的处理结果。
type Outcome struct {
	Item   Item
	Status Status
	Reason string
	Freed  int64 // 实际释放的字节（删除前后大小之差；移到废纸篓时为 0，空间还在废纸篓里）
}

// RunOptions 控制一次删除。
type RunOptions struct {
	DryRun   bool                             // 只做全部检查，不删
	Progress func(done, total int, o Outcome) // 每处理完一项回调一次（在调用 Run 的 goroutine 上）
}

// RunReport 是一次删除的结果。
type RunReport struct {
	Outcomes []Outcome
	Freed    int64
	Canceled bool
	LogErr   error // 操作日志写失败（不影响删除）
}

// Run 按清单逐项删除。只处理传进来的项（必须来自 Scan，用户确认过）；每一项删之前：
//  1. 用同一条规则重新选一遍候选，这一项必须仍在其中（路径、删除方式、工具命令都一致）——仍符合规则；
//  2. 重新校验路径（家目录内、非关键路径、上级目录不是 symlink、不在保护名单和白名单）；
//  3. 比对 dev/inode 和文件类型，和扫描时不一致就跳过（路径被替换了）；
//  4. 重新取进程快照，相关 app 在运行或判断不了就跳过。
//
// ctx 取消后，当前这一项做完就停，剩下的标为「已取消」；已删的无法回滚。单项出错只记下原因，继续下一项。
func Run(ctx context.Context, env *Env, items []Item, opts RunOptions) *RunReport {
	rep := &RunReport{}
	log := newOpLog(env)
	action := "clean"
	if opts.DryRun {
		action = "dry-run"
	}
	log.line(env.Now(), action, "开始", 0, "", "", fmt.Sprintf("%d 项", len(items)))
	eligible := map[string]map[string]Candidate{}
	for i, it := range items {
		if ctx.Err() != nil {
			for _, rest := range items[i:] {
				o := Outcome{Item: rest, Status: StatusCanceled, Reason: "用户取消"}
				rep.Outcomes = append(rep.Outcomes, o)
				log.line(env.Now(), action, string(o.Status), rest.Size/1024, rest.RuleID, rest.Path, o.Reason)
			}
			rep.Canceled = true
			break
		}
		o := runOne(ctx, env, it, opts.DryRun, eligible)
		rep.Outcomes = append(rep.Outcomes, o)
		rep.Freed += o.Freed
		log.line(env.Now(), action, string(o.Status), it.Size/1024, it.RuleID, it.Path, o.Reason)
		if opts.Progress != nil {
			opts.Progress(i+1, len(items), o)
		}
	}
	log.line(env.Now(), action, "结束", rep.Freed/1024, "", "", fmt.Sprintf("释放 %d KB", rep.Freed/1024))
	rep.LogErr = log.err
	return rep
}

func runOne(ctx context.Context, env *Env, it Item, dry bool, eligible map[string]map[string]Candidate) Outcome {
	skip := func(reason string) Outcome { return Outcome{Item: it, Status: StatusSkipped, Reason: reason} }
	rule, ok := ruleByID(it.RuleID)
	if !ok {
		return skip("未知规则")
	}
	if it.Blocked != "" {
		return skip(it.Blocked)
	}

	// 1. 仍符合规则
	cands, ok := eligible[rule.ID]
	if !ok {
		list, err := candidates(ctx, env, rule)
		if err != nil {
			return skip("无法确认是否仍符合规则：" + err.Error())
		}
		cands = map[string]Candidate{}
		for _, c := range list {
			cands[c.Path] = c
		}
		eligible[rule.ID] = cands
	}
	c, ok := cands[it.Path]
	if !ok {
		return skip("不再符合规则（可能已被删除或已经变化）")
	}
	if c.Blocked != "" {
		return skip(c.Blocked)
	}
	if c.Target != it.Target || !slices.Equal(c.Command, it.Command) {
		return skip("清理方式和扫描时不一致")
	}

	// 2. 路径校验；「删内容、留目录」的根目录不查名字，子项逐个查
	if err := validatePath(it.Path, env, it.Target == Whole && !rule.AnyName); err != nil {
		return skip(err.Error())
	}

	// 3. 身份
	id, _, err := identityOf(it.Path)
	if err != nil {
		return skip("已经不存在")
	}
	if id != it.Ident {
		return skip("扫描后路径被替换成了别的文件或目录")
	}

	// 4. 进程（每项都重新取快照）
	snap, snapErr := env.Processes(ctx)
	if r := guardReason(rule, c, snap, snapErr); r != "" {
		return skip(r)
	}
	if rule.BundleGuard {
		reason, uncertain := ownerReason(ownerName(rule, it.Path), snap, snapErr)
		if reason != "" {
			return skip(reason)
		}
		// 判断不了的项：只有扫描时就标了「需确认」、用户仍然勾了才清；扫描后才变得判断不了的跳过
		if uncertain && !it.Uncertain {
			return skip("无法确认是否有 app 正在用它")
		}
	}

	if rule.PreDelete != nil {
		if r := rule.PreDelete(ctx, env, it.Path); r != "" {
			return skip(r)
		}
	}

	if dry {
		reason := it.Disposal.String()
		if len(it.Command) > 0 {
			reason = "运行 " + strings.Join(it.Command, " ")
		} else if it.Target == Contents {
			reason += "（目录里的全部内容，保留目录本身）"
		}
		return Outcome{Item: it, Status: StatusWould, Reason: reason}
	}

	// 当前这一项一旦开始就做完，不响应取消（取消在项与项之间生效）
	work := context.WithoutCancel(ctx)
	switch {
	case len(it.Command) > 0:
		return runTool(work, env, rule, it)
	case it.Disposal == ToTrash:
		if err := MoveToTrash(work, env, it.Path); err != nil {
			return Outcome{Item: it, Status: StatusFailed, Reason: err.Error()}
		}
		return Outcome{Item: it, Status: StatusDone, Reason: "已移到废纸篓"}
	case it.Target == Whole:
		if err := removeAllWritable(it.Path); err != nil {
			return failed(work, it, err)
		}
		return Outcome{Item: it, Status: StatusDone, Freed: it.Size}
	default:
		return removeContents(work, env, rule, it)
	}
}

// removeContents 删除目录里的每个子项（保留目录本身）：子项逐个校验，symlink 只删链接本身，不跟进去。
func removeContents(ctx context.Context, env *Env, rule *Rule, it Item) Outcome {
	entries, err := os.ReadDir(it.Path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return Outcome{Item: it, Status: StatusSkipped, Reason: "需要完全磁盘访问"}
		}
		return Outcome{Item: it, Status: StatusFailed, Reason: err.Error()}
	}
	var failedN, protectedN int
	var firstErr error
	for _, e := range entries {
		p := filepath.Join(it.Path, e.Name())
		if err := validatePath(p, env, !rule.AnyName); err != nil {
			protectedN++
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			continue // 已经没了
		}
		if info.Mode()&os.ModeSymlink != 0 {
			err = os.Remove(p) // 只删链接本身
			if errors.Is(err, fs.ErrPermission) && makeWritable(it.Path) == nil {
				err = os.Remove(p)
			}
		} else {
			// 只读目录（如 dotslash 解压出的工具）：给属主是自己的目录加写权限再删；项本身的目录也允许改
			err = removeAllWritable(p, it.Path)
		}
		if err != nil {
			failedN++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	after, _ := diskUsage(ctx, it.Path)
	o := Outcome{Item: it, Status: StatusDone, Freed: max(it.Size-after, 0)}
	var notes []string
	if protectedN > 0 {
		notes = append(notes, fmt.Sprintf("%d 个受保护的子项没有删", protectedN))
	}
	if failedN > 0 {
		o.Status = StatusFailed
		notes = append(notes, fmt.Sprintf("%d 个子项删除失败：%s", failedN, reasonOf(firstErr)))
	}
	o.Reason = strings.Join(notes, "；")
	return o
}

// runTool 调工具自带的清理命令；释放量 = 扫描时大小 - 清理后大小。
func runTool(ctx context.Context, env *Env, rule *Rule, it Item) Outcome {
	timeout := 2 * time.Minute
	if rule.Tool != nil && rule.Tool.Timeout > 0 {
		timeout = rule.Tool.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := env.Run(ctx, it.Command[0], it.Command[1:]...)
	// 释放量 = 前后之差；能估算的工具（uv）前后都用估算值，口径一致
	after, _ := diskUsage(context.WithoutCancel(ctx), it.Path)
	if rule.Tool != nil && rule.Tool.Estimate != nil {
		if n, ok := rule.Tool.Estimate(context.WithoutCancel(ctx), it.Path); ok {
			after = n
		}
	}
	freed := max(it.Size-after, 0)
	if err != nil && rule.Tool != nil && rule.GuardReason != "" {
		for _, b := range rule.Tool.Busy {
			if strings.Contains(err.Error(), b) {
				return Outcome{Item: it, Status: StatusSkipped, Reason: rule.GuardReason, Freed: freed}
			}
		}
	}
	if err != nil {
		return Outcome{Item: it, Status: StatusFailed, Reason: "命令失败：" + err.Error(), Freed: freed}
	}
	return Outcome{Item: it, Status: StatusDone, Freed: freed}
}

func failed(ctx context.Context, it Item, err error) Outcome {
	after, _ := diskUsage(ctx, it.Path)
	return Outcome{Item: it, Status: StatusFailed, Reason: reasonOf(err), Freed: max(it.Size-after, 0)}
}

func reasonOf(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "没有权限"
	case errors.Is(err, fs.ErrNotExist):
		return "已经不存在"
	}
	return err.Error()
}
