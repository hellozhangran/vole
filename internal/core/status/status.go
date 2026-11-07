// Package status 定义 Vole 自己的系统状态模型和数据源接口。
// UI 只认这里的类型，不认任何具体采集器（Mole status 的 NDJSON 等）的格式。
package status

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Snapshot 是一帧系统状态。慢指标（电池、GPU、温度、进程、硬件）在采集器拿到之前为零值。
type Snapshot struct {
	At         time.Time
	Host       string
	OS         string // 如 "macOS 14.6.1"
	Model      string // 机型，如 "MacBook Pro"
	Chip       string // 如 "Apple M1 Pro"
	Uptime     time.Duration
	Procs      int
	Health     int    // 0–100；-1 = 未知
	HealthNote string // 健康分的简短说明
	CPU        CPU
	GPUs       []GPU
	Memory     Memory
	Disks      []Disk // 已过滤掉模拟器等非用户卷
	DiskIO     Rate
	Net        []Iface
	NetHistory NetHistory
	Batteries  []Battery
	Thermal    Thermal
	TopProcs   []Proc
	Alerts     []int // 采集器判定为持续高 CPU 的进程 PID（Mole 规则：超过阈值持续一段时间）
	Proxy      Proxy
	Bluetooth  []BluetoothDevice
	TrashBytes uint64
}

// CPU 占用。百分比 0–100（单核）。
type CPU struct {
	Usage                 float64
	PerCore               []float64
	Load1, Load5, Load15  float64
	Cores, PCores, ECores int
}

// GPU 信息。Usage = -1 表示拿不到（需要 root 才能读）。
type GPU struct {
	Name  string
	Usage float64
	Cores int
}

// Memory 单位字节。
type Memory struct {
	Used, Total, Available, Cached uint64
	SwapUsed, SwapTotal            uint64
	UsedPercent                    float64
	Pressure                       string // normal / warn / critical；空 = 未知
}

// Disk 一个卷，单位字节。
type Disk struct {
	Mount, Device string
	Used, Total   uint64
	Purgeable     uint64
	UsedPercent   float64
	External      bool
}

// Rate 读写或收发速率，单位字节/秒。
type Rate struct{ In, Out float64 }

// Iface 一个网络接口。Rate.In = 下行，Rate.Out = 上行。
type Iface struct {
	Name, IP string
	Rate     Rate
}

// NetHistory 全局网络速率历史（字节/秒），最旧在前。
type NetHistory struct{ In, Out []float64 }

// Battery 电池。
type Battery struct {
	Percent  float64
	Status   string // charging / discharging / charged …
	TimeLeft string // 如 "1:24"
	Health   string
	Cycles   int
	Capacity int // 最大容量 %，0 = 未知
}

// Thermal 温度（℃）与功耗（W），0 = 拿不到。
type Thermal struct {
	CPUTemp, GPUTemp, BatteryTemp float64
	FanRPM                        int
	SystemPower, BatteryPower     float64
	AdapterPower                  float64
}

// Proc 一个高占用进程。CPU 可超过 100（多核）。
type Proc struct {
	PID      int
	Name     string
	CPU      float64
	MemBytes uint64
}

// Proxy 系统代理。
type Proxy struct {
	Enabled    bool
	Type, Host string
}

// BluetoothDevice 蓝牙设备。
type BluetoothDevice struct {
	Name      string
	Connected bool
	Battery   string
}

// Source 是系统状态数据源。
type Source interface {
	// Watch 每 interval 采一帧并调用 emit（在采集 goroutine 上）。
	// 阻塞到 ctx 取消（返回 ctx.Err()）或采集器意外退出（返回 *ExitError）。重启 = 再调一次 Watch。
	Watch(ctx context.Context, interval time.Duration, emit func(Snapshot)) error
}

// ErrUnavailable 表示采集器本身不可用：二进制缺失、无法执行、格式损坏或被信号杀掉。
var ErrUnavailable = errors.New("状态组件不可用")

// ExitError 表示采集器进程自己退出了（watch 模式本不该退出）。
type ExitError struct {
	ExitCode int    // -1 = 未能启动或被信号杀掉
	Stderr   string // 已截断
	Frames   int    // 退出前收到了几帧
	Err      error
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("状态采集意外退出（已收到 %d 帧", e.Frames)
	if e.ExitCode >= 0 {
		msg += fmt.Sprintf("，退出码 %d", e.ExitCode)
	}
	msg += "）"
	if e.Stderr != "" {
		msg += "：" + e.Stderr
	} else if e.Err != nil {
		msg += "：" + e.Err.Error()
	}
	return msg
}

func (e *ExitError) Unwrap() error { return e.Err }
