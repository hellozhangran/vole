// SPDX-License-Identifier: GPL-3.0-only

// Package i18n 是界面文字的翻译层：源码里照旧写简体中文原文，T 按当前语言查译文表，查不到就用原文。
// 语言在启动时定一次：环境变量 VOLE_LANG（zh / ko）优先，其次是系统首选语言（AppleLanguages）里
// 第一个支持的语言，都没有就是简体中文。测试二进制里固定为简体中文，原有断言不受系统语言影响。
//
// 加一种语言：在 parse 里认出它的语言代码，新建一个像 ko.go 那样的译文表，T 里按语言查表。
package i18n

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Lang 是界面语言。
type Lang string

const (
	ZH Lang = "zh" // 简体中文（原文，默认）
	KO Lang = "ko" // 韩语
)

var current = detect()

// Current 是当前界面语言。
func Current() Lang { return current }

// T 把简体中文原文换成当前语言；没有译文时返回原文。
func T(s string) string {
	if current == KO {
		if v, ok := ko[s]; ok {
			return v
		}
	}
	return s
}

// TC 和 T 一样，但先按「context\x04原文」查：同一句原文在不同地方要译得不一样时用。中文照旧返回原文。
func TC(context, s string) string {
	if current == KO {
		if v, ok := ko[context+"\x04"+s]; ok {
			return v
		}
	}
	return T(s)
}

// Tf 是 fmt.Sprintf(T(format), args...)。
func Tf(format string, args ...any) string { return fmt.Sprintf(T(format), args...) }

// AppleLanguage 是读 app 本地化显示名时优先用的 .lproj 语言。
func AppleLanguage() string {
	if current == KO {
		return "ko"
	}
	return "zh-Hans"
}

func detect() Lang {
	if l, ok := parse(os.Getenv("VOLE_LANG")); ok {
		return l
	}
	if testing.Testing() {
		return ZH
	}
	for _, l := range appleLanguages() {
		if v, ok := parse(l); ok {
			return v
		}
	}
	return ZH
}

// parse 认语言代码：ko、ko-KR 是韩语，zh、zh-Hans-CN 是简体中文；其他语言不认。
func parse(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "ko" || strings.HasPrefix(s, "ko-") || strings.HasPrefix(s, "ko_"):
		return KO, true
	case s == "zh" || strings.HasPrefix(s, "zh-") || strings.HasPrefix(s, "zh_"):
		return ZH, true
	}
	return "", false
}

// appleLanguages 读 defaults read -g AppleLanguages，如 ["ko-KR", "en-US"]。读不到就是空。
func appleLanguages() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return nil
	}
	var langs []string
	for _, line := range strings.Split(string(out), "\n") {
		if l := strings.Trim(strings.TrimSpace(line), `",()`); l != "" {
			langs = append(langs, l)
		}
	}
	return langs
}
