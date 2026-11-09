// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Env 是清理用到的全部外部环境。测试里每一项都指向临时目录或假实现，绝不碰本机真实目录。
type Env struct {
	Home   string // 家目录的真实路径（已解析 symlink）；所有规则路径都必须在它下面
	LogDir string // 操作日志目录

	Now       func() time.Time
	LookPath  func(name string) (string, error)                                      // 查工具
	Run       func(ctx context.Context, name string, args ...string) ([]byte, error) // 跑外部命令（工具查询与清理）
	Processes func(ctx context.Context) (*ProcSnapshot, error)                       // 进程快照
	Finder    bool                                                                   // 允许用 Finder 兜底移到废纸篓

	ScanBudget time.Duration // 每个类别的扫描时间预算，0 = 3 分钟

	Getenv func(key string) string // 读环境变量（如 UV_LINK_MODE）；nil = 一律当没设
	EtcDir string                  // 系统配置目录（/etc）；空 = 不读
}

// DefaultEnv 是本机的真实环境。
func DefaultEnv() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("解析家目录失败：%w", err)
	}
	env := &Env{
		Home:     home,
		LogDir:   filepath.Join(home, "Library", "Logs", "Vole"),
		Now:      time.Now,
		LookPath: lookPathIn(toolDirs(home)),
		Run:      runCommand,
		Finder:   true,
		Getenv:   os.Getenv,
		EtcDir:   "/etc",
	}
	env.Processes = func(ctx context.Context) (*ProcSnapshot, error) { return snapshotProcesses(ctx, env.Run) }
	return env, nil
}

// expand 把 ~/ 开头的规则路径换成家目录下的绝对路径。
func (e *Env) expand(p string) string {
	if p == "~" {
		return e.Home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(e.Home, rest)
	}
	return p
}

// pretty 把家目录写成 ~，用于显示和 ID。
func (e *Env) pretty(p string) string {
	if p == e.Home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, e.Home+"/"); ok {
		return "~/" + rest
	}
	return p
}

// toolDirs 是查找工具的目录。GUI app 从 Finder 启动时 PATH 很短，所以补上常见的安装位置。
func toolDirs(home string) []string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	return append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin",
		filepath.Join(home, ".local", "bin"), filepath.Join(home, ".cargo", "bin"), filepath.Join(home, "go", "bin"))
}

func lookPathIn(dirs []string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, d := range dirs {
			if d == "" {
				continue
			}
			p := filepath.Join(d, name)
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				return p, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

// runCommand 跑外部命令：独立进程组，ctx 取消时连同子进程一起杀掉；失败时带上 stderr 的最后几行。
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 300 {
		msg = "…" + msg[len(msg)-300:]
	}
	if ctx.Err() != nil {
		err = ctx.Err() // 超时 / 取消也带上 stderr 的末尾（如 uv 的「Waiting to acquire lock」）
	}
	if err != nil {
		if msg != "" {
			return stdout.Bytes(), fmt.Errorf("%w：%s", err, msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}
