package sysenv

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// HardwarePorts 用 networksetup 读网卡对应的硬件端口名，如 en0 → "Wi-Fi"、en5 → "Ethernet Adapter (en5)"。
// 只读；本机实测约 25ms。读不到返回空表。
func HardwarePorts() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/networksetup", "-listallhardwareports").Output()
	if err != nil {
		return map[string]string{}
	}
	return parsePorts(string(out))
}

// parsePorts 解析 networksetup -listallhardwareports 的输出（Hardware Port / Device 成对出现）。
func parsePorts(out string) map[string]string {
	ports := map[string]string{}
	port := ""
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Hardware Port":
			port = strings.TrimSpace(v)
		case "Device":
			if dev := strings.TrimSpace(v); dev != "" && port != "" {
				ports[dev] = port
			}
			port = ""
		}
	}
	return ports
}
