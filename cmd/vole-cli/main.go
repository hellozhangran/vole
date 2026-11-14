// vole-cli 在命令行验证 Vole 的纯 Go 后端，不依赖任何 UI。
//
//	vole-cli analyze [--json] [--timeout 5m] [--limit 30] [PATH]   无 PATH = overview
//	vole-cli status [--frames 3] [--interval 1s] [--json]           打几帧实时系统状态就退出
//	vole-cli doctor                                                二进制定位 + 环境探测
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"vole/internal/core/analyze"
	"vole/internal/core/analyze/mole"
	"vole/internal/core/binloc"
	"vole/internal/core/status"
	statusmole "vole/internal/core/status/mole"
	"vole/internal/core/sysenv"
	"vole/internal/i18n"
)

var usage = i18n.T(`用法：
  vole-cli analyze [--json] [--timeout 5m] [--limit 30] [PATH]   无 PATH = overview
  vole-cli status [--frames 3] [--interval 1s] [--json]           打几帧实时系统状态就退出
  vole-cli clean scan | run --dry-run|--yes ...                  清理：只读扫描 / 按指定类别或项清理（详见 vole-cli clean）
  vole-cli apps list | show <app> | remove --dry-run|--yes <app>...  软件：已装 app、关联文件、卸载（详见 vole-cli apps）
  vole-cli doctor                                                二进制定位 + 环境探测

开发期用 VOLE_ANALYZE_BIN / VOLE_STATUS_BIN 指定二进制（scripts/build-mole.sh analyze|status 编译）。`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("错误：")+err.Error())
		var nf *binloc.NotFoundError
		if errors.As(err, &nf) {
			fmt.Fprintln(os.Stderr, i18n.T("提示：开发期请设置"), binloc.AnalyzeEnv, "/", binloc.StatusEnv, i18n.T("指向 scripts/build-mole.sh 编出的二进制"))
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Println(usage)
		return nil
	}
	switch args[0] {
	case "analyze":
		return runAnalyze(args[1:])
	case "status":
		return runStatus(args[1:])
	case "apps":
		return runApps(args[1:])
	case "clean":
		return runClean(args[1:])
	case "doctor":
		return runDoctor()
	case "-h", "--help", "help":
		fmt.Println(usage)
		return nil
	}
	return fmt.Errorf(i18n.T("未知子命令 %q\n%s"), args[0], usage)
}

func runAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, i18n.T("输出 Vole 的 Report JSON"))
	timeout := fs.Duration("timeout", 10*time.Minute, i18n.T("整体超时，到点杀掉扫描进程"))
	limit := fs.Int("limit", 30, i18n.T("目录模式最多列出多少条（0 = 全部）"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New(i18n.T("只能指定一个路径"))
	}

	bin, err := binloc.Locate(binloc.AnalyzeName, binloc.AnalyzeEnv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	var src analyze.Source = &mole.Source{Binary: bin}
	if !*asJSON {
		fmt.Fprintln(os.Stderr, i18n.T("扫描中（analyze 不输出进度，请等待）…"))
	}
	report, err := src.Scan(ctx, analyze.Request{Path: fs.Arg(0)})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printReport(report, *limit)
	return nil
}

func printReport(r analyze.Report, limit int) {
	mode := i18n.T("目录")
	if r.Overview {
		mode = "overview"
	}
	fmt.Printf(i18n.T("%s  %s  状态=%s  耗时=%s\n\n"), mode, r.Path, r.Status, r.Elapsed.Round(100*time.Millisecond))

	entries := r.Entries
	if !r.Overview && limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	for _, e := range entries {
		fmt.Printf("  %10s  %-11s  %-4s %s%s\n", sizeText(e.Size, e.Status), e.Status, kind(e), e.Name, flags(e))
	}
	if len(entries) < len(r.Entries) {
		fmt.Printf(i18n.T("  … 还有 %d 条（--limit 0 显示全部）\n"), len(r.Entries)-len(entries))
	}

	if r.Overview {
		fmt.Println(i18n.T("\n  注：overview 各行有重叠（insight 行在 Home/User Library 内），不汇总总量。"))
	} else {
		fmt.Printf(i18n.T("\n  合计 %s，%d 个文件\n"), humanSize(r.TotalSize), r.TotalFiles)
		if len(r.LargeFiles) > 0 {
			fmt.Printf(i18n.T("\n大文件 top %d：\n"), len(r.LargeFiles))
			for _, f := range r.LargeFiles {
				fmt.Printf("  %10s  %s\n", humanSize(f.Size), f.Path)
			}
		}
	}

	if r.Status != analyze.StatusComplete {
		fmt.Print(i18n.T("\n  有条目不完整（partial/unavailable）：可能是超时，或缺少完全磁盘访问"))
		if sysenv.Probe().FullDiskAccess == sysenv.AccessDenied {
			fmt.Print(i18n.T("（当前进程确实没有完全磁盘访问）"))
		}
		fmt.Println(i18n.T("。"))
	}
}

func runDoctor() error {
	info := sysenv.Probe()
	fmt.Printf(i18n.T("macOS %s / %s\nHOME %s\n完全磁盘访问：%s\n"), info.OSVersion, info.Arch, info.Home, info.FullDiskAccess)
	bin, err := binloc.Locate(binloc.AnalyzeName, binloc.AnalyzeEnv)
	if err != nil {
		return err
	}
	fmt.Println(i18n.T("analyze 二进制："), bin)
	sbin, err := binloc.Locate(binloc.StatusName, binloc.StatusEnv)
	if err != nil {
		return err
	}
	fmt.Println(i18n.T("status 二进制："), sbin)
	return nil
}

// runStatus 用 status.Source 打 frames 帧就取消，验证流式采集、解析和取消。
func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	frames := fs.Int("frames", 3, i18n.T("收到多少帧后退出"))
	interval := fs.Duration("interval", time.Second, i18n.T("采集间隔"))
	asJSON := fs.Bool("json", false, i18n.T("每帧输出 Vole 的 Snapshot JSON（一行一帧）"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	bin, err := binloc.Locate(binloc.StatusName, binloc.StatusEnv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	start, n := time.Now(), 0
	enc := json.NewEncoder(os.Stdout)
	err = (&statusmole.Source{Binary: bin}).Watch(ctx, *interval, func(s status.Snapshot) {
		n++
		if *asJSON {
			_ = enc.Encode(s)
		} else {
			printSnapshot(n, time.Since(start), s)
		}
		if n >= *frames {
			cancel()
		}
	})
	if errors.Is(err, context.Canceled) && n >= *frames {
		return nil
	}
	return err
}

func printSnapshot(n int, since time.Duration, s status.Snapshot) {
	fmt.Printf(i18n.T("── 第 %d 帧（+%s）%s %s %s\n"), n, since.Round(10*time.Millisecond), s.Model, s.Chip, s.OS)
	fmt.Printf(i18n.T("  CPU   %5.1f%%  %d 核（P%d/E%d） 负载 %.2f %.2f %.2f\n"), s.CPU.Usage, s.CPU.Cores, s.CPU.PCores, s.CPU.ECores, s.CPU.Load1, s.CPU.Load5, s.CPU.Load15)
	fmt.Printf(i18n.T("  内存  %5.1f%%  %s / %s  swap %s\n"), s.Memory.UsedPercent, humanSize(int64(s.Memory.Used)), humanSize(int64(s.Memory.Total)), humanSize(int64(s.Memory.SwapUsed)))
	for _, d := range s.Disks {
		fmt.Printf(i18n.T("  磁盘  %5.1f%%  %s / %s  %s\n"), d.UsedPercent, humanSize(int64(d.Used)), humanSize(int64(d.Total)), d.Mount)
	}
	fmt.Printf(i18n.T("  磁盘IO 读 %s/s 写 %s/s\n"), humanSize(int64(s.DiskIO.In)), humanSize(int64(s.DiskIO.Out)))
	for _, i := range s.Net {
		if i.IP != "" {
			fmt.Printf(i18n.T("  网络  %s %s  ↓%s/s ↑%s/s\n"), i.Name, i.IP, humanSize(int64(i.Rate.In)), humanSize(int64(i.Rate.Out)))
		}
	}
	for _, b := range s.Batteries {
		fmt.Printf(i18n.T("  电池  %.0f%% %s 剩余 %s 健康 %s 循环 %d 容量 %d%%\n"), b.Percent, b.Status, b.TimeLeft, b.Health, b.Cycles, b.Capacity)
	}
	for _, g := range s.GPUs {
		usage := i18n.T("未知（需 root）")
		if g.Usage >= 0 {
			usage = fmt.Sprintf("%.1f%%", g.Usage)
		}
		fmt.Printf(i18n.T("  GPU   %s %d 核 %s\n"), g.Name, g.Cores, usage)
	}
	if len(s.TopProcs) > 0 {
		p := s.TopProcs[0]
		fmt.Printf(i18n.T("  进程  最高 %s（%d）%.1f%%  共 %d 个进程\n"), p.Name, p.PID, p.CPU, s.Procs)
	}
	if s.Health >= 0 {
		fmt.Printf(i18n.T("  健康  %d  %s\n"), s.Health, s.HealthNote)
	}
}

func kind(e analyze.Entry) string {
	if e.IsDir {
		return i18n.T("目录")
	}
	return i18n.T("文件")
}

func flags(e analyze.Entry) string {
	var tags []string
	if e.Insight {
		tags = append(tags, "insight")
	}
	if e.Cleanable {
		tags = append(tags, i18n.T("可清理"))
	}
	if !e.LastAccess.IsZero() {
		tags = append(tags, i18n.T("访问于 ")+e.LastAccess.Local().Format("2006-01-02"))
	}
	if len(tags) == 0 {
		return ""
	}
	return "  [" + strings.Join(tags, ", ") + "]"
}

func sizeText(size int64, s analyze.Status) string {
	if s == analyze.StatusUnavailable {
		return "—"
	}
	if s == analyze.StatusPartial {
		return "≥" + humanSize(size)
	}
	return humanSize(size)
}

// humanSize 按 1024 进制格式化字节数。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	v, i := float64(n), 0
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	for v /= unit; v >= unit && i < len(units)-1; i++ {
		v /= unit
	}
	return fmt.Sprintf("%.1f%s", v, units[i])
}
