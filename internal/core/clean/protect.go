// SPDX-License-Identifier: GPL-3.0-only
// 移植自 tw93/Mole（commit 6d1af75，GPL-3.0）：
//   - lib/core/file_ops.sh _mole_is_critical_deletion_path（:1044）→ criticalPath
//   - lib/core/app_protection.sh should_protect_data（:199）→ protectedName
//   - lib/core/app_protection.sh should_protect_path（:406）中和 MVP 清理范围相关的路径规则 → protectedPath
//   - lib/core/base.sh DEFAULT_WHITELIST_PATTERNS（:172）、SAFETY_WHITELIST_PATTERNS（:197）→ defaultWhitelist、safetyWhitelist
// 改动：bash case/glob 改成 Go 实现；只移植 MVP 五类（家目录下的缓存、日志、废纸篓）用得到的部分，
// 系统路径、卸载、容器相关的规则等扩到对应类别时再移植。日志目录加上 Vole 自己的。

package clean

import (
	"path"
	"regexp"
	"strings"
)

// criticalPath 报告绝对路径是否是绝不能删的系统关键路径（含家目录本身）。
func criticalPath(p, home string) bool {
	if strings.HasPrefix(p, "/usr/local/") || strings.HasPrefix(p, "/opt/homebrew/") {
		return false
	}
	switch p {
	case "/", "/bin", "/dev", "/sbin", "/usr", "/System", "/Library", "/Library/Apple", "/Library/Application Support",
		"/Library/Extensions", "/Library/Keychains", "/Applications", "/Volumes", "/opt", "/opt/homebrew",
		"/Users", "/Users/Shared", "/Users/Guest", "/private", "/private/tmp", "/etc", "/private/etc",
		"/var", "/var/db", "/var/audit", "/var/root", "/private/var", "/private/var/tmp", "/private/var/folders",
		"/private/var/db", "/private/var/audit", "/private/var/root", "/Network", "/cores", "/home", "/net", "/tmp":
		return true
	}
	for _, root := range []string{"/bin/", "/dev/", "/sbin/", "/usr/", "/System/", "/Library/Apple/", "/Library/Extensions/",
		"/Library/Keychains/", "/Applications/Finder.app", "/Applications/Safari.app", "/Users/Guest/", "/etc/",
		"/private/etc/", "/var/db/", "/var/audit/", "/private/var/db/", "/private/var/audit/"} {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	// /Users/<名字> 本身（任何用户的家目录）
	if strings.HasPrefix(p, "/Users/") && !strings.Contains(strings.TrimPrefix(p, "/Users/"), "/") {
		return true
	}
	return home != "" && p == home
}

// protectedName 报告一个文件或目录名（通常是 bundle id）对应的数据是否受保护，对应 Mole 的 should_protect_data。
func protectedName(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(name, "com.apple."), name == "loginwindow", name == "dock", name == "systempreferences",
		name == "finder", name == "safari", strings.HasPrefix(name, "org.cups."):
		return true
	case hasAnyPrefix(name, "backgroundtaskmanagement", "keychain", "security", "bluetooth", "wifi", "network",
		"notification", "accessibility", "universalaccess", "HIToolbox", "textinput", "TextInput",
		"keyboard", "Keyboard", "inputsource", "InputSource", "keylayout", "KeyLayout", "org.pqrs.Karabiner"),
		name == "tcc", name == "GlobalPreferences", name == ".GlobalPreferences",
		strings.Contains(name, "inputmethod"), strings.Contains(name, "InputMethod"), strings.HasSuffix(name, "IME"):
		return true
	case hasAnyPrefix(name, "com.1password.", "com.agilebits.", "com.lastpass.", "com.dashlane.", "com.bitwarden.",
		"com.jetbrains.", "JetBrains", "com.microsoft.", "com.visualstudio.", "com.sublimetext.", "com.sublimehq.",
		"com.nssurge.", "com.v2ray.", "com.clash.", "ClashX", "Surge", "Shadowrocket", "Quantumult",
		"com.docker.", "com.getpostman.", "com.insomnia."):
		return true
	case name == "Cursor", name == "Claude", name == "ChatGPT", name == "com.openai.codex", name == "Codex",
		name == "codex-runtimes", name == "Ollama":
		return true
	case strings.Contains(lower, "clash"):
		return true
	}
	for _, g := range dataProtectedBundles {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	for _, g := range systemCriticalBundles {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// protectedPath 报告路径是否命中受保护的路径规则（系统设置、账户与钥匙串、iCloud、Mole/Vole 自己的日志等）。
func protectedPath(p, home string) bool {
	for _, s := range []string{"SystemSettings", "Systemsettings", "systemSettings", "systemsettings",
		"SystemPreferences", "systempreferences", "ControlCenter", "controlcenter", "Controlcenter",
		"com.apple.Settings", "com.apple.settings", "com.apple.Notes", "com.apple.notes",
		"com.apple.finder", "com.apple.dock", "com.apple.coreaudio", "com.apple.audio.", "coreaudiod",
		"/Mobile Documents"} {
		if strings.Contains(p, s) {
			return true
		}
	}
	lib := home + "/Library/"
	for _, dir := range []string{"Accounts", "Keychains", "Mail", "Calendars", "Contacts",
		"Logs/mole", "Logs/Vole", "Logs/com.openai.codex", "Application Support/Codex",
		"Caches/ms-playwright", "Caches/app.cotypist.Cotypist", "Caches/com.displaylink.DisplayLinkUserAgent",
		"Caches/com.lasersoft-imaging.SilverFast9", "Caches/com.lasersoft-imaging.SilverFast-9-Installer",
		"Caches/com.apple.containermanagerd", "Caches/com.apple.homed", "Caches/com.apple.ap.adprivacyd",
		"Caches/FamilyCircle", "Caches/com.apple.HomeKit", "Caches/GIPPseudonymousID", "Caches/CCTClearcutLogger",
		"Application Support/com.apple.idleassetsd", "Application Support/com.apple.wallpaper"} {
		root := lib + dir
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	if strings.HasPrefix(p, lib+"Caches/Adobe ") || strings.Contains(strings.TrimPrefix(p, lib+"Caches/"), " Adobe") {
		return true
	}
	// 编译好的端侧模型缓存（Mole holds_compiled_model_cache）
	return strings.HasSuffix(p, "/com.apple.e5rt.e5bundlecache") || strings.Contains(p, "/com.apple.e5rt.e5bundlecache/")
}

// 默认白名单（用户以后可以改，MVP 只内置）和强制安全白名单（删了会坏系统功能，永远生效）。
// 和 Mole 一样，模式里的 * 可以跨过 /。
var (
	defaultWhitelist = []string{
		"~/Library/Caches/ms-playwright*", "~/.gradle/caches/*", "~/.gradle/daemon/*", "~/.ollama/models/*",
		"~/Library/Caches/com.nssurge.surge-mac/*", "~/Library/Application Support/com.nssurge.surge-mac/*",
		"~/Library/Caches/org.R-project.R/R/renv/*", "~/Library/Caches/JetBrains*", "~/Library/Caches/com.jetbrains.toolbox*",
		"~/Library/Caches/tealdeer/tldr-pages", "~/Library/Application Support/JetBrains*",
		"~/Library/Caches/com.apple.finder", "~/Library/Mobile Documents*",
	}
	safetyWhitelist = []string{
		"~/Library/Caches/com.apple.FontRegistry*", "~/Library/Caches/com.apple.spotlight*",
		"~/Library/Caches/com.apple.Spotlight*", "~/Library/Caches/CloudKit*", "~/Library/Caches/kitty",
		"~/Library/Caches/pypoetry/virtualenvs*", "~/Library/Caches/org.R-project.R/R/renv/*",
		"~/Library/Caches/pnpm/dlx*", "~/Library/Caches/llama.cpp",
	}
)

// whitelisted 报告路径是否在白名单里（路径本身、或它在某个白名单项下面）。
func whitelisted(p string, env *Env) bool {
	for _, list := range [][]string{safetyWhitelist, defaultWhitelist} {
		for _, pat := range list {
			re := globRegexp(env.expand(pat))
			if re.MatchString(p) {
				return true
			}
			// 白名单项下面的路径也算（如白名单是目录，p 是它的子项）
			if !strings.HasSuffix(pat, "*") && strings.HasPrefix(p, env.expand(pat)+"/") {
				return true
			}
		}
	}
	return false
}

// globRegexp 把 bash 风格的 glob 转成正则：* 匹配任意字符（含 /），? 匹配一个字符。
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
