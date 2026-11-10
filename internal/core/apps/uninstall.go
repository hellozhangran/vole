// SPDX-License-Identifier: GPL-3.0-only
// 卸载的执行顺序参考自 tw93/Mole lib/uninstall/batch.sh（commit 6d1af75，GPL-3.0）：_batch_execute_removals（:2692）、
// unload_launch_plist（:434）、unregister_app_bundle（:619）、brew 卸载不加 --zap（:2780）；退出 app 参考
// lib/core/app_protection.sh force_kill_app（:2730）。用 Go 重写；和 Mole 的不同：退不掉的 app 跳过不删。

package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vole/internal/core/fsops"
)

// Plan 是卸载一个 app 的清单：app 本体 + 用户勾选的关联文件（只收用户级的）。
type Plan struct {
	App   App
	Files []File
}

// Status 是一个 app 的处理结果。
type Status string

const (
	StatusDone     Status = "已移到废纸篓"
	StatusWould    Status = "将卸载" // dry-run
	StatusSkipped  Status = "跳过"
	StatusFailed   Status = "失败"
	StatusCanceled Status = "已取消"
)

// Outcome 是一个 app 的结果。
type Outcome struct {
	App      App
	Status   Status
	Reason   string
	Moved    int      // 移到废纸篓的项数（含 app 本体）
	Size     int64    // 移走的大小（扫描时的大小；空间要清空废纸篓后才释放）
	Failures []string // 没移走的关联文件和原因
}

// RunOptions 控制一次卸载。
type RunOptions struct {
	DryRun   bool
	Quit     bool // 用户在确认框里同意「退出并移除」：运行中的 app 先退出；false 时运行中的 app 跳过
	Progress func(done, total int, o Outcome)
}

// Report 是一次卸载的结果。
type Report struct {
	Outcomes []Outcome
	Canceled bool
	LogErr   error
}

// Run 按清单逐个卸载。每个 app：复核 → 退出 → 卸载它的用户级 LaunchAgent → 注销 LaunchServices → app 本体和勾选的关联文件移到废纸篓。
// ctx 取消后当前这个 app 做完就停。
func Run(ctx context.Context, env *Env, plans []Plan, opts RunOptions) *Report {
	rep := &Report{}
	log := fsops.NewOpLog(env.LogDir, env.Now())
	action := "uninstall"
	if opts.DryRun {
		action = "uninstall-dry-run"
	}
	log.Line(env.Now(), action, "开始", 0, "", "", fmt.Sprintf("%d 个 app", len(plans)))
	for i, p := range plans {
		if ctx.Err() != nil {
			for _, rest := range plans[i:] {
				o := Outcome{App: rest.App, Status: StatusCanceled, Reason: "用户取消"}
				rep.Outcomes = append(rep.Outcomes, o)
				log.Line(env.Now(), action, string(o.Status), rest.App.Size/1024, rest.App.BundleID, rest.App.Path, o.Reason)
			}
			rep.Canceled = true
			break
		}
		o := runOne(context.WithoutCancel(ctx), env, p, opts, log, action)
		rep.Outcomes = append(rep.Outcomes, o)
		if opts.Progress != nil {
			opts.Progress(i+1, len(plans), o)
		}
	}
	log.Line(env.Now(), action, "结束", 0, "", "", "")
	rep.LogErr = log.Err()
	return rep
}

func runOne(ctx context.Context, env *Env, p Plan, opts RunOptions, log *fsops.OpLog, action string) (o Outcome) {
	app := p.App
	o = Outcome{App: app}
	defer func() {
		log.Line(env.Now(), action, string(o.Status), o.Size/1024, app.BundleID, app.Path, o.Reason)
	}()
	skip := func(r string) Outcome { o.Status, o.Reason = StatusSkipped, r; return o }

	// 1. 复核：还能卸、还在原地、身份和 bundle id 没变
	if app.Blocked != "" {
		return skip(app.Blocked)
	}
	if systemApp(app.BundleID) {
		return skip("系统 app，不能卸载")
	}
	if err := checkApp(env, app.Path); err != nil {
		return skip(err.Error())
	}
	if id, _, err := fsops.IdentityOf(app.Path); err != nil || id != app.Ident {
		return skip("扫描后 app 被移动或替换了")
	}
	if bundleIDOf(ctx, env, app.Path) != app.BundleID {
		return skip("扫描后 app 被替换了（bundle id 变了）")
	}
	// 同一个 bundle id 还有别的副本：数据是共享的，只卸本体（Mole batch.sh:748）
	files := p.Files
	if others := copiesOf(ctx, env, app); len(others) > 0 && len(files) > 0 {
		files = nil
		o.Failures = append(o.Failures, "还有别的副本装着（"+strings.Join(others, "、")+"），关联文件保留")
	}
	var keep []File
	for _, f := range files {
		if f.Kind == KindSystem {
			continue // 系统级不处理
		}
		under := env.Home
		if env.DarwinCache != "" && strings.HasPrefix(f.Path, env.DarwinCache+"/") {
			under = env.DarwinCache
		}
		if err := fsops.CheckPath(f.Path, under); err != nil || protectedForUninstall(f.Path, env.Home) {
			o.Failures = append(o.Failures, prettyPath(env, f.Path)+"：不能删")
			continue
		}
		if id, _, err := fsops.IdentityOf(f.Path); err != nil || id != f.Ident {
			o.Failures = append(o.Failures, prettyPath(env, f.Path)+"：扫描后变了，没动")
			continue
		}
		keep = append(keep, f)
	}

	// 2. 运行中的 app
	var procs []Proc
	if env.Processes != nil {
		var err error
		if procs, err = env.Processes(ctx); err != nil {
			return skip("读不到进程列表，无法确认 app 是否在运行")
		}
	}
	running := len(pidsIn(procs, app.Path)) > 0
	if running && !opts.Quit {
		return skip("正在运行")
	}

	if opts.DryRun {
		o.Status, o.Moved = StatusWould, 1+len(keep)
		o.Size = app.Size + sizeOf(keep)
		how := "移到废纸篓"
		switch {
		case app.Source == SourceHomebrew && app.CaskToken != "":
			how = "brew uninstall --cask " + app.CaskToken
		case app.NeedsAdmin:
			how = "交给 Finder 移到废纸篓（会要管理员密码）"
		}
		o.Reason = how
		if running {
			o.Reason = "先退出 app；" + how
		}
		return o
	}

	if running {
		if err := quitApp(ctx, env, app); err != nil {
			return skip(err.Error())
		}
	}

	// 3. 卸载属于它的用户级 LaunchAgent（plist 文件名就是 bid，或 Program 指向 app 里面）
	for _, f := range keep {
		if f.Agent {
			label := strings.TrimSuffix(filepath.Base(f.Path), ".plist")
			bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, _ = env.Run(bctx, "/bin/launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
			cancel()
		}
	}
	// 4. 注销 LaunchServices（失败不影响卸载）
	uctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, _ = env.Run(uctx, lsregister, "-u", app.Path)
	cancel()

	// 5. app 本体
	if err := removeApp(ctx, env, app); err != nil {
		o.Status, o.Reason = StatusFailed, err.Error()
		return o
	}
	o.Moved, o.Size = 1, app.Size
	log.Line(env.Now(), action, "已移到废纸篓", app.Size/1024, app.BundleID, app.Path, "app 本体")

	// 6. 勾选的关联文件
	for _, f := range keep {
		if err := fsops.MoveToTrash(ctx, env.trashEnv(), f.Path); err != nil {
			o.Failures = append(o.Failures, prettyPath(env, f.Path)+"："+err.Error())
			log.Line(env.Now(), action, "失败", f.Size/1024, app.BundleID, f.Path, err.Error())
			continue
		}
		o.Moved++
		o.Size += f.Size
		log.Line(env.Now(), action, "已移到废纸篓", f.Size/1024, app.BundleID, f.Path, f.Kind.String())
	}
	o.Status = StatusDone
	if len(o.Failures) > 0 {
		o.Reason = fmt.Sprintf("%d 项关联文件没有移走", len(o.Failures))
	}
	return o
}

const lsregister = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// checkApp：app 路径必须在扫描根目录里、是 .app、本身和上级都不是 symlink、不是系统关键路径。
func checkApp(env *Env, p string) error {
	if p != filepath.Clean(p) || !filepath.IsAbs(p) || !strings.HasSuffix(strings.ToLower(p), ".app") {
		return errors.New("路径不规范")
	}
	inRoot := false
	for _, r := range env.Roots {
		if strings.HasPrefix(p, filepath.Clean(r)+"/") {
			inRoot = true
		}
	}
	if !inRoot {
		return errors.New("不在应用程序目录里")
	}
	if fsops.CriticalPath(p, env.Home) {
		return errors.New("系统关键路径")
	}
	info, err := os.Lstat(p)
	if err != nil {
		return errors.New("已经不存在")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil // 指向别处的 app 别名：只移走别名本身
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(p)); err != nil || real != filepath.Dir(p) {
		return errors.New("上级目录经过符号链接")
	}
	return nil
}

// copiesOf：卸载前再找一遍同 bundle id 的别的副本（不算大小，很快）。
func copiesOf(ctx context.Context, env *Env, app App) []string {
	var out []string
	for _, d := range discover(env) {
		if d.path != app.Path && bundleIDOf(ctx, env, d.path) == app.BundleID {
			out = append(out, prettyPath(env, d.path))
		}
	}
	return out
}

// quitApp：先正常退出（AppleScript quit，让 app 自己保存）→ 等 QuitWait → TERM → 等 KillWait → KILL → 等 KillWait；
// 还在就返回错误（跳过不删）。只发给这个 app 里面的进程（按可执行文件路径），系统进程名一律不碰。
func quitApp(ctx context.Context, env *Env, app App) error {
	switch strings.ToLower(app.Executable) {
	case "finder", "dock", "systemuiserver", "loginwindow", "windowserver":
		return errors.New("系统进程，不能退出")
	}
	alive := func() []int {
		procs, err := env.Processes(ctx)
		if err != nil {
			return []int{-1}
		}
		return pidsIn(procs, app.Path)
	}
	wait := func(d time.Duration) bool {
		deadline := env.Now().Add(d)
		for {
			if len(alive()) == 0 {
				return true
			}
			if !env.Now().Before(deadline) {
				return false
			}
			select {
			case <-ctx.Done():
				return false
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if app.BundleID != "" {
		qctx, cancel := context.WithTimeout(ctx, env.quitWait())
		_, _ = env.Run(qctx, "/usr/bin/osascript", "-e", fmt.Sprintf(`tell application id "%s" to quit`, strings.ReplaceAll(app.BundleID, `"`, "")))
		cancel()
		if wait(env.quitWait()) {
			return nil
		}
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		for _, pid := range alive() {
			if pid > 0 {
				_ = env.Signal(pid, sig)
			}
		}
		if wait(env.killWait()) {
			return nil
		}
	}
	return errors.New("没能退出，请手动退出后再试")
}

// removeApp 移走 app 本体：Homebrew 装的交给 brew（不加 --zap）；当前用户能改的直接移到废纸篓；
// root 拥有的交给 Finder，由系统弹管理员密码框。
func removeApp(ctx context.Context, env *Env, app App) error {
	if app.Source == SourceHomebrew && app.CaskToken != "" {
		if brew, err := env.LookPath("brew"); err == nil {
			bctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			_, err := env.Run(bctx, brew, "uninstall", "--cask", app.CaskToken)
			if _, statErr := os.Lstat(app.Path); err == nil && os.IsNotExist(statErr) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("brew 卸载失败：%v", err)
			}
			return errors.New("brew 卸载后 app 还在")
		}
	}
	if !app.NeedsAdmin {
		if err := fsops.MoveToTrash(ctx, fsops.TrashEnv{Home: env.Home, Now: env.Now}, app.Path); err == nil {
			return nil
		} else if !env.Finder {
			return err
		}
	}
	if !env.Finder {
		return errors.New("需要管理员权限")
	}
	return finderError(fsops.MoveToTrashViaFinderWithin(ctx, app.Path, 3*time.Minute)) // 要等用户输管理员密码
}

// finderError 把 Finder 的报错翻成用户能看懂的话。
func finderError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "-1743") || strings.Contains(msg, "Not authorized"):
		return errors.New("Vole 没有控制 Finder 的权限：到「系统设置 › 隐私与安全性 › 自动化」里允许 Vole 控制「访达」后再试")
	case strings.Contains(msg, "-128") || strings.Contains(msg, "User canceled") || strings.Contains(msg, "用户已取消"):
		return errors.New("已取消（没有输入管理员密码）")
	case strings.Contains(msg, "超时"):
		return errors.New("等 Finder 超时了（可能在等管理员密码）")
	}
	return err
}

func sizeOf(fs []File) int64 {
	var n int64
	for _, f := range fs {
		n += f.Size
	}
	return n
}

func prettyPath(env *Env, p string) string {
	if rest, ok := strings.CutPrefix(p, env.Home+"/"); ok {
		return "~/" + rest
	}
	return p
}
