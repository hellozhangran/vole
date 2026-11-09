// SPDX-License-Identifier: GPL-3.0-only

package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// readPlist 读 plist（XML、二进制都行，.strings 也行）：用系统自带的 plutil 转成 JSON。
func readPlist(ctx context.Context, env *Env, p string) (map[string]any, error) {
	if _, err := os.Stat(p); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := env.Run(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", p)
	if err != nil {
		return nil, fmt.Errorf("读不了 %s：%w", filepath.Base(p), err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func boolOf(m map[string]any, k string) bool {
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return v == "1" || strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
	case float64:
		return v != 0
	}
	return false
}

// localizedName 按系统首选语言取显示名（Mole bin/uninstall.sh:133-207）：每种语言依次试几种 .lproj 写法，
// 命中第一个存在的目录就停，不再试别的语言；读其中 InfoPlist.strings 的 CFBundleDisplayName，没有再读 CFBundleName。
// 语言和 CFBundleDevelopmentRegion 相同时也认 Base.lproj。
func localizedName(ctx context.Context, env *Env, app string, info map[string]any) string {
	res := filepath.Join(app, "Contents", "Resources")
	dev := str(info, "CFBundleDevelopmentRegion")
	for _, lang := range env.Languages {
		var dirs []string
		parts := strings.Split(lang, "-")
		dirs = append(dirs, lang, strings.ReplaceAll(lang, "-", "_"))
		if len(parts) >= 2 {
			dirs = append(dirs, parts[0]+"_"+parts[len(parts)-1], parts[0]+"-"+parts[1])
		}
		if strings.EqualFold(lang, "zh-Hans") || strings.HasPrefix(strings.ToLower(lang), "zh-hans-") {
			dirs = append(dirs, "zh_CN", "zh-CN") // 老 app 的简体中文目录叫 zh_CN
		}
		dirs = append(dirs, parts[0])
		if dev != "" && (strings.EqualFold(dev, lang) || strings.EqualFold(dev, parts[0])) {
			dirs = append(dirs, "Base")
		}
		for _, d := range dirs {
			lproj := filepath.Join(res, d+".lproj")
			if info, err := os.Stat(lproj); err != nil || !info.IsDir() {
				continue
			}
			if s, err := readPlist(ctx, env, filepath.Join(lproj, "InfoPlist.strings")); err == nil {
				if n := str(s, "CFBundleDisplayName"); n != "" {
					return n
				}
				if n := str(s, "CFBundleName"); n != "" {
					return n
				}
			}
			return "" // 命中第一个存在的 .lproj 就停（同 Mole），由调用方退回 Info.plist
		}
	}
	return ""
}
