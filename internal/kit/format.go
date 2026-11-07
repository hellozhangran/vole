package kit

import (
	"fmt"
	"time"
)

// HumanSize 按 1000 进制格式化字节数，与 Finder 一致，如 "22.99 GB"。
func HumanSize(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", max(n, 0))
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(n) / 1000
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f %s", v, units[i])
	case v >= 10:
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

// Elapsed 把耗时格式化成 "8s" / "1:05" / "1:02:03"。
func Elapsed(d time.Duration) string {
	s := int(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%d:%02d", s/60, s%60)
	}
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}
