package mole

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"vole/internal/core/analyze"
)

func TestParseOverview(t *testing.T) {
	data, _ := os.ReadFile("testdata/overview.json")
	r, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Overview || r.Status != analyze.StatusPartial || len(r.Entries) != 3 {
		t.Fatalf("结果不对：%+v", r)
	}
	if r.TotalSize != 0 {
		t.Fatalf("overview 的 total_size 重复计数，应丢弃，got %d", r.TotalSize)
	}
	uv := r.Entries[1]
	if !uv.Insight || !uv.Cleanable || uv.Status != analyze.StatusComplete {
		t.Fatalf("insight/cleanable 没解析出来：%+v", uv)
	}
	if r.Entries[2].Status != analyze.StatusUnavailable {
		t.Fatalf("unavailable 没解析出来")
	}
}

func TestParseDir(t *testing.T) {
	data, _ := os.ReadFile("testdata/dir.json")
	r, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Overview || r.TotalSize != 5301 || r.TotalFiles != 42 || len(r.LargeFiles) != 1 {
		t.Fatalf("目录模式汇总不对：%+v", r)
	}
	want := time.Date(2025, 6, 30, 10, 36, 8, 0, time.UTC)
	if !r.Entries[2].LastAccess.Equal(want) {
		t.Fatalf("last_access：got %v", r.Entries[2].LastAccess)
	}
	x := r.Entries[3]
	if !x.LastAccess.IsZero() || x.Status != analyze.StatusUnavailable {
		t.Fatalf("异常字段应降级为未知：%+v", x)
	}
}

func TestParseBad(t *testing.T) {
	for _, in := range []string{"", "not json", `{"entries":[]}`} {
		if _, err := parse([]byte(in)); err == nil {
			t.Fatalf("%q 应报错", in)
		}
	}
}

// fakeBinary 写一个假的 analyze：按 FAKE_MODE 输出 fixture / 失败 / 挂住，并记录参数与子进程 pid。
func fakeBinary(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	fixture, _ := filepath.Abs("testdata/dir.json")
	script := `#!/bin/sh
echo "$@" > "` + dir + `/args"
case "$FAKE_MODE" in
  ok)   cat "` + fixture + `" ;;
  fail) echo "failed to scan directory: boom" >&2; exit 1 ;;
  garbage) echo "hello" ;;
  hang) sleep 30 & echo $! > "` + dir + `/child"; wait ;;
esac
`
	bin = filepath.Join(dir, "analyze")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestScan(t *testing.T) {
	bin, dir := fakeBinary(t)
	src := &Source{Binary: bin}
	ctx := context.Background()

	t.Setenv("MO_ANALYZE_PATH", "/should/be/removed")
	t.Setenv("FAKE_MODE", "ok")
	r, err := src.Scan(ctx, analyze.Request{})
	if err != nil || r.TotalFiles != 42 {
		t.Fatalf("ok：%v %+v", err, r)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.TrimSpace(string(args)) != "--json" {
		t.Fatalf("overview 不应带路径：%q", args)
	}
	if _, err := src.Scan(ctx, analyze.Request{Path: dir}); err != nil {
		t.Fatal(err)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.TrimSpace(string(args)) != "--json "+dir {
		t.Fatalf("目录模式参数不对：%q", args)
	}

	// 路径不存在：exec 前就拦住
	if _, err := src.Scan(ctx, analyze.Request{Path: filepath.Join(dir, "nope")}); !errors.Is(err, analyze.ErrPathNotFound) {
		t.Fatalf("应为 ErrPathNotFound：%v", err)
	}

	// 非零退出
	t.Setenv("FAKE_MODE", "fail")
	_, err = src.Scan(ctx, analyze.Request{})
	var se *analyze.ScanError
	if !errors.As(err, &se) || se.ExitCode != 1 || !strings.Contains(se.Stderr, "boom") {
		t.Fatalf("fail：%v", err)
	}

	// 输出无法解析
	t.Setenv("FAKE_MODE", "garbage")
	if _, err := src.Scan(ctx, analyze.Request{}); !errors.As(err, &se) {
		t.Fatalf("garbage：%v", err)
	}

	// 二进制不存在
	if _, err := (&Source{Binary: filepath.Join(dir, "missing")}).Scan(ctx, analyze.Request{}); !errors.As(err, &se) || !errors.Is(err, analyze.ErrScannerUnavailable) {
		t.Fatalf("missing 应为 ErrScannerUnavailable：%v", err)
	}

	// 二进制损坏（截断成垃圾字节）：无法执行，也算扫描器不可用
	broken := filepath.Join(dir, "broken")
	_ = os.WriteFile(broken, []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 1, 2}, 0o755)
	if _, err := (&Source{Binary: broken}).Scan(ctx, analyze.Request{}); !errors.Is(err, analyze.ErrScannerUnavailable) {
		t.Fatalf("broken 应为 ErrScannerUnavailable：%v", err)
	}
	// 正常退出码 1 不算不可用
	t.Setenv("FAKE_MODE", "fail")
	if _, err := src.Scan(ctx, analyze.Request{}); errors.Is(err, analyze.ErrScannerUnavailable) {
		t.Fatalf("退出码 1 不应算扫描器不可用：%v", err)
	}
}

func TestScanCancelKillsChildren(t *testing.T) {
	bin, dir := fakeBinary(t)
	t.Setenv("FAKE_MODE", "hang")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errc := make(chan error, 1)
	go func() {
		_, err := (&Source{Binary: bin}).Scan(ctx, analyze.Request{})
		errc <- err
	}()

	// 等假 analyze 把子进程拉起来再取消，避免机器负载高时测不到子进程
	childFile := filepath.Join(dir, "child")
	var pid int
	for i := 0; i < 100 && pid <= 0; i++ {
		data, _ := os.ReadFile(childFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		time.Sleep(20 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("没拿到子进程 pid")
	}
	cancel()

	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("应为取消：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后没有及时返回")
	}
	// 子进程应已随进程组被杀
	for i := 0; i < 40; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("子进程 %d 仍存活", pid)
}
