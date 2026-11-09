// SPDX-License-Identifier: GPL-3.0-only

// Package clean 是「清理」的后端：规则、只读扫描、按用户确认的清单删除、操作日志。纯 Go，不依赖 UI。
//
// 安全原则（plan 3.8.3）：
//   - 扫描只读，产出清单；删除只执行调用方传进来、用户确认过的清单。
//   - 每一项删除之前重新校验路径、复核身份（dev/inode）、重新判断仍符合规则、重新检查相关 app 是否在运行；
//     任何一项判断不了就跳过，不删。
//   - 可再生缓存永久删除；用户内容移到废纸篓；装了工具时优先调工具自带的清理命令。
//
// 规则语义参考自 tw93/Mole lib/clean/*.sh（commit 6d1af75，GPL-3.0），每条规则注明对应位置。
package clean

import (
	"context"
	"time"

	"vole/internal/core/fsops"
)

// Target 决定一项删的是什么。
type Target int

const (
	// Contents 删目录里的全部内容，保留目录本身（缓存目录，app 期望目录存在）。
	Contents Target = iota
	// Whole 删这一项本身（文件，或整个目录，如旧版本的 DeviceSupport）。
	Whole
)

// Disposal 是删除方式。
type Disposal int

const (
	// Permanent 永久删除：可再生缓存，移到废纸篓腾不出空间。
	Permanent Disposal = iota
	// ToTrash 移到废纸篓：用户自己的内容，删错了能找回。
	ToTrash
)

func (d Disposal) String() string {
	if d == ToTrash {
		return "移到废纸篓"
	}
	return "永久删除"
}

// Tool 是工具自带的清理命令。装了工具时优先用它（工具知道哪些还在用），没装就按路径删。
type Tool struct {
	Bin        string        // 可执行文件名，用 Env.LookPath 查
	Args       []string      // 清理命令参数
	RootArgs   []string      // 可选：问工具缓存目录在哪的只读命令参数，输出一行路径
	RootSuffix string        // 可选：拼在工具给出的目录后面（如 npm 的 _cacache）
	Timeout    time.Duration // 0 = 2 分钟
	Required   bool          // 没装工具就整条不碰（不按路径删）
	Env        []string      // 运行清理命令时加的环境变量（如 UV_LOCK_TIMEOUT=3）
	// GuardOnlyByPath：Guard 只在没装工具、按路径删时生效；交给工具命令时不按进程拦（工具自己有锁）
	GuardOnlyByPath bool
	// Busy：命令失败、且输出里含其中之一时，算「工具正在用这个缓存」，跳过而不是失败（原因用 Rule.GuardReason）
	Busy []string
	// Empty：命令失败、但输出里含其中之一时，是「没有可清理的内容」（如旧版 pip 的 No matching packages），算已清理、不报失败
	Empty []string
	// Estimate 估算工具命令实际能清掉多少（只读）；ok 为 false 时按整个目录的大小算
	Estimate func(ctx context.Context, root string) (n int64, ok bool)
}

// Rule 是一条清理规则。绝大多数规则只用数据字段；少数用 Custom 写代码。
type Rule struct {
	ID       string
	Category string
	Title    string
	Note     string   // 删了会怎样，如「下次构建会重新下载」
	Paths    []string // 以 ~/ 开头，可含 glob；每个匹配到的路径是一项
	Target   Target   // 对目录匹配项生效；文件匹配项总是 Whole
	Default  bool     // 扫描结果里默认勾不勾
	Disposal Disposal

	Guard     []string // 这些进程（可执行文件名，区分大小写）在运行就跳过
	GuardArgs []string // 命令行里含这些字符串的进程在运行就跳过（如 Mole 的 pgrep -f chrome-devtools-mcp）
	Group     string   // 结果页里按它分组（如「Claude」「Codex」）；空 = 不分组
	// GuardReason：守卫进程在运行时显示的原因（空 = 「<进程> 正在运行」）
	GuardReason string
	// GuardText：守卫进程在运行时，按这些进程（PID、命令行）写原因，如「被 uv run 占用：my-mcp-server（PID 123）」；
	// 返回空时退回 GuardReason
	GuardText func(procs []Proc) string
	// Risk：扫描时检查的风险（如 uv 用了 symlink 模式）；返回非空时换掉说明，并且默认不勾
	Risk func(env *Env) string
	// PreDelete：删之前最后一道检查（如 lsof 看有没有程序开着这些文件），返回非空就跳过
	PreDelete func(ctx context.Context, env *Env, path string) string
	// BundleGuard：按目录名判断对应 app 是否在运行（名字像 bundle id 时按 bundle id，否则按进程名 / app 名）；
	// 在运行就挡住；判断不了的列出来、标「需确认」、默认不勾
	BundleGuard bool
	Owner       int // BundleGuard 用往上第几层的目录名（0 = 匹配项自己；如 Application Support/<App>/Cache 用 1）
	// ShowProtected：glob 匹配到的第三方项在保护名单或白名单里时，不丢掉，列出来但置灰并写明原因
	// （com.apple.* 系统项仍然不列）。用户能看到它们占了多少，但永远不会被清理。
	ShowProtected bool
	Exclude       []string // 跳过这些名字（可含 glob；Owner > 0 时比对 app 目录名）：由别的规则处理，或明确不碰
	MinAge        time.Duration
	KeepNewest    int  // 按修改时间保留最新的 N 项
	AnyName       bool // 删内容时不按名字查保护名单（废纸篓：里面是用户扔进去的任意文件）

	Tool   *Tool
	Custom func(ctx context.Context, env *Env) ([]Candidate, error)
}

// Candidate 是规则选出的一项（还没算大小）。
type Candidate struct {
	Path    string
	Target  Target
	Command []string // 非空：交给工具命令清理，第一个元素是可执行文件绝对路径
	Title   string   // 可选的显示名
	Blocked string   // 非空：列出来但不能清理的原因（如受保护）
	Group   string   // 可选：覆盖规则的 Group
	// Confirm：要用户确认才清（默认不勾、标「需确认」），Note 写明原因
	Confirm bool
	Note    string
}

// Identity 是扫描时记下的身份（定义在 core/fsops）。
type Identity = fsops.Identity

// Item 是扫描结果里的一项，也是删除清单里的一项。
type Item struct {
	ID       string // 规则 ID + ":" + 路径（家目录写成 ~），稳定且唯一
	RuleID   string
	Category string
	Title    string
	Path     string
	Target   Target
	Size     int64 // 字节（占用块数 × 512，同 du）；工具命令项是缓存目录当前大小，实际释放可能更少
	Default  bool
	Disposal Disposal
	Command  []string
	Note     string
	Blocked  string // 非空：不能勾选的原因（app 正在运行、需要完全磁盘访问……）
	// SizeUnknown：读不了（比如没有完全磁盘访问），Size 不可信，界面显示「—」
	SizeUnknown bool
	// Uncertain：判断不了是否有 app 正在用它；列出来、默认不勾，用户确认后才清理
	Uncertain bool
	Group     string // 结果页的分组（app 名等）；空 = 单独一行
	Ident     Identity
}

// CategoryDef 是一个类别和它的规则。
type CategoryDef struct {
	ID, Title string
	Rules     []Rule
}

// CategoryResult 是一个类别的扫描结果。
type CategoryResult struct {
	ID, Title string
	Items     []Item // 按大小降序
	Size      int64  // 全部项（含不能勾选的）的总大小
	Note      string // 整类的问题，如扫描超时、没有权限
	Elapsed   time.Duration
}

// ScanResult 是一次扫描的结果。
type ScanResult struct {
	Categories []CategoryResult
	Elapsed    time.Duration
}

// Items 返回全部项，按类别顺序。
func (r *ScanResult) Items() []Item {
	var all []Item
	for _, c := range r.Categories {
		all = append(all, c.Items...)
	}
	return all
}
