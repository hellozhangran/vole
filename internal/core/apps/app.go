// SPDX-License-Identifier: GPL-3.0-only
// 卸载的整体做法参考自 tw93/Mole bin/uninstall.sh、lib/uninstall/batch.sh（commit 6d1af75，GPL-3.0），用 Go 重写，
// 只做用户级、不提权；每处规则在对应函数的注释里写了 Mole 出处。

// Package apps 是「软件」tab 的后端：已装 app 清单、关联文件查找、退出 app、卸载（移到废纸篓）。纯 Go，不依赖 UI。
package apps

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vole/internal/core/fsops"
)

// Source 是 app 的来源。
type Source string

const (
	SourceOther    Source = ""
	SourceHomebrew Source = "homebrew" // Homebrew cask：卸载走 brew uninstall --cask（不加 --zap）
	SourceAppStore Source = "appstore" // Mac App Store：有 Contents/_MASReceipt
	SourceSteam    Source = "steam"    // Steam 生成的启动壳：游戏本体由 Steam 管理
)

// App 是一个已装的 app。
type App struct {
	Path       string // .app 的路径
	BundleID   string
	Name       string // 显示名（按系统语言本地化）
	Version    string // CFBundleShortVersionString
	Executable string // Contents/MacOS 下的可执行文件名

	Size      int64 // 字节，口径同 du
	SizeKnown bool
	LastUsed  time.Time // 上次使用；取不到时为零值
	Installed time.Time // 安装日期（排序用）

	Source    Source
	CaskToken string // Homebrew cask 的名字
	Intel     bool   // 可执行文件只有 x86_64，要 Rosetta 才能跑
	MenuBar   bool   // LSUIElement：没有 Dock 图标的菜单栏 app
	Running   bool   // 有进程跑在这个 .app 里

	NeedsAdmin bool   // app 或它所在的目录当前用户不可写：要交给 Finder 移到废纸篓（系统会弹管理员密码框）
	Blocked    string // 非空：不能卸的原因（Vole 自己、要用厂商自己的卸载器）

	Ident fsops.Identity
}

// Idle 是很久没用：上次使用超过 3 个月。
func (a App) Idle(now time.Time) bool {
	return !a.LastUsed.IsZero() && now.Sub(a.LastUsed) > 90*24*time.Hour
}

// Proc 是一个进程：PID 和可执行文件的完整路径。
type Proc struct {
	PID  int
	Path string
}

// Env 是卸载用到的全部外部环境。测试里每一项都指向临时目录或假实现。
type Env struct {
	Home        string   // 家目录（已解析 symlink）
	Roots       []string // 扫 app 的目录
	CaskRoots   []string // Homebrew 的 Caskroom
	LogDir      string   // 操作日志目录（和清理共用 operations.log）
	DarwinCache string   // getconf DARWIN_USER_CACHE_DIR（/var/folders/.../C）；空 = 不找
	Languages   []string // 系统首选语言（AppleLanguages），取本地化显示名
	SelfID      string   // Vole 自己的 bundle id：不允许卸载

	Now       func() time.Time
	Run       func(ctx context.Context, name string, args ...string) ([]byte, error)
	LookPath  func(name string) (string, error)
	Processes func(ctx context.Context) ([]Proc, error)
	Signal    func(pid int, sig syscall.Signal) error
	Finder    bool // 允许用 Finder 移到废纸篓（root 拥有的 app 只能这样）

	QuitWait time.Duration // 正常退出后最多等多久（默认 5 秒）
	KillWait time.Duration // TERM、KILL 之后各等多久（默认 2 秒）
}

// SelfBundleID 是 Vole 自己的 bundle id（mygo.json 的 identifier）。
const SelfBundleID = "dev.vole.app"

// DefaultEnv 是本机的真实环境。
func DefaultEnv() (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if home, err = filepath.EvalSymlinks(home); err != nil {
		return nil, err
	}
	env := &Env{
		Home: home,
		// Mole bin/uninstall.sh:525：/Applications、~/Applications、两处 Input Methods
		Roots:     []string{"/Applications", filepath.Join(home, "Applications"), "/Library/Input Methods", filepath.Join(home, "Library", "Input Methods")},
		CaskRoots: []string{"/opt/homebrew/Caskroom", "/usr/local/Caskroom"},
		LogDir:    filepath.Join(home, "Library", "Logs", "Vole"),
		SelfID:    SelfBundleID,
		Now:       time.Now,
		Run:       runCommand,
		LookPath:  lookPath,
		Signal:    syscall.Kill,
		Finder:    true,
	}
	env.Processes = func(ctx context.Context) ([]Proc, error) { return listProcesses(ctx, env.Run) }
	if out, err := env.Run(context.Background(), "/usr/bin/getconf", "DARWIN_USER_CACHE_DIR"); err == nil {
		if d, err := filepath.EvalSymlinks(strings.TrimSpace(string(out))); err == nil {
			env.DarwinCache = d // /var → /private/var：用真实路径，删前校验才过得了
		}
	}
	// Vole 的界面是简体中文：显示名先取简体中文，再按系统首选语言（Mole 命令行版只按系统语言；Mole app 显示的也是中文名）
	env.Languages = append([]string{"zh-Hans"}, preferredLanguages(env.Run)...)
	return env, nil
}

func (e *Env) quitWait() time.Duration {
	if e.QuitWait > 0 {
		return e.QuitWait
	}
	return 5 * time.Second
}

func (e *Env) killWait() time.Duration {
	if e.KillWait > 0 {
		return e.KillWait
	}
	return 2 * time.Second
}

func (e *Env) trashEnv() fsops.TrashEnv {
	return fsops.TrashEnv{Home: e.Home, Now: e.Now, Finder: e.Finder}
}

// runCommand 跑外部命令，失败时把 stderr 的末尾带进错误。
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = "…" + msg[len(msg)-300:]
		}
		if msg != "" {
			return out, &cmdError{err: err, msg: msg}
		}
		return out, err
	}
	return out, nil
}

type cmdError struct {
	err error
	msg string
}

func (e *cmdError) Error() string { return e.err.Error() + "：" + e.msg }
func (e *cmdError) Unwrap() error { return e.err }

// lookPath 在 PATH 和 Homebrew 的常见位置里找工具（从 Finder 打开时 PATH 很短）。
func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		p := filepath.Join(d, name)
		if info, err := os.Stat(p); err == nil && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// listProcesses：全部进程的 PID 和可执行文件路径（ps 的 comm 在最后一列时是完整路径，含空格）。
func listProcesses(ctx context.Context, run func(context.Context, string, ...string) ([]byte, error)) ([]Proc, error) {
	out, err := run(ctx, "/bin/ps", "-axww", "-o", "pid=,comm=")
	if err != nil {
		return nil, err
	}
	var ps []Proc
	for _, l := range strings.Split(string(out), "\n") {
		pid, path, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(pid); err == nil {
			ps = append(ps, Proc{PID: n, Path: strings.TrimSpace(path)})
		}
	}
	return ps, nil
}

// preferredLanguages 读系统首选语言（defaults read -g AppleLanguages），如 ["zh-Hans-CN", "en-CN"]。
func preferredLanguages(run func(context.Context, string, ...string) ([]byte, error)) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := run(ctx, "/usr/bin/defaults", "read", "-g", "AppleLanguages")
	if err != nil {
		return nil
	}
	var langs []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.Trim(strings.TrimSpace(l), `",()`)
		if l != "" {
			langs = append(langs, l)
		}
	}
	return langs
}
