package procs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFindOrphans(t *testing.T) {
	bin := "/Applications/Vole.app/Contents/Resources/analyze"
	out := []byte("" +
		"  100     1   100   501 /Applications/Vole.app/Contents/Resources/analyze\n" + // 孤儿、组长：结束整组
		"  101   100   100   501 /usr/bin/du\n" + //                                       组里的 du：随组结束，不单独列
		"  200   555   200   501 /Applications/Vole.app/Contents/Resources/analyze\n" + // 父进程还在（正在运行的 Vole 起的）：不动
		"  300     1   300   502 /Applications/Vole.app/Contents/Resources/analyze\n" + // 别的用户：不动
		"  400     1   400   501 /opt/homebrew/bin/analyze\n" + //                         用户自己的同名程序：不动
		"  500     1   500   501 /Users/a/My Dev/build/dev-bin/status\n" + //              开发期路径（含空格）
		"  600     1   600   501 /Users/a/My Dev/build/dev-bin/status-old\n" + //          前缀相同的别的程序：不动
		"garbage line\n")
	got := findOrphans(out, []string{bin, "/Users/a/My Dev/build/dev-bin/status", "", "relative/analyze"}, 501, 999)
	if len(got) != 2 || got[0] != (Orphan{100, 100, bin}) || got[1].PID != 500 || got[1].Path != "/Users/a/My Dev/build/dev-bin/status" {
		t.Fatalf("%+v", got)
	}
	if got := findOrphans(out, nil, 501, 999); len(got) != 0 {
		t.Fatalf("没给路径不该结束任何进程：%+v", got)
	}
}

// buildSleeper 编一个只会 sleep 的假二进制（复制系统自带的 sleep 会被系统直接杀掉）。
func buildSleeper(t *testing.T, path string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.go")
	_ = os.WriteFile(src, []byte("package main\nimport \"time\"\nfunc main() { time.Sleep(60 * time.Second) }\n"), 0o644)
	if out, err := exec.Command("go", "build", "-o", path, src).CombinedOutput(); err != nil {
		t.Fatalf("编假二进制：%v\n%s", err, out)
	}
}

// orphan 用一个马上退出的 sh 把 bin 放到后台：sh 一退，bin 的父进程就变成 launchd，模拟 Vole 被 kill -9。
func orphan(t *testing.T, bin string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `"$0" >/dev/null 2>&1 & echo $!`, bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestKillOrphansReal 用真的进程：父进程已经不在的那个被结束；父进程还在的、路径不对的都留着。
func TestKillOrphansReal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Vole dev", "dev-bin") // 路径带空格
	_ = os.MkdirAll(dir, 0o755)
	bin, other := filepath.Join(dir, "analyze"), filepath.Join(dir, "analyze-other")
	buildSleeper(t, bin)
	buildSleeper(t, other)

	lost := orphan(t, bin)       // 「上次的 Vole」留下的
	stranger := orphan(t, other) // 路径不是 Vole 的：不能动
	child := exec.Command(bin)   // 「正在运行的 Vole」起的：父进程还在，不能动
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	time.Sleep(300 * time.Millisecond) // 等 sh 退出、孤儿被 launchd 接管

	found, err := KillOrphans(context.Background(), []string{bin})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].PID != lost {
		t.Fatalf("应只找到 %d：%+v", lost, found)
	}
	for i := 0; i < 50 && alive(lost); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(lost) {
		t.Fatal("孤儿进程应被结束")
	}
	if !alive(stranger) || !alive(child.Process.Pid) {
		t.Fatal("路径不对的、父进程还在的都不能动")
	}
}
