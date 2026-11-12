package statusui

import (
	"fmt"
	"strings"
	"time"

	core "vole/internal/core/status"
	"vole/internal/i18n"
	"vole/internal/kit"
)

// 健康度文案：status 输出 "Good: High CPU, Disk Almost Full"，这里只把它的固定词表翻成中文，认不出的原样显示。
var healthLevels = map[string]string{"Excellent": i18n.T("优秀"), "Good": i18n.T("良好"), "Fair": i18n.T("一般"), "Needs Attention": i18n.T("需要关注")}
var healthIssues = map[string]string{
	"High CPU": i18n.T("CPU 负载偏高"), "High Memory": i18n.T("内存占用偏高"), "Memory Pressure": i18n.T("内存压力大"),
	"Critical Memory": i18n.T("内存严重不足"), "Disk Almost Full": i18n.T("磁盘快满了"), "Disk SMART Failing": i18n.T("磁盘 SMART 异常"),
	"Overheating": i18n.T("温度过高"), "Heavy Disk IO": i18n.T("磁盘读写繁重"), "Battery Service Soon": i18n.T("电池需要维修"),
	"Restart Recommended": i18n.T("建议重启"),
}

// HealthArt 按健康等级选插画：优先用 status 给的等级词（Mole cmd/status/metrics_health.go:197 的
// Excellent / Good / Fair / Needs Attention）；没有等级词时按同一处的分数阈值（85 / 65 / 45）判断。
func HealthArt(msg string, score int) kit.ArtName {
	head, _, _ := strings.Cut(msg, ":")
	switch strings.TrimSpace(head) {
	case "Excellent", "Good":
		return kit.ArtHealthGood
	case "Fair":
		return kit.ArtHealthFair
	case "Needs Attention":
		return kit.ArtHealthPoor
	}
	switch {
	case score >= 65: // Good 及以上
		return kit.ArtHealthGood
	case score >= 45: // Fair
		return kit.ArtHealthFair
	case score >= 0:
		return kit.ArtHealthPoor
	}
	return kit.ArtHealthGood // 还没有分数（首帧前）
}

// HealthText 把健康说明拆成中文等级和问题列表；没有问题时 issues 是「没有发现问题」。
func HealthText(msg string) (level, issues string) {
	head, tail, _ := strings.Cut(msg, ":")
	level = strings.TrimSpace(head)
	if zh, ok := healthLevels[level]; ok {
		level = zh
	}
	var out []string
	for _, it := range strings.Split(tail, ",") {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if zh, ok := healthIssues[it]; ok {
			it = zh
		}
		out = append(out, it)
	}
	if len(out) == 0 {
		return level, i18n.T("没有发现问题")
	}
	return level, strings.Join(out, i18n.T("，"))
}

// TempTag 温度标签；拿不到就不显示，不写 0°C。
func TempTag(v float64) string {
	if v <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f°C", v)
}

// PressureTag 内存压力标签。
func PressureTag(p string) string {
	switch p {
	case "normal":
		return i18n.T("压力 正常")
	case "warn":
		return i18n.T("压力 偏高")
	case "critical":
		return i18n.T("压力 严重")
	}
	return ""
}

// BatteryState 充电状态或剩余时间。
func BatteryState(b *core.Battery) string {
	switch b.Status {
	case "charging":
		return i18n.T("充电中")
	case "charged":
		return i18n.T("已充满")
	}
	if b.TimeLeft != "" {
		return i18n.TC("battery", "可用 ") + b.TimeLeft
	}
	return ""
}

// BatteryHealth 电池状况。
func BatteryHealth(h string) string {
	if h == "" {
		return ""
	}
	return i18n.T("状况 ") + h
}

// UptimeText 开机时长，如「29d 12h」「3h 5m」。
func UptimeText(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
}

// RateText 按 1000 进制格式化速率（字节/秒）。
func RateText(bps float64) string {
	return kit.HumanSize(int64(bps)) + "/s"
}
