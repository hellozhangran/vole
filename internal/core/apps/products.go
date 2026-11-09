// SPDX-License-Identifier: GPL-3.0-only
// 按产品写死的关联目录，移植自 tw93/Mole lib/core/app_protection.sh find_app_files 的第二部分（:1886-2110，commit 6d1af75，GPL-3.0）：
// 这些 app 的数据目录名和 app 名、bundle id 都对不上，只能逐个写明。同 bundle id 还有别的副本装着时整段不做（Mole :1880）。

package apps

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var versionTail = regexp.MustCompile(`^[0-9]{4}(\.[0-9]+)+$`)

// versionedProduct：JetBrains、Android Studio 的目录是「产品名 + 年份版本」，只认这种，不认同前缀的别的产品（Mole :797）。
func versionedProduct(leaf, product string) bool {
	if leaf == product {
		return true
	}
	rest, ok := strings.CutPrefix(leaf, product)
	return ok && versionTail.MatchString(rest)
}

func (f *finder) products() {
	h, name, bid := f.env.Home, f.name, f.app.BundleID
	lname, lbid := strings.ToLower(name), strings.ToLower(bid)
	dirs := func(ps ...string) {
		for _, p := range ps {
			f.add(p, KindData, "")
		}
	}
	glob := func(pattern string, keep func(string) bool) {
		ms, _ := filepath.Glob(pattern)
		for _, m := range ms {
			if info, err := os.Lstat(m); err == nil && info.IsDir() && (keep == nil || keep(filepath.Base(m))) {
				f.add(m, KindData, "")
			}
		}
	}
	lib := f.lib
	// 1. DevEco Studio：只清它自己的缓存和日志（Huawei 根目录是别的华为产品共用的）
	if strings.Contains(lname, "deveco") || regexp.MustCompile(`huawei.*deveco`).MatchString(lbid) {
		glob(lib("Caches", "Huawei", "DevEcoStudio*"), nil)
		glob(lib("Logs", "Huawei", "DevEcoStudio*"), nil)
	}
	// 2. Android Studio：~/.android 下只清缓存（debug.keystore、adbkey、模拟器镜像不碰）
	if regexp.MustCompile(`android.*studio`).MatchString(lname) || regexp.MustCompile(`google.*android.*studio|jetbrains.*android`).MatchString(lbid) {
		dirs(filepath.Join(h, ".android", "cache"), filepath.Join(h, ".android", "build-cache"), filepath.Join(h, ".android", "breakpad"))
		glob(lib("Application Support", "Google", "AndroidStudio*"), func(n string) bool { return versionedProduct(n, "AndroidStudio") })
	}
	// 3. Xcode：只清可再生的编译和设备缓存（Archives、证书描述文件、模拟器设备不碰）
	if strings.Contains(lname, "xcode") || regexp.MustCompile(`apple.*xcode`).MatchString(lbid) {
		dev := lib("Developer")
		dirs(filepath.Join(dev, "Xcode", "DerivedData"), filepath.Join(dev, "Xcode", "iOS DeviceSupport"), filepath.Join(dev, "Xcode", "macOS DeviceSupport"),
			filepath.Join(dev, "Xcode", "watchOS DeviceSupport"), filepath.Join(dev, "Xcode", "tvOS DeviceSupport"), filepath.Join(dev, "Xcode", "xrOS DeviceSupport"),
			filepath.Join(dev, "Xcode", "visionOS DeviceSupport"), filepath.Join(dev, "CoreSimulator", "Caches"), filepath.Join(h, ".Xcode"))
	}
	// 4. JetBrains：JetBrains 目录下「产品名 + 版本」的设置、缓存、日志
	if strings.Contains(lbid, "jetbrains") || regexp.MustCompile(`IntelliJ|PyCharm|WebStorm|GoLand|RubyMine|PhpStorm|CLion|DataGrip|Rider`).MatchString(name) {
		for _, base := range []string{lib("Application Support", "JetBrains"), lib("Caches", "JetBrains"), lib("Logs", "JetBrains")} {
			glob(filepath.Join(base, name+"*"), func(n string) bool { return versionedProduct(n, name) })
		}
	}
	// 5. Godot
	if strings.Contains(lname, "godot") {
		dirs(lib("Application Support", "Godot"))
	}
	// 6. VS Code：数据目录叫 Code / Code - Insiders（Mole #850）
	if regexp.MustCompile(`microsoft.*vscode`).MatchString(lbid) {
		dirs(lib("Caches", "com.microsoft.VSCode.ShipIt"), lib("Caches", "com.microsoft.VSCodeInsiders.ShipIt"))
		if strings.Contains(lbid, "insiders") {
			dirs(filepath.Join(h, ".vscode-insiders"), lib("Application Support", "Code - Insiders"), lib("Caches", "com.microsoft.VSCodeInsiders"))
		} else {
			dirs(filepath.Join(h, ".vscode"), lib("Application Support", "Code"), lib("Caches", "com.microsoft.VSCode"))
		}
	}
	// Docker：~/.docker 里有登录凭据和 context，只清可再生的 buildx、scan
	if strings.Contains(name, "Docker") {
		dirs(filepath.Join(h, ".docker", "buildx"), filepath.Join(h, ".docker", "scan"))
	}
	// Maestro Studio
	if bid == "com.maestro.studio" || regexp.MustCompile(`maestro\s*studio`).MatchString(lname) {
		dirs(filepath.Join(h, ".mobiledev"))
	}
	// Anki：只清启动器的支持文件（Anki2 里是卡组和备份，不碰）
	if bid == "net.ankiweb.anki" || name == "Anki" {
		dirs(lib("Application Support", "AnkiProgramFiles"))
	}
	// Claude 桌面版用第三方服务时数据目录带 -3p（~/.claude 是 Claude Code 命令行的，不碰）
	if bid == "com.anthropic.claudefordesktop" {
		dirs(lib("Application Support", "Claude-3p"), lib("Logs", "Claude-3p"))
	}
	// 7. Raycast：名字含 raycast 的目录（Raycast v2 是另一个 app，raycast-x 不碰）
	if bid == "com.raycast.macos" {
		ray := func(n string) bool {
			l := strings.ToLower(n)
			return strings.Contains(l, "raycast") && !strings.Contains(l, "raycast-x")
		}
		for _, d := range []string{lib("Application Support"), lib("Application Scripts"), lib("Containers")} {
			glob(filepath.Join(d, "*"), ray)
		}
		glob(lib("Caches", "*"), ray)
		glob(lib("Caches", "*", "*"), ray)
		glob(lib("Application Support", "Code", "User", "globalStorage", "*"), ray)
	}
}
