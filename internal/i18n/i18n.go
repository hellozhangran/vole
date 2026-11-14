// SPDX-License-Identifier: GPL-3.0-only

// Package i18n 是界面文字的翻译层：源码里照旧写简体中文原文，T 按当前语言查译文表，查不到就用原文。
// 语言在启动时定一次（改了要重启 Vole 才生效），优先级：
//  1. 环境变量 VOLE_LANG（zh / ko / en）；
//  2. 设置里保存的语言（core/settings，~/Library/Application Support/Vole/settings.json）；
//  3. 系统首选语言（AppleLanguages）里第一个支持的语言；
//  4. 都不支持时用英文（日语、法语等系统看到英文）。
//
// 测试二进制里没设 VOLE_LANG 时固定为简体中文，原有断言不受系统语言和设置影响。
//
// 加一种语言：在 parse 里认出它的语言代码，新建一个像 ko.go 那样的译文表，登记到 tables。
package i18n

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"vole/internal/core/settings"
)

// Lang 是界面语言。
type Lang string

const (
	ZH Lang = "zh" // 简体中文（原文）
	KO Lang = "ko" // 韩语
	EN Lang = "en" // 英文
)

// tables 是各语言的译文表；简体中文是原文，没有表。
var tables = map[Lang]map[string]string{KO: ko, EN: en}

var current = detect()

// Current 是当前界面语言。
func Current() Lang { return current }

// T 把简体中文原文换成当前语言；没有译文时返回原文。
func T(s string) string {
	if v, ok := tables[current][s]; ok {
		return v
	}
	return s
}

// TC 和 T 一样，但先按「context\x04原文」查：同一句原文在不同地方要译得不一样时用。中文照旧返回原文。
func TC(context, s string) string {
	if v, ok := tables[current][context+"\x04"+s]; ok {
		return v
	}
	return T(s)
}

// Tf 是 fmt.Sprintf(T(format), args...)。
func Tf(format string, args ...any) string { return fmt.Sprintf(T(format), args...) }

// AppleLanguage 是读 app 本地化显示名时优先用的 .lproj 语言。
func AppleLanguage() string {
	switch current {
	case KO:
		return "ko"
	case EN:
		return "en"
	}
	return "zh-Hans"
}

// Resolve 是设置里选 choice（空 = 跟随系统）后，下次启动会用的语言。设置页用它判断要不要提示重启。
func Resolve(choice string) Lang {
	return resolve(os.Getenv("VOLE_LANG"), choice, systemLanguages())
}

func detect() Lang {
	env := os.Getenv("VOLE_LANG")
	if l, ok := parse(env); ok {
		return l
	}
	if testing.Testing() {
		return ZH
	}
	s, _ := settings.Load(settings.DefaultPath()) // 读不到或坏了就当没设置
	return resolve(env, s.Lang, systemLanguages())
}

// resolve 按优先级定语言：VOLE_LANG > 设置 > 系统首选语言里第一个支持的 > 英文。
func resolve(env, saved string, system []string) Lang {
	if l, ok := parse(env); ok {
		return l
	}
	if l, ok := parse(saved); ok {
		return l
	}
	for _, l := range system {
		if v, ok := parse(l); ok {
			return v
		}
	}
	return EN
}

// parse 认语言代码：ko、ko-KR 是韩语，zh、zh-Hans-CN 是简体中文，en、en-US、en_GB 是英文；其他语言不认。
func parse(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, l := range []Lang{KO, ZH, EN} {
		p := string(l)
		if s == p || strings.HasPrefix(s, p+"-") || strings.HasPrefix(s, p+"_") {
			return l, true
		}
	}
	return "", false
}

// systemLanguages 是系统首选语言，读一次后缓存。
var systemLanguages = sync.OnceValue(appleLanguages)

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
