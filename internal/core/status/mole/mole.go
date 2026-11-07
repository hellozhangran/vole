// Package mole 实现 status.Source：exec 随 Vole 打包的 status 二进制（编译自 Mole cmd/status），
// 用 --watch 模式逐行读 NDJSON。只做进程调用 + 解析，不引用、不拷贝 Mole 源码。
package mole

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"vole/internal/core/status"
)

// Source 调用 status 二进制。Binary 为绝对路径，由 binloc 定位。
type Source struct {
	Binary string
}

var _ status.Source = (*Source)(nil)

// maxBadLines 连续这么多行解析失败就当采集器坏了，停掉重来。
const maxBadLines = 3

// Watch 启动 status --watch，每解析出一帧就 emit；ctx 取消时杀掉整个进程组。
func (s *Source) Watch(ctx context.Context, interval time.Duration, emit func(status.Snapshot)) error {
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.Binary, "--watch", "--interval", interval.String())
	// status 会起 ioreg / system_profiler 等子进程，取消时连同子进程一起杀掉
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	stderr := &tailBuffer{limit: 4 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return &status.ExitError{ExitCode: -1, Err: fmt.Errorf("%w：%w", status.ErrUnavailable, err)}
	}

	frames, bad := 0, 0
	var parseErr error
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20) // 一帧几 KB，留足余量
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		snap, err := parse(line)
		if err != nil {
			bad++
			parseErr = err
			if bad >= maxBadLines {
				cancel() // 输出持续不可解析：杀掉，按意外退出处理
				break
			}
			continue
		}
		bad = 0
		frames++
		emit(snap)
	}
	waitErr := cmd.Wait()
	if outer := ctx.Err(); outer != nil && bad < maxBadLines {
		// 调用方取消：正常结束
		return outer
	}

	e := &status.ExitError{ExitCode: -1, Stderr: stderr.String(), Frames: frames, Err: waitErr}
	var exitErr *exec.ExitError
	switch {
	case bad >= maxBadLines:
		e.Err = parseErr
	case errors.As(waitErr, &exitErr) && exitErr.ExitCode() >= 0:
		e.ExitCode = exitErr.ExitCode()
	case waitErr != nil:
		// 被信号杀掉（如签名校验失败）：采集器本身坏了
		e.Err = fmt.Errorf("%w：%w", status.ErrUnavailable, waitErr)
	default:
		e.ExitCode = 0
		e.Err = errors.New("进程自行退出")
	}
	return e
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
