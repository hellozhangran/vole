// Package mole 实现 analyze.Source：exec 随 Vole 打包的 analyze 二进制（编译自 Mole cmd/analyze）。
// 只做进程调用 + 解析 JSON，不引用、不拷贝 Mole 源码（GPL-3 约束，见 THIRD_PARTY_NOTICES.md）。
package mole

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vole/internal/core/analyze"
	"vole/internal/i18n"
)

// Source 调用 analyze 二进制。Binary 为绝对路径，由 binloc 定位。
type Source struct {
	Binary string
}

var _ analyze.Source = (*Source)(nil)

// Scan 跑一次 analyze --json [PATH]，阻塞到扫描结束；ctx 取消或超时会杀掉整个进程组。
func (s *Source) Scan(ctx context.Context, req analyze.Request) (analyze.Report, error) {
	args := []string{"--json"}
	if req.Path != "" {
		abs, err := checkDir(req.Path)
		if err != nil {
			return analyze.Report{}, err
		}
		args = append(args, abs)
	}

	cmd := exec.CommandContext(ctx, s.Binary, args...)
	cmd.Env = scanEnv(os.Environ())
	// analyze 会再起 du / mdls 子进程，超时要连同子进程一起杀掉
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var stdout bytes.Buffer
	stderr := &tailBuffer{limit: 4 << 10}
	cmd.Stdout, cmd.Stderr = &stdout, stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return analyze.Report{}, fmt.Errorf(i18n.T("扫描已中止（%s）：%w"), reason(ctxErr), ctxErr)
	}
	if err != nil {
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		se := &analyze.ScanError{ExitCode: code, Stderr: stderr.String(), Err: err}
		if code == -1 {
			// 没能启动（不存在 / 不可执行 / 格式错误）或被信号杀掉（如签名校验失败）：扫描器本身坏了
			se.Err = fmt.Errorf(i18n.T("%w：%w"), analyze.ErrScannerUnavailable, err)
		}
		return analyze.Report{}, se
	}

	report, err := parse(stdout.Bytes())
	if err != nil {
		return analyze.Report{}, &analyze.ScanError{ExitCode: 0, Stderr: stderr.String(), Err: err}
	}
	report.Elapsed = elapsed
	return report, nil
}

// checkDir 在 exec 前给出友好错误，避免依赖 analyze 的 stderr 文案。
func checkDir(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf(i18n.T("%w：%s"), analyze.ErrPathNotFound, abs)
	}
	if err != nil {
		return "", fmt.Errorf(i18n.T("无法访问 %s：%w"), abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf(i18n.T("不是目录：%s"), abs)
	}
	return abs, nil
}

// scanEnv 去掉 MO_ANALYZE_PATH：它会覆盖命令行路径，让 overview 变成目录扫描。
func scanEnv(base []string) []string {
	env := make([]string, 0, len(base))
	for _, kv := range base {
		if !strings.HasPrefix(kv, "MO_ANALYZE_PATH=") {
			env = append(env, kv)
		}
	}
	return env
}

func reason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return i18n.T("超时")
	}
	return i18n.T("已取消")
}

// tailBuffer 只保留最后 limit 字节，防止异常输出撑爆内存。
type tailBuffer struct {
	buf   []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return strings.TrimSpace(string(b.buf)) }
