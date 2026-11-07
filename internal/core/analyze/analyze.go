// Package analyze 定义 Vole 自己的磁盘分析数据模型和数据源接口。
// UI 只认这里的类型，不认任何具体扫描器（mole exec / 以后的自研扫描器）的格式。
package analyze

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Status 表示一次测量是否完整。
type Status string

const (
	StatusComplete    Status = "complete"    // 完整
	StatusPartial     Status = "partial"     // 有值但不完整（超时或权限拒绝）
	StatusUnavailable Status = "unavailable" // 没拿到值
)

// Entry 是一行结果：overview 的一个大类，或目录下的一个子项。
type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	IsDir      bool      `json:"is_dir"`
	Insight    bool      `json:"insight,omitempty"`    // 仅 overview：隐藏大户
	Cleanable  bool      `json:"cleanable,omitempty"`  // 可再生、可安全删（保守标记，不是清理清单）
	LastAccess time.Time `json:"last_access,omitzero"` // 零值 = 未知
	Status     Status    `json:"status"`
}

// LargeFile 是递归扫描找到的大文件。
type LargeFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Report 是一次扫描的完整结果。
type Report struct {
	Path       string        `json:"path"`
	Overview   bool          `json:"overview"`
	Status     Status        `json:"status"`
	Entries    []Entry       `json:"entries"`
	LargeFiles []LargeFile   `json:"large_files,omitempty"` // 仅目录模式
	TotalSize  int64         `json:"total_size"`            // 仅目录模式；overview 为 0（Mole 原值重复计数）
	TotalFiles int64         `json:"total_files,omitempty"` // 仅目录模式
	Elapsed    time.Duration `json:"elapsed_ns"`
}

// Request 描述一次扫描。Path 为空 = overview。
type Request struct {
	Path string
}

// Source 是分析数据源。取消与超时走 ctx。
type Source interface {
	Scan(ctx context.Context, req Request) (Report, error)
}

// ErrPathNotFound 表示要扫描的路径不存在。
var ErrPathNotFound = errors.New("路径不存在")

// ErrScannerUnavailable 表示扫描器本身不可用：二进制缺失、无法执行、格式损坏或被系统杀掉。
// 和「某个路径扫失败」不同，它对所有路径都会失败，UI 应整体提示。
var ErrScannerUnavailable = errors.New("分析组件不可用")

// ScanError 表示扫描器本身失败（非零退出、输出无法解析等）。
type ScanError struct {
	ExitCode int    // -1 表示未拿到退出码
	Stderr   string // 已截断
	Err      error
}

func (e *ScanError) Error() string {
	msg := "扫描失败"
	if e.ExitCode >= 0 {
		msg += fmt.Sprintf("（退出码 %d）", e.ExitCode)
	}
	if e.Stderr != "" {
		msg += "：" + e.Stderr
	} else if e.Err != nil {
		msg += "：" + e.Err.Error()
	}
	return msg
}

func (e *ScanError) Unwrap() error { return e.Err }
