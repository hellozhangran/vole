package procs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"vole/internal/i18n"
)

// 结束进程的失败原因。
var (
	ErrProtected  = errors.New(i18n.T("系统关键进程，不能结束"))
	ErrNotOwner   = errors.New(i18n.T("需要管理员权限"))
	ErrGone       = errors.New(i18n.T("进程已经退出"))
	ErrPermission = errors.New(i18n.T("权限不够，系统拒绝了这个操作"))
)

// protected 是不允许从 Vole 结束的进程（按可执行文件名），值是原因。
var protected = map[string]string{
	"kernel_task":  i18n.T("系统内核，不能结束"),
	"launchd":      i18n.T("系统的第一个进程，结束会让系统崩溃"),
	"loginwindow":  i18n.T("结束会立即注销当前用户，未保存的工作会丢失"),
	"WindowServer": i18n.T("结束会让整个图形界面重启，所有窗口都会关闭"),
}

// Protected 返回进程在保护名单里的原因；不在名单里返回空字符串。
// 名单：kernel_task、launchd（含 PID 0/1）、loginwindow、WindowServer，以及 Vole 自己。
func Protected(p Proc) string {
	if p.PID <= 1 {
		return i18n.T("系统核心进程，不能结束")
	}
	if p.PID == os.Getpid() {
		return i18n.T("这是 Vole 自己，请用 Cmd+Q 退出")
	}
	return protected[p.Name]
}

// CanTerminate 返回当前用户不能结束这个进程的原因；可以结束时返回空字符串。
// 只允许结束当前用户自己的进程；别的用户或 root 的进程返回「需要管理员权限」。
func CanTerminate(p Proc) string {
	if r := Protected(p); r != "" {
		return r
	}
	if p.UID != os.Getuid() {
		return ErrNotOwner.Error()
	}
	return ""
}

// Terminate 结束进程：force 为 false 时发 SIGTERM（进程可以先保存再退出），true 时发 SIGKILL（立即结束，来不及保存）。
//
// 发信号前会重新读一次这个 PID 的属主和启动时间，并复核：
//   - 进程还在，且启动时间和 p.Started 一致——否则说明它已退出、PID 可能已被别的进程复用，返回 ErrGone，绝不误杀；
//   - 属主是当前用户，不在保护名单（CanTerminate）——否则返回 ErrNotOwner / ErrProtected。
//
// 信号发出后不等待进程真正退出（SIGTERM 下进程可能先收尾）；系统返回 ESRCH 视为 ErrGone，EPERM 视为 ErrPermission。
func Terminate(ctx context.Context, p Proc, force bool) error {
	return terminate(ctx, p, force, runPS)
}

func terminate(ctx context.Context, p Proc, force bool, ps runFunc) error {
	if r := Protected(p); r != "" {
		return fmt.Errorf(i18n.T("%w：%s"), ErrProtected, r)
	}
	out, err := ps(ctx, "-o", "uid=,lstart=", "-p", strconv.Itoa(p.PID))
	if err != nil {
		return fmt.Errorf(i18n.T("读取进程信息失败：%w"), err)
	}
	f := strings.Fields(string(out))
	if len(f) != 6 {
		return ErrGone
	}
	uid, err := strconv.Atoi(f[0])
	if err != nil {
		return ErrGone
	}
	started, err := time.ParseInLocation(lstartLayout, strings.Join(f[1:], " "), time.Local)
	if err != nil || !started.Equal(p.Started) {
		return ErrGone // PID 已被复用给别的进程
	}
	if uid != os.Getuid() {
		return ErrNotOwner
	}

	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	switch err := syscall.Kill(p.PID, sig); {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ESRCH):
		return ErrGone
	case errors.Is(err, syscall.EPERM):
		return ErrPermission
	default:
		return err
	}
}

// Lookup 读一个进程的当前信息（不含 CPU），用于测试和复核；进程不存在返回 ErrGone。
func Lookup(ctx context.Context, pid int) (Proc, error) {
	out, err := runPS(ctx, "-o", "pid=,ppid=,uid=,rss=,time=,lstart=", "-p", strconv.Itoa(pid))
	if err != nil {
		return Proc{}, err
	}
	rows := parseScan(out)
	if len(rows) != 1 {
		return Proc{}, ErrGone
	}
	r := rows[0]
	d := parseDetails(must(runPS(ctx, "-o", "pid=,user=,comm=", "-p", strconv.Itoa(pid))))[pid]
	return Proc{PID: r.pid, PPID: r.ppid, UID: r.uid, User: d.user, Name: baseName(d.path, pid), Path: d.path,
		MemBytes: r.rss * 1024, Started: r.started}, nil
}

func must(b []byte, _ error) []byte { return b }
