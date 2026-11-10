package procs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseCPUTime(t *testing.T) {
	cases := map[string]time.Duration{
		"0:00.04":    40 * time.Millisecond,
		"226:46.77":  226*time.Minute + 46770*time.Millisecond,
		"1:02:03.50": time.Hour + 2*time.Minute + 3500*time.Millisecond,
		"2-01:00:00": 49 * time.Hour,
	}
	for in, want := range cases {
		got, err := parseCPUTime(in)
		if err != nil || got != want {
			t.Errorf("%q → %v, %v；期望 %v", in, got, err, want)
		}
	}
	if _, err := parseCPUTime("abc"); err == nil {
		t.Error("坏格式应报错")
	}
}

func TestParseScanAndDetails(t *testing.T) {
	scan := []byte(`    1     0     0  18976 226:46.77 Thu Oct  8 17:01:30 2026
  500     1   501   2048   0:01.50 Fri Nov  7 05:13:50 2026
  bad line
`)
	rows := parseScan(scan)
	if len(rows) != 2 || rows[1].pid != 500 || rows[1].uid != 501 || rows[1].rss != 2048 || rows[1].cpu != 1500*time.Millisecond {
		t.Fatalf("parseScan：%+v", rows)
	}
	if rows[0].started.Day() != 8 || rows[0].started.Hour() != 17 {
		t.Fatalf("lstart 解析不对：%v", rows[0].started)
	}
	d := parseDetails([]byte("  500 alice      /Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness Helper (Renderer)\n    1 root     /sbin/launchd\n"))
	if d[500].user != "alice" || d[500].path != "/Applications/DeepSeek Harness.app/Contents/MacOS/DeepSeek Harness Helper (Renderer)" || d[1].path != "/sbin/launchd" {
		t.Fatalf("parseDetails：%+v", d)
	}
}

func TestAppBundle(t *testing.T) {
	p := "/Applications/DeepSeek Harness.app/Contents/Frameworks/DeepSeek Harness Helper (Renderer).app/Contents/MacOS/DeepSeek Harness Helper (Renderer)"
	if b := AppBundle(p); b != "/Applications/DeepSeek Harness.app" || AppName(b) != "DeepSeek Harness" || AppName("") != "" {
		t.Fatalf("got %q %q", b, AppName(b))
	}
	if AppBundle("/usr/libexec/logd") != "" {
		t.Fatal("不在 .app 里应为空")
	}
}

// fakePS 按调用顺序返回预设的 ps 输出，并记录参数。
type fakePS struct {
	scans   [][]byte
	details []byte
	calls   [][]string
}

func (f *fakePS) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if args[0] == "-axo" {
		out := f.scans[0]
		if len(f.scans) > 1 {
			f.scans = f.scans[1:]
		}
		return out, nil
	}
	return f.details, nil
}

func TestSamplerCPUFromDeltas(t *testing.T) {
	line := func(pid, uid int, rss int, cpu string) string {
		return fmt.Sprintf("%d 1 %d %d %s Fri Nov  7 05:13:50 2026\n", pid, uid, rss, cpu)
	}
	f := &fakePS{
		scans: [][]byte{
			[]byte(line(10, 501, 100, "0:01.00") + line(20, 501, 900, "0:05.00") + line(30, 0, 50, "0:00.00")),
			// 2 秒后：10 用了 1.6s CPU（80%），20 用了 0.2s（10%），30 没动；40 是新进程（没有上一次，记 0）
			[]byte(line(10, 501, 100, "0:02.60") + line(20, 501, 900, "0:05.20") + line(30, 0, 50, "0:00.00") + line(40, 501, 10, "0:09.00")),
		},
		details: []byte("10 alice /bin/busy\n20 alice /Applications/X.app/Contents/MacOS/X\n30 root /usr/libexec/idle\n40 alice /bin/new\n"),
	}
	s := newSampler(f.run)
	t0 := time.Now()
	extra := &PIDSet{}
	extra.Set(30, true)
	if _, err := s.sample(context.Background(), t0, Options{Top: 2, Extra: extra}); err != nil {
		t.Fatal(err)
	}
	list, err := s.sample(context.Background(), t0.Add(2*time.Second), Options{Top: 2, Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	// 前 2：10（80%）、20（10%）；30 不在前 2 但被固定，也要带上
	if len(list) != 3 || list[0].PID != 10 || list[1].PID != 20 || list[2].PID != 30 {
		t.Fatalf("列表不对：%+v", list)
	}
	if list[0].CPU < 79.9 || list[0].CPU > 80.1 || list[1].CPU < 9.9 || list[1].CPU > 10.1 {
		t.Fatalf("CPU 应按累计时间差算：%v %v", list[0].CPU, list[1].CPU)
	}
	if list[1].Name != "X" || list[1].User != "alice" || list[1].MemBytes != 900*1024 {
		t.Fatalf("细节不对：%+v", list[1])
	}
	// 路径只对新进入列表的进程查：第二次只该补查没缓存过的
	var detailCalls []string
	for _, c := range f.calls {
		if c[0] == "-o" {
			detailCalls = append(detailCalls, c[len(c)-1])
		}
	}
	if len(detailCalls) != 1 {
		t.Fatalf("两次采样列表没变，细节应只查一次：%v", detailCalls)
	}
}

func TestParseLaunchctl(t *testing.T) {
	svc := []byte(`system = {
	type = system
	services = {
		     161      - 	com.apple.runningboardd
		       0      0 	com.apple.idle
		       -      - 	com.apple.notrunning
	}
	other = {
		     999      - 	not.a.service
	}
}`)
	got := parseServices(svc)
	if len(got) != 1 || got[161] != "com.apple.runningboardd" {
		t.Fatalf("parseServices：%v", got)
	}
	plist, by := parseJobPath([]byte("com.apple.runningboardd = {\n\tactive count = 1\n\tpath = /System/Library/LaunchDaemons/com.apple.runningboardd.plist\n"))
	if plist != "/System/Library/LaunchDaemons/com.apple.runningboardd.plist" || by != "" {
		t.Fatalf("plist：%q %q", plist, by)
	}
	plist, by = parseJobPath([]byte("x = {\n\tpath = (submitted by runningboardd.161)\n"))
	if plist != "" || by != "runningboardd.161" {
		t.Fatalf("submitted：%q %q", plist, by)
	}
}

func TestExplainChainAndJob(t *testing.T) {
	ps := func(_ context.Context, args ...string) ([]byte, error) {
		return []byte("    1     0 /sbin/launchd\n  161     1 /usr/libexec/runningboardd\n  300     1 /Applications/X.app/Contents/MacOS/X\n  400   300 /Applications/X.app/Contents/Frameworks/X Helper.app/Contents/MacOS/X Helper\n"), nil
	}
	launchctl := func(_ context.Context, args ...string) ([]byte, error) {
		switch args[1] {
		case "gui/501":
			return []byte("gui/501 = {\n\tservices = {\n\t\t     300      - \tapplication.x.1\n\t}\n}"), nil
		case "system":
			return nil, errors.New("不让查") // 某个域查不了：跳过，不报错
		case "gui/501/application.x.1":
			return []byte("application.x.1 = {\n\tpath = (submitted by runningboardd.161)\n"), nil
		}
		return nil, errors.New("unexpected")
	}
	ex, err := explain(context.Background(), Proc{PID: 400, PPID: 300}, 501, ps, launchctl)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Chain) != 2 || ex.Chain[0].Name != "X" || ex.Chain[1].PID != 1 || ex.Chain[1].Name != "launchd" {
		t.Fatalf("父进程链：%+v", ex.Chain)
	}
	// 自己不是任务，往上找到父进程 300 是 gui 域的任务
	if ex.Job == nil || ex.Job.PID != 300 || ex.Job.Label != "application.x.1" || ex.Job.SubmittedBy != "runningboardd.161" || ex.Job.Plist != "" {
		t.Fatalf("launchd 任务：%+v", ex.Job)
	}
}

// startSleep 自己 fork 一个 sleep 作为被结束的对象。
func startSleep(t *testing.T) (*exec.Cmd, Proc) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	p, err := Lookup(context.Background(), cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "sleep" || p.UID != os.Getuid() || CanTerminate(p) != "" {
		t.Fatalf("Lookup 不对：%+v（%s）", p, CanTerminate(p))
	}
	return cmd, p
}

// waitSignal 等 sleep 退出并返回它收到的信号。
func waitSignal(t *testing.T, cmd *exec.Cmd) syscall.Signal {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("sleep 应被信号结束：%v", err)
		}
		return ee.Sys().(syscall.WaitStatus).Signal()
	case <-time.After(3 * time.Second):
		t.Fatal("sleep 没有退出")
	}
	return 0
}

func TestTerminateRealProcess(t *testing.T) {
	ctx := context.Background()

	cmd, p := startSleep(t)
	if err := Terminate(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	if sig := waitSignal(t, cmd); sig != syscall.SIGTERM {
		t.Fatalf("终止应发 SIGTERM，收到 %v", sig)
	}
	// 已经退出了再结束一次：ErrGone
	if err := Terminate(ctx, p, false); !errors.Is(err, ErrGone) {
		t.Fatalf("已退出应为 ErrGone：%v", err)
	}

	cmd, p = startSleep(t)
	if err := Terminate(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	if sig := waitSignal(t, cmd); sig != syscall.SIGKILL {
		t.Fatalf("强制退出应发 SIGKILL，收到 %v", sig)
	}
}

func TestTerminateGuards(t *testing.T) {
	ctx := context.Background()

	// 启动时间对不上（模拟 PID 已被复用）：不发信号，进程还活着
	cmd, p := startSleep(t)
	stale := p
	stale.Started = p.Started.Add(-time.Hour)
	if err := Terminate(ctx, stale, true); !errors.Is(err, ErrGone) {
		t.Fatalf("PID 复用应为 ErrGone：%v", err)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("PID 复用防护失败：sleep 被误杀了")
	}

	// 保护名单：不读系统、不发信号
	for _, name := range []string{"WindowServer", "loginwindow", "kernel_task"} {
		fake := p
		fake.Name = name
		if err := Terminate(ctx, fake, true); !errors.Is(err, ErrProtected) {
			t.Fatalf("%s 应受保护：%v", name, err)
		}
		if CanTerminate(fake) == "" {
			t.Fatalf("%s 的菜单项应置灰", name)
		}
	}
	self := Proc{PID: os.Getpid(), UID: os.Getuid(), Name: "vole"}
	if !strings.Contains(CanTerminate(self), "Vole 自己") {
		t.Fatalf("自己应受保护：%q", CanTerminate(self))
	}

	// 别的用户的进程（找一个 root 的）：ErrNotOwner，不会发信号
	out, _ := exec.Command("/bin/ps", "-axo", "pid=,uid=").Output()
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == "0" && f[0] != "1" && f[0] != "0" {
			pid, _ := strconv.Atoi(f[0])
			rp, err := Lookup(ctx, pid)
			if err != nil {
				continue
			}
			if CanTerminate(rp) == "" {
				t.Fatalf("root 进程不应允许结束：%+v", rp)
			}
			if Protected(rp) == "" {
				if err := Terminate(ctx, rp, false); !errors.Is(err, ErrNotOwner) {
					t.Fatalf("root 进程应为 ErrNotOwner：%v", err)
				}
			}
			return
		}
	}
	t.Skip("没找到 root 进程")
}
