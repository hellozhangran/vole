package mole

import (
	"encoding/json"
	"fmt"
	"time"

	"vole/internal/core/analyze"
)

// 以下结构对应 analyze --json 的输出格式（字段名见 research/01），仅用于反序列化。
type rawOutput struct {
	ScanStatus string     `json:"scan_status"`
	Path       string     `json:"path"`
	Overview   bool       `json:"overview"`
	Entries    []rawEntry `json:"entries"`
	LargeFiles []rawFile  `json:"large_files"`
	TotalSize  int64      `json:"total_size"`
	TotalFiles int64      `json:"total_files"`
}

type rawEntry struct {
	ScanStatus string `json:"scan_status"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	IsDir      bool   `json:"is_dir"`
	Insight    bool   `json:"insight"`
	Cleanable  bool   `json:"cleanable"`
	LastAccess string `json:"last_access"`
}

type rawFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// parse 把 analyze 的 JSON 转成 Vole 的 Report。
func parse(data []byte) (analyze.Report, error) {
	var raw rawOutput
	if err := json.Unmarshal(data, &raw); err != nil {
		return analyze.Report{}, fmt.Errorf("解析 analyze 输出失败：%w", err)
	}
	if raw.Path == "" {
		return analyze.Report{}, fmt.Errorf("analyze 输出缺少 path 字段")
	}

	r := analyze.Report{
		Path:     raw.Path,
		Overview: raw.Overview,
		Status:   status(raw.ScanStatus),
		Entries:  make([]analyze.Entry, 0, len(raw.Entries)),
	}
	// overview 的 total_size 是各行相加，insight 行是 Home/Library 的子目录，重复计数，丢弃
	if !raw.Overview {
		r.TotalSize, r.TotalFiles = raw.TotalSize, raw.TotalFiles
	}
	for _, e := range raw.Entries {
		entry := analyze.Entry{
			Name: e.Name, Path: e.Path, Size: e.Size, IsDir: e.IsDir,
			Insight: e.Insight, Cleanable: e.Cleanable, Status: status(e.ScanStatus),
		}
		if e.LastAccess != "" {
			// 格式异常时当作未知，不让单个字段拖垮整次结果
			if t, err := time.Parse(time.RFC3339, e.LastAccess); err == nil {
				entry.LastAccess = t
			}
		}
		r.Entries = append(r.Entries, entry)
	}
	for _, f := range raw.LargeFiles {
		r.LargeFiles = append(r.LargeFiles, analyze.LargeFile(f))
	}
	return r, nil
}

// status 未知取值按 unavailable 处理，宁可保守。
func status(s string) analyze.Status {
	switch analyze.Status(s) {
	case analyze.StatusComplete, analyze.StatusPartial:
		return analyze.Status(s)
	}
	return analyze.StatusUnavailable
}
