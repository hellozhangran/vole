package mole

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vole/internal/core/status"
)

// 以下结构对应 Mole status --watch 每行 JSON 的字段（定义见 third_party/mole/cmd/status/metrics.go 的 MetricsSnapshot），只取 Vole 用得上的，仅用于反序列化。
type rawSnapshot struct {
	CollectedAt   time.Time `json:"collected_at"`
	Host          string    `json:"host"`
	UptimeSeconds uint64    `json:"uptime_seconds"`
	Procs         int       `json:"procs"`
	Hardware      struct {
		Model     string `json:"model"`
		CPUModel  string `json:"cpu_model"`
		OSVersion string `json:"os_version"`
	} `json:"hardware"`
	HealthScore    *int   `json:"health_score"`
	HealthScoreMsg string `json:"health_score_msg"`
	CPU            struct {
		Usage      float64   `json:"usage"`
		PerCore    []float64 `json:"per_core"`
		Load1      float64   `json:"load1"`
		Load5      float64   `json:"load5"`
		Load15     float64   `json:"load15"`
		LogicalCPU int       `json:"logical_cpu"`
		PCoreCount int       `json:"p_core_count"`
		ECoreCount int       `json:"e_core_count"`
	} `json:"cpu"`
	GPU []struct {
		Name      string  `json:"name"`
		Usage     float64 `json:"usage"`
		CoreCount int     `json:"core_count"`
	} `json:"gpu"`
	Memory struct {
		Used        uint64  `json:"used"`
		Total       uint64  `json:"total"`
		Available   uint64  `json:"available"`
		UsedPercent float64 `json:"used_percent"`
		SwapUsed    uint64  `json:"swap_used"`
		SwapTotal   uint64  `json:"swap_total"`
		Cached      uint64  `json:"cached"`
		Pressure    string  `json:"pressure"`
	} `json:"memory"`
	Disks []struct {
		Mount       string  `json:"mount"`
		Device      string  `json:"device"`
		Used        uint64  `json:"used"`
		Total       uint64  `json:"total"`
		UsedPercent float64 `json:"used_percent"`
		External    bool    `json:"external"`
		Purgeable   uint64  `json:"purgeable"`
	} `json:"disks"`
	TrashSize uint64 `json:"trash_size"`
	DiskIO    struct {
		ReadRate  float64 `json:"read_rate"`  // MB/s
		WriteRate float64 `json:"write_rate"` // MB/s
	} `json:"disk_io"`
	Network []struct {
		Name string  `json:"name"`
		Rx   float64 `json:"rx_rate_mbs"` // MB/s
		Tx   float64 `json:"tx_rate_mbs"` // MB/s
		IP   string  `json:"ip"`
	} `json:"network"`
	NetworkHistory struct {
		Rx []float64 `json:"rx_history"` // MB/s
		Tx []float64 `json:"tx_history"` // MB/s
	} `json:"network_history"`
	Proxy struct {
		Enabled bool   `json:"enabled"`
		Type    string `json:"type"`
		Host    string `json:"host"`
	} `json:"proxy"`
	Batteries []struct {
		Percent    float64 `json:"percent"`
		Status     string  `json:"status"`
		TimeLeft   string  `json:"time_left"`
		Health     string  `json:"health"`
		CycleCount int     `json:"cycle_count"`
		Capacity   int     `json:"capacity"`
	} `json:"batteries"`
	Thermal struct {
		CPUTemp      float64 `json:"cpu_temp"`
		GPUTemp      float64 `json:"gpu_temp"`
		BatteryTemp  float64 `json:"battery_temp"`
		FanSpeed     int     `json:"fan_speed"`
		SystemPower  float64 `json:"system_power"`
		AdapterPower float64 `json:"adapter_power"`
		BatteryPower float64 `json:"battery_power"`
	} `json:"thermal"`
	Bluetooth []struct {
		Name      string `json:"name"`
		Connected bool   `json:"connected"`
		Battery   string `json:"battery"`
	} `json:"bluetooth"`
	ProcessAlerts []struct {
		PID    int    `json:"pid"`
		Status string `json:"status"`
	} `json:"process_alerts"`
	TopProcesses []struct {
		PID         int     `json:"pid"`
		Name        string  `json:"name"`
		CPU         float64 `json:"cpu"`
		MemoryBytes uint64  `json:"memory_bytes"`
	} `json:"top_processes"`
}

// mb 是 Mole 的 MB/s（10^6 字节）。
const mb = 1e6

// parse 把一行 NDJSON 转成 Vole 的 Snapshot。
func parse(line []byte) (status.Snapshot, error) {
	var raw rawSnapshot
	if err := json.Unmarshal(line, &raw); err != nil {
		return status.Snapshot{}, fmt.Errorf("解析 status 输出失败：%w", err)
	}
	if raw.CollectedAt.IsZero() {
		return status.Snapshot{}, fmt.Errorf("status 输出缺少 collected_at")
	}

	s := status.Snapshot{
		At: raw.CollectedAt, Host: raw.Host, OS: raw.Hardware.OSVersion,
		Model: raw.Hardware.Model, Chip: raw.Hardware.CPUModel,
		Uptime: time.Duration(raw.UptimeSeconds) * time.Second, Procs: raw.Procs,
		Health: -1, HealthNote: raw.HealthScoreMsg,
		CPU: status.CPU{
			Usage: raw.CPU.Usage, PerCore: raw.CPU.PerCore,
			Load1: raw.CPU.Load1, Load5: raw.CPU.Load5, Load15: raw.CPU.Load15,
			Cores: raw.CPU.LogicalCPU, PCores: raw.CPU.PCoreCount, ECores: raw.CPU.ECoreCount,
		},
		Memory: status.Memory{
			Used: raw.Memory.Used, Total: raw.Memory.Total, Available: raw.Memory.Available, Cached: raw.Memory.Cached,
			SwapUsed: raw.Memory.SwapUsed, SwapTotal: raw.Memory.SwapTotal,
			UsedPercent: raw.Memory.UsedPercent, Pressure: raw.Memory.Pressure,
		},
		DiskIO:     status.Rate{In: raw.DiskIO.ReadRate * mb, Out: raw.DiskIO.WriteRate * mb},
		NetHistory: status.NetHistory{In: scale(raw.NetworkHistory.Rx), Out: scale(raw.NetworkHistory.Tx)},
		Proxy:      status.Proxy{Enabled: raw.Proxy.Enabled, Type: raw.Proxy.Type, Host: raw.Proxy.Host},
		Thermal: status.Thermal{
			CPUTemp: raw.Thermal.CPUTemp, GPUTemp: raw.Thermal.GPUTemp, BatteryTemp: raw.Thermal.BatteryTemp,
			FanRPM: raw.Thermal.FanSpeed, SystemPower: raw.Thermal.SystemPower,
			BatteryPower: raw.Thermal.BatteryPower, AdapterPower: raw.Thermal.AdapterPower,
		},
		TrashBytes: raw.TrashSize,
	}
	if raw.HealthScore != nil {
		s.Health = *raw.HealthScore
	}
	for _, g := range raw.GPU {
		s.GPUs = append(s.GPUs, status.GPU{Name: g.Name, Usage: g.Usage, Cores: g.CoreCount})
	}
	for _, d := range raw.Disks {
		if !userVolume(d.Mount) {
			continue
		}
		s.Disks = append(s.Disks, status.Disk{
			Mount: d.Mount, Device: d.Device, Used: d.Used, Total: d.Total,
			Purgeable: d.Purgeable, UsedPercent: d.UsedPercent, External: d.External,
		})
	}
	for _, n := range raw.Network {
		s.Net = append(s.Net, status.Iface{Name: n.Name, IP: n.IP, Rate: status.Rate{In: n.Rx * mb, Out: n.Tx * mb}})
	}
	for _, b := range raw.Batteries {
		s.Batteries = append(s.Batteries, status.Battery{
			Percent: b.Percent, Status: b.Status, TimeLeft: b.TimeLeft, Health: b.Health,
			Cycles: b.CycleCount, Capacity: b.Capacity,
		})
	}
	for _, d := range raw.Bluetooth {
		s.Bluetooth = append(s.Bluetooth, status.BluetoothDevice{Name: d.Name, Connected: d.Connected, Battery: d.Battery})
	}
	for _, p := range raw.TopProcesses {
		s.TopProcs = append(s.TopProcs, status.Proc{PID: p.PID, Name: p.Name, CPU: p.CPU, MemBytes: p.MemoryBytes})
	}
	for _, a := range raw.ProcessAlerts {
		if a.Status == "active" {
			s.Alerts = append(s.Alerts, a.PID)
		}
	}
	return s, nil
}

// userVolume 只保留系统盘和 /Volumes 下的卷，过滤模拟器运行时等挂载卷。
func userVolume(mount string) bool {
	return mount == "/" || strings.HasPrefix(mount, "/Volumes/")
}

func scale(v []float64) []float64 {
	if len(v) == 0 {
		return nil
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x * mb
	}
	return out
}
