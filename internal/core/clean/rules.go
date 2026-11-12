// SPDX-License-Identifier: GPL-3.0-only
// 规则参考自 tw93/Mole lib/clean/*.sh（commit 6d1af75，GPL-3.0），每条注明对应位置；用 Go 数据重写。
// 默认勾选的分档（设计决定）：包缓存默认勾；Go module 缓存、DeviceSupport 默认不勾；废纸篓默认不勾。

package clean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"vole/internal/i18n"
)

// 加规则：在对应类别里追加一条 Rule；加类别：在 Categories 里加一项。
const (
	CatDev     = "dev"
	CatXcode   = "xcode"
	CatCaches  = "caches"
	CatBrowser = "browser"
	CatLogs    = "logs"
	CatTrash   = "trash"
	CatSystem  = "system"
	CatAI      = "ai"
	CatComms   = "comms"
)

// xcodeProcs 在运行时不清 Xcode 相关的缓存（Mole lib/core/app_protection.sh:62 的进程表）。
var xcodeProcs = []string{"Xcode", "xcodebuild", "XCBBuildService", "swift-frontend", "xctest", "XCTRunner", "Simulator", "simctl"}

// Categories 返回全部类别和规则（顺序参照 Mole app 的结果页）。
func Categories() []CategoryDef {
	return []CategoryDef{
		{ID: CatCaches, Title: i18n.T("App 缓存"), Rules: cacheRules()},
		{ID: CatSystem, Title: i18n.T("系统缓存"), Rules: systemRules()},
		{ID: CatLogs, Title: i18n.T("日志"), Rules: logRules()},
		{ID: CatDev, Title: i18n.T("开发者缓存"), Rules: devRules()},
		{ID: CatXcode, Title: i18n.T("Xcode 与模拟器"), Rules: xcodeRules()},
		{ID: CatAI, Title: i18n.T("AI 工具"), Rules: aiRules()},
		{ID: CatBrowser, Title: i18n.T("浏览器缓存"), Rules: browserRules()},
		{ID: CatComms, Title: i18n.T("通讯工具"), Rules: commsRules()},
		{ID: CatTrash, Title: i18n.T("废纸篓"), Rules: trashRules()},
	}
}

func ruleByID(id string) (*Rule, bool) {
	for _, c := range Categories() {
		for i := range c.Rules {
			if c.Rules[i].ID == id {
				return &c.Rules[i], true
			}
		}
	}
	return nil, false
}

var redownload = i18n.T("删了要重新下载，下次用到时会慢一些。")

// devRules：Mole lib/clean/dev.sh clean_developer_tools（:6278）。
func devRules() []Rule {
	r := []Rule{
		// dev.sh:204 clean_uv_cache：装了 uv 就跑 uv cache prune，不按进程拦（uv 自己有锁；UV_LOCK_TIMEOUT 让它
		// 遇到长期占着锁的 uvx 时 3 秒后放弃）；没装 uv 时，确认没有 uv 进程才按路径删。
		{ID: "dev.uv", Title: i18n.T("uv 缓存"), Paths: []string{"~/.cache/uv"}, Guard: []string{"uv", "uvx"}, GuardText: uvHolders, Default: true,
			Tool: &Tool{Bin: "uv", Args: []string{"cache", "prune"}, RootArgs: []string{"cache", "dir"}, Env: []string{"UV_LOCK_TIMEOUT=3"},
				Timeout: 20 * time.Second, GuardOnlyByPath: true, Estimate: uvPrunable},
			Note: i18n.T("由 uv 自己清理不再被引用的包（uv cache prune）；大小是按 uv 的索引估算的，项目正在用的包不会删。")},
		// 设计决定：另给一项「uv 全部缓存」，uv cache clean，需确认、默认不勾。
		// macOS 上 uv 默认用 clone（写时复制）把包装进 .venv，删缓存不影响已装好的环境（开发时抽查过）。
		// 有 uv / uvx 进程就跳过（uv 0.7 不认 UV_LOCK_TIMEOUT，拿不到锁会一直等）；新版 uv 拿不到锁时按输出识别。
		{ID: "dev.uv.all", Title: i18n.T("uv 全部缓存"), Paths: []string{"~/.cache/uv"}, Guard: []string{"uv", "uvx"}, GuardReason: i18n.T("uv 正在使用缓存"),
			GuardText: uvHolders, Risk: uvSymlinkRisk, Default: false,
			Tool: &Tool{Bin: "uv", Args: []string{"cache", "clean"}, RootArgs: []string{"cache", "dir"}, Env: []string{"UV_LOCK_TIMEOUT=3"},
				Timeout: 10 * time.Minute, Required: true, Busy: []string{"acquire lock", "Waiting to acquire"}},
			Note: i18n.T("清空整个 uv 缓存（uv cache clean）。已装好的虚拟环境不受影响，之后安装依赖要重新下载。")},
		// dev.sh:161 clean_corepack_cache
		{ID: "dev.corepack", Title: i18n.T("Corepack 缓存"), Paths: []string{"~/.cache/node/corepack"}, Default: true,
			Tool: &Tool{Bin: "corepack", Args: []string{"cache", "clean"}, Env: []string{"COREPACK_ENABLE_DOWNLOAD_PROMPT=0"}, Timeout: 20 * time.Second},
			Note: redownload},
		// dev.sh:663 clean_dev_npm
		{ID: "dev.npm", Title: i18n.T("npm 缓存"), Paths: []string{"~/.npm/_cacache"}, Default: true,
			Tool: &Tool{Bin: "npm", Args: []string{"cache", "clean", "--force"}, RootArgs: []string{"config", "get", "cache"}, RootSuffix: "_cacache"},
			Note: redownload},
		// dev.sh:542 clean_pnpm_stores（没装 pnpm 时 store 不动）
		{ID: "dev.pnpm", Title: "pnpm store", Guard: []string{"pnpm"}, Default: true,
			Tool: &Tool{Bin: "pnpm", Args: []string{"store", "prune"}, RootArgs: []string{"store", "path"}, Required: true},
			Note: i18n.T("由 pnpm 自己清理没有项目在用的包。")},
		// dev.sh:684：npm 默认缓存目录下的日志和预编译包（_npx 不动，运行中的命令可能在读）
		{ID: "dev.npmlogs", Title: i18n.T("npm 日志"), Paths: []string{"~/.npm/_logs", "~/.npm/_prebuilds"}, Default: true},
		// dev.sh:1692
		{ID: "dev.buildx", Title: i18n.T("Docker BuildX 缓存"), Paths: []string{"~/.docker/buildx/cache"}, Default: true, Note: i18n.T("下次构建会重新生成。")},
		// app_caches.sh:633 Zed npm 缓存：照命令行版默认勾，Zed 在运行就跳过（设计决定）
		{ID: "dev.zed", Title: i18n.T("Zed LSP 缓存"), Group: "Zed", Guard: []string{"zed", "Zed"}, Default: true,
			Paths: []string{"~/Library/Application Support/Zed/node/cache", "~/Library/Application Support/Zed/node/node-v*/cache"},
			Note:  i18n.T("Zed 语言服务器的 npm 缓存，下次打开项目时会重新下载。")},
		// app_caches.sh:589 clean_editor_obsolete_extensions：编辑器在 .obsolete 里标记过时的扩展目录
		{ID: "dev.obsoleteext", Title: i18n.T("过时的编辑器扩展"), Target: Whole, Default: true, Custom: obsoleteExtensions,
			Note: i18n.T("扩展更新后留下的旧版本，编辑器已经标记为过时。")},
		// dev.sh:720
		{ID: "dev.yarn", Title: i18n.T("Yarn 缓存"), Paths: []string{"~/Library/Caches/Yarn"}, Guard: []string{"yarn"}, Default: true, Note: redownload},
		// dev.sh:620 clean_bun_cache
		{ID: "dev.bun", Title: i18n.T("Bun 缓存"), Paths: []string{"~/.bun/install/cache"}, Guard: []string{"bun", "bunx"}, Default: true,
			Tool: &Tool{Bin: "bun", Args: []string{"pm", "cache", "rm"}, RootArgs: []string{"pm", "cache"}}, Note: redownload},
		// dev.sh:946 clean_dev_python
		// pip cache purge 只删 http / http-v2 下的下载缓存和 wheels 下本地编译的 wheel 文件，selfcheck（版本检查记录）和目录本身不删，
		// 所以大小按这些文件算（估算为 0 就不列）；旧版 pip（如 Xcode 自带的 21.x）没东西可删时会报
		// 「ERROR: No matching packages」并返回 1，算已清理（新版只给 WARNING、返回 0）。
		{ID: "dev.pip", Title: i18n.T("pip 缓存"), Paths: []string{"~/Library/Caches/pip"}, Default: true,
			Tool: &Tool{Bin: "pip3", Args: []string{"cache", "purge"}, RootArgs: []string{"cache", "dir"},
				Empty: []string{"No matching packages"}, Estimate: pipPurgeable}, Note: redownload},
		// dev.sh:1218 clean_dev_go（GOCACHE）
		{ID: "dev.gobuild", Title: i18n.T("Go 编译缓存"), Paths: []string{"~/Library/Caches/go-build"}, Default: true,
			Tool: &Tool{Bin: "go", Args: []string{"clean", "-cache"}, RootArgs: []string{"env", "GOCACHE"}},
			Note: i18n.T("下次编译会慢一些。")},
		// dev.sh:1218 clean_dev_go（GOMODCACHE）：文件是只读的，只交给 go 自己删；默认不勾
		{ID: "dev.gomod", Title: i18n.T("Go module 缓存"), Paths: []string{"~/go/pkg/mod"}, Guard: []string{"go", "gopls"}, Default: false,
			Tool: &Tool{Bin: "go", Args: []string{"clean", "-modcache"}, RootArgs: []string{"env", "GOMODCACHE"}, Required: true},
			Note: i18n.T("删了要重新下载全部依赖，下次构建会明显变慢。")},
		// dev.sh:1419 clean_dev_rust（只清下载的 .crate 包）
		{ID: "dev.cargo", Title: i18n.T("Cargo 下载缓存"), Paths: []string{"~/.cargo/registry/cache"}, Guard: []string{"cargo", "rustc"}, Default: true, Note: redownload},
		// app_caches.sh / dev.sh：CocoaPods
		{ID: "dev.cocoapods", Title: i18n.T("CocoaPods 缓存"), Paths: []string{"~/Library/Caches/CocoaPods"}, Guard: []string{"pod"}, Default: true, Note: redownload},
		// dev.sh:6319 Homebrew 下载缓存（api/、bootsnap/ 不动）
		{ID: "dev.homebrew", Title: i18n.T("Homebrew 下载缓存"), Paths: []string{"~/Library/Caches/Homebrew/downloads"}, Guard: []string{"brew"}, Default: true, Note: redownload},
		// dev.sh:1713 clean_dev_frontend
		{ID: "dev.frontend", Title: i18n.T("前端工具缓存"), Paths: []string{
			"~/.cache/typescript", "~/.cache/electron", "~/.cache/node-gyp", "~/.node-gyp", "~/.cache/vite",
			"~/.cache/webpack", "~/.cache/eslint", "~/.cache/prettier", "~/.turbo/cache", "~/.parcel-cache"},
			Default: true, Note: i18n.T("下次用到时会重新生成。")},
	}
	return inCategory(CatDev, r)
}

// xcodeRules：Mole lib/clean/app_caches.sh clean_xcode_derived_data（:215）、clean_xcode_tools（:399），
// lib/clean/dev.sh clean_xcode_device_support（:2370）、clean_dev_mobile（:3152）。
func xcodeRules() []Rule {
	r := []Rule{
		{ID: "xcode.derived", Title: "Xcode DerivedData", Paths: []string{"~/Library/Developer/Xcode/DerivedData"}, Guard: xcodeProcs, Default: true,
			Note: i18n.T("下次编译会重新生成，第一次会慢一些。")},
		{ID: "xcode.products", Title: "Xcode Products", Paths: []string{"~/Library/Developer/Xcode/Products"}, Guard: xcodeProcs, Default: true},
		{ID: "xcode.cache", Title: i18n.T("Xcode 缓存"), Paths: []string{"~/Library/Caches/com.apple.dt.Xcode"}, Guard: xcodeProcs, Default: true},
		{ID: "xcode.simcaches", Title: i18n.T("模拟器缓存"), Paths: []string{"~/Library/Developer/CoreSimulator/Caches"},
			Guard: append(append([]string{}, xcodeProcs...), "launchd_sim"), Default: true},
		{ID: "xcode.devicesupport", Title: i18n.T("设备调试符号（旧版本）"), Target: Whole, KeepNewest: 2, Guard: xcodeProcs, Default: false,
			Paths: []string{
				"~/Library/Developer/Xcode/iOS DeviceSupport/*", "~/Library/Developer/Xcode/watchOS DeviceSupport/*",
				"~/Library/Developer/Xcode/tvOS DeviceSupport/*", "~/Library/Developer/Xcode/macOS DeviceSupport/*",
				"~/Library/Developer/Xcode/visionOS DeviceSupport/*"},
			Note: i18n.T("每种系统保留最新的 2 个版本；接旧设备调试时要重新下载符号。")},
		{ID: "xcode.unavailable", Title: i18n.T("不可用的模拟器"), Guard: xcodeProcs, Default: true, Custom: unavailableSimulators,
			Note: i18n.T("对应的系统版本已经删除，这些模拟器已经无法启动。由 xcrun simctl 删除。")},
	}
	return inCategory(CatXcode, r)
}

// unavailableSimulators 用 `xcrun simctl list devices unavailable -j` 找出不可用的模拟器，交给 simctl 删除。
// 没有完整的 Xcode 或命令失败时什么都不列（Mole dev.sh:3152）。
func unavailableSimulators(ctx context.Context, env *Env) ([]Candidate, error) {
	xcrun, err := env.LookPath("xcrun")
	if err != nil {
		return nil, nil
	}
	qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := env.Run(qctx, xcrun, "simctl", "list", "devices", "unavailable", "-j")
	if err != nil {
		return nil, nil
	}
	var list struct {
		Devices map[string][]struct {
			UDID, Name  string
			IsAvailable bool
		} `json:"devices"`
	}
	if json.Unmarshal(out, &list) != nil {
		return nil, nil
	}
	base := env.expand("~/Library/Developer/CoreSimulator/Devices")
	var cs []Candidate
	for _, devs := range list.Devices {
		for _, d := range devs {
			if d.IsAvailable || d.UDID == "" || filepath.Base(d.UDID) != d.UDID {
				continue
			}
			cs = append(cs, Candidate{Path: filepath.Join(base, d.UDID), Target: Whole,
				Command: []string{xcrun, "simctl", "delete", d.UDID}, Title: d.Name})
		}
	}
	return cs, nil
}

// electronLeaves 是 Electron / Chromium app 可以重新生成的缓存目录（Mole user.sh clean_application_support_logs :2619）。
var electronLeaves = []string{"Cache", "Code Cache", "GPUCache", "DawnCache", "GrShaderCache", "GraphiteDawnCache",
	"DawnGraphiteCache", "DawnWebGPUCache", "CachedData"}

// under 把每个 app 目录和每个子目录名拼成路径。
func under(apps, leaves []string) []string {
	var out []string
	for _, a := range apps {
		for _, l := range leaves {
			out = append(out, a+"/"+l)
		}
	}
	return out
}

// 由别的类别处理的 ~/Library/Caches 子目录（App 缓存跳过它们）。
var (
	aiCacheIDs    = []string{"com.openai.chat", "com.anthropic.claudefordesktop", "com.lmstudio.lmstudio", "com.filo.client", "com.anysphere.sand", "com.openai.sky.*"}
	commsCacheIDs = []string{"us.zoom.xos", "com.tencent.xinWeChat", "ru.keepcoder.Telegram", "com.microsoft.teams2",
		"net.whatsapp.WhatsApp", "com.skype.skype", "com.tencent.meeting", "com.tencent.WeWorkMac", "com.tencent.qq",
		"dd.work.exclusive4aliding", "com.alibaba.AliLang.osx", "LarkShell", "com.feishu.*"}
	systemCacheIDs = []string{"com.apple.WebKit.Networking", "com.apple.helpd", "GeoServices", "com.apple.AppleMediaServices",
		"com.apple.duetexpertd", "com.apple.parsecd", "com.apple.python", "com.apple.rosetta.update", "com.apple.amp.mediasevicesd"}
	aiAppSupport    = []string{"Grok Bot", "Antigravity", "Qoder", "Filo"}
	commsAppSupport = []string{"discord", "Slack", "legcord"}
)

// cacheRules：Mole lib/clean/user.sh clean_user_essentials（:267）的 ~/Library/Caches/*，
// 和 clean_application_support_logs（:2619）的 Application Support/<App>/ 下 Electron 缓存。
// 删内容、留目录。对应 app 在运行就挡住；判断不了（名字对不上任何进程）的列出来、标「需确认」、默认不勾。
// 保护名单、白名单里的列出来置灰（不清理）；com.apple.* 归系统缓存类别；由别的规则处理的跳过。
func cacheRules() []Rule {
	exclude := []string{"com.apple.dt.Xcode", "com.apple.Safari", "com.microsoft.edgemac", "company.thebrowser.Browser",
		"BraveSoftware", "com.vivaldi.Vivaldi", "Firefox", "Google", // 浏览器
		"go-build", "CocoaPods", "Yarn", "pip", "Homebrew", // 开发者缓存
		"Codex", "cursor-compile-cache", "claude-cli-nodejs"} // AI 工具
	exclude = append(append(append(exclude, aiCacheIDs...), commsCacheIDs...), systemCacheIDs...)
	r := []Rule{
		{ID: "caches.apps", Title: i18n.T("App 缓存"), Paths: []string{"~/Library/Caches/*"}, BundleGuard: true, ShowProtected: true, Default: true,
			Exclude: exclude, Note: i18n.T("app 下次启动时会重新生成。")},
		{ID: "caches.appsupport", Title: i18n.T("App 网页缓存"), Paths: under([]string{"~/Library/Application Support/*"}, electronLeaves),
			BundleGuard: true, Owner: 1, Default: true, ShowProtected: true,
			Exclude: append(append([]string{"Google", "BraveSoftware", "Microsoft Edge"}, aiAppSupport...), commsAppSupport...),
			Note:    i18n.T("Electron / Chromium app 的网页和图形缓存，下次打开时会重新生成。")},
	}
	return inCategory(CatCaches, r)
}

// systemRules：Mole lib/clean/user.sh（:1260、:1298、:3864）里不需要管理员权限的系统缓存。
func systemRules() []Rule {
	paths := []string{}
	for _, id := range systemCacheIDs {
		paths = append(paths, "~/Library/Caches/"+id)
	}
	r := []Rule{
		{ID: "system.caches", Title: i18n.T("系统缓存"), Paths: append(paths, "~/Library/IdentityCaches",
			"~/Library/Containers/com.apple.AppleMediaServicesUI.UtilityExtension/Data/tmp"), Default: true,
			Note: i18n.T("macOS 会自动重建。")},
		{ID: "system.suggestions", Title: i18n.T("Siri 建议缓存"), Paths: []string{"~/Library/Suggestions"}, Default: true, // 同 Mole user.sh:1269
			Note: i18n.T("Siri 建议要重新学习一段时间。")},
	}
	return inCategory(CatSystem, r)
}

// aiRules：Mole lib/clean/app_caches.sh clean_ai_apps（:1151）、dev.sh Codex 桌面版缓存（:4515）、
// Chrome DevTools MCP（:5888）、OpenCode（:6175），以及 Application Support 下 AI app 的 Electron 缓存。
// 按 app 分组。对话、项目和本地模型不碰。
func aiRules() []Rule {
	codex := []string{"Codex", "ChatGPT", "Codex (Renderer)", "Codex (Service)"}
	support := func(app string) []string {
		return under([]string{"~/Library/Application Support/" + app}, electronLeaves)
	}
	mcp := "~/.cache/chrome-devtools-mcp/chrome-profile"
	r := []Rule{
		{ID: "ai.codex", Title: i18n.T("Codex 网页缓存"), Group: "Codex", Guard: codex, Default: true,
			Paths: under([]string{"~/Library/Caches/Codex/Default", "~/Library/Caches/Codex/Default/Partitions/codex-browser-app",
				"~/Library/Caches/Codex/codex-browser-app"}, []string{"Cache", "Code Cache"}),
			Note: i18n.T("只清网页缓存；登录状态、本地存储不碰。")},
		// dev.sh:4733 clean_codex_desktop_staging：Sparkle 下载好、但已经被装上的版本取代的更新包。
		// 只清「暂存的版本号 ≤ 已装的版本号」的；版本号读不出时，30 天没动过才算过期。当前版本和待安装的新版本绝不碰。
		{ID: "ai.codex.staging", Title: i18n.T("Codex 过期更新包"), Group: "Codex", Target: Whole, Default: true, Custom: codexStaleStaging,
			Guard: codex, GuardArgs: []string{"/Codex.app/", "org.sparkle-project.Sparkle", "Sparkle.framework/"}, PreDelete: noOpenFiles,
			Note: i18n.T("已经被新版本取代的更新下载包；正在用的版本和还没安装的更新不会删。")},
		{ID: "ai.chatgpt", Title: i18n.T("ChatGPT 缓存"), Group: "ChatGPT", BundleGuard: true, Default: true,
			Paths: []string{"~/Library/Caches/com.openai.chat", "~/Library/Caches/com.openai.sky.*"}},
		{ID: "ai.claude", Title: i18n.T("Claude 缓存"), Group: "Claude", BundleGuard: true, Default: true,
			Paths: []string{"~/Library/Caches/com.anthropic.claudefordesktop"}},
		// Application Support/Claude 在 Mole 的保护名单里（通用清理会跳过它），这里不清；它的网页缓存在 App 缓存里置灰显示
		{ID: "ai.claude.logs", Title: i18n.T("Claude 日志"), Group: "Claude", Paths: []string{"~/Library/Logs/Claude"}, Guard: []string{"Claude"}, Default: true},
		{ID: "ai.claudecli", Title: i18n.T("Claude Code 缓存"), Group: "Claude", Paths: []string{"~/Library/Caches/claude-cli-nodejs"}, Guard: []string{"claude"}, Default: true,
			Note: i18n.T("MCP 日志等临时文件。")},
		{ID: "ai.grok", Title: i18n.T("Grok Bot 缓存"), Group: "Grok Bot", BundleGuard: true, Default: true, Paths: []string{"~/Library/Caches/com.anysphere.sand"}},
		{ID: "ai.grok.web", Title: i18n.T("Grok Bot 网页缓存"), Group: "Grok Bot", BundleGuard: true, Owner: 1, Default: true, Paths: support("Grok Bot")},
		{ID: "ai.mcp", Title: i18n.T("Chrome DevTools MCP 缓存"), Group: "Chrome DevTools MCP", GuardArgs: []string{"chrome-devtools-mcp"}, Default: true,
			Paths: append(under([]string{mcp + "/Default"}, append(append([]string{}, electronLeaves[:len(electronLeaves)-1]...), "Service Worker/CacheStorage")),
				mcp+"/GraphiteDawnCache", mcp+"/component_crx_cache", mcp+"/extensions_crx_cache"),
			Note: i18n.T("MCP 启动的 Chrome 的网页缓存；登录状态不碰。")},
		{ID: "ai.cursor", Title: i18n.T("Cursor 编译缓存"), Group: "Cursor", Paths: []string{"~/Library/Caches/cursor-compile-cache"}, Guard: []string{"Cursor"}, Default: true},
		{ID: "ai.opencode", Title: i18n.T("OpenCode 缓存"), Paths: []string{"~/.cache/opencode"}, Default: true},
		{ID: "ai.others", Title: i18n.T("AI app 缓存"), BundleGuard: true, Default: true,
			Paths: []string{"~/Library/Caches/com.lmstudio.lmstudio", "~/Library/Caches/com.filo.client"}},
		{ID: "ai.others.web", Title: i18n.T("AI app 网页缓存"), BundleGuard: true, Owner: 1, Default: true,
			Paths: append(append(support("Antigravity"), support("Qoder")...), support("Filo")...)},
	}
	for i := range r {
		if r[i].Note == "" {
			r[i].Note = i18n.T("下次打开时会重新生成。")
		}
	}
	return inCategory(CatAI, r)
}

// codexStaleStaging 列出 Codex（bundle id com.openai.codex，现在装在 ChatGPT.app 里）过期的 Sparkle 更新暂存目录。
func codexStaleStaging(ctx context.Context, env *Env) ([]Candidate, error) {
	root := env.expand("~/Library/Caches/com.openai.codex/org.sparkle-project.Sparkle/Installation")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	installed, known := codexInstalledBuild(ctx, env)
	var out []Candidate
	for _, e := range entries {
		p := filepath.Join(root, e.Name())
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || validatePath(p, env, true) != nil {
			continue
		}
		staged, ok := codexStagedBuild(ctx, env, p)
		switch {
		case known && ok:
			if staged > installed {
				continue // 还没安装的新版本：不碰
			}
			out = append(out, Candidate{Path: p, Target: Whole, Title: fmt.Sprintf(i18n.T("更新包 %d（已装 %d）"), staged, installed)})
		case env.Now().Sub(info.ModTime()) > 30*24*time.Hour: // 版本号读不出：同 Mole，30 天没动过才算过期
			out = append(out, Candidate{Path: p, Target: Whole, Title: i18n.T("更新包（30 天前）")})
		}
	}
	return out, ctx.Err()
}

// codexInstalledBuild 读已装 Codex 的 CFBundleVersion；几个位置读出来不一致就当读不出（同 Mole :4783）。
func codexInstalledBuild(ctx context.Context, env *Env) (int, bool) {
	build, found := 0, false
	for _, app := range []string{"/Applications/Codex.app", "/Applications/ChatGPT.app", env.expand("~/Applications/Codex.app"), env.expand("~/Applications/ChatGPT.app")} {
		b, ok := codexBuildOf(ctx, env, app)
		if !ok {
			continue
		}
		if found && b != build {
			return 0, false
		}
		build, found = b, true
	}
	return build, found
}

// codexStagedBuild 读暂存目录里 .app 的版本号（.app 可能在第一层或第二层）；有多个且不一致就当读不出。
func codexStagedBuild(ctx context.Context, env *Env, entry string) (int, bool) {
	apps, _ := filepath.Glob(filepath.Join(entry, "*.app"))
	deeper, _ := filepath.Glob(filepath.Join(entry, "*", "*.app"))
	build, found := 0, false
	for _, app := range append(apps, deeper...) {
		b, ok := codexBuildOf(ctx, env, app)
		if !ok || found && b != build {
			return 0, false
		}
		build, found = b, true
	}
	return build, found
}

func codexBuildOf(ctx context.Context, env *Env, app string) (int, bool) {
	plist := filepath.Join(app, "Contents", "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return 0, false
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	id, err := env.Run(qctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plist)
	if err != nil || strings.TrimSpace(string(id)) != "com.openai.codex" {
		return 0, false
	}
	v, err := env.Run(qctx, "/usr/bin/plutil", "-extract", "CFBundleVersion", "raw", "-o", "-", plist)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(v)))
	if err != nil || n <= 0 || len(strings.TrimSpace(string(v))) > 10 {
		return 0, false
	}
	return n, true
}

// noOpenFiles：删之前用 lsof 看有没有程序开着这个目录里的文件（同 Mole codex_sparkle_staging_has_open_files）；判断不了也跳过。
func noOpenFiles(ctx context.Context, env *Env, path string) string {
	qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := env.Run(qctx, "/usr/sbin/lsof", "-t", "+D", path)
	if len(strings.TrimSpace(string(out))) > 0 {
		return i18n.T("有程序正在使用这些文件")
	}
	var ee interface{ ExitCode() int }
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
		return i18n.T("无法确认是否有程序正在使用这些文件")
	}
	return ""
}

// obsoleteExtensions：VS Code / Cursor 在扩展目录的 .obsolete（JSON）里记下更新后过时的扩展目录名，值为 true 的才算
// （Mole app_caches.sh:589）。每个目录是一项，按编辑器分组。
func obsoleteExtensions(ctx context.Context, env *Env) ([]Candidate, error) {
	var out []Candidate
	for _, ed := range []struct{ root, label string }{
		{"~/.vscode/extensions", "VS Code"}, {"~/.vscode-insiders/extensions", "VS Code Insiders"}, {"~/.cursor/extensions", "Cursor"},
	} {
		root := env.expand(ed.root)
		b, err := os.ReadFile(filepath.Join(root, ".obsolete"))
		if err != nil {
			continue
		}
		var keys map[string]any
		if json.Unmarshal(b, &keys) != nil {
			continue
		}
		for k, v := range keys {
			if v != true || k == "" || k == "." || k == ".." || strings.ContainsAny(k, "/\\") {
				continue
			}
			p := filepath.Join(root, k)
			info, err := os.Lstat(p)
			if err != nil || !info.IsDir() || validatePath(p, env, true) != nil {
				continue
			}
			out = append(out, Candidate{Path: p, Target: Whole, Title: k, Group: i18n.T("过时的 ") + ed.label + i18n.T(" 扩展")})
		}
	}
	return out, ctx.Err()
}

var commsNote = i18n.T("旧图片视频清理后需重新下载，过期内容无法找回。")

// commsRules：Mole lib/clean/app_caches.sh clean_communication_apps（:1106）、clean_dingtalk（:1142）、
// clean_wechat_container_caches（:1044）；微信聊天图片视频按月份列出（Mole 命令行版没有，参照 Mole app）。全部默认不勾。
func commsRules() []Rule {
	paths := []string{}
	for _, id := range commsCacheIDs {
		paths = append(paths, "~/Library/Caches/"+id)
	}
	wx := "~/Library/Containers/com.tencent.xinWeChat/Data"
	wechat := []string{"WeChat", "WeChatAppEx"}
	r := []Rule{
		{ID: "comms.caches", Title: i18n.T("聊天 app 缓存"), Paths: paths, BundleGuard: true, Note: i18n.T("app 下次启动时会重新生成。")},
		{ID: "comms.appsupport", Title: i18n.T("聊天 app 网页缓存"), Paths: under(under([]string{"~/Library/Application Support"}, commsAppSupport), []string{"Cache"}),
			BundleGuard: true, Owner: 1},
		{ID: "comms.wechat", Title: i18n.T("微信日志与小程序缓存"), Group: i18n.T("微信"), Guard: wechat, Paths: []string{
			wx + "/Documents/app_data/log", wx + "/.wxapplet/WMPF",
			wx + "/Documents/app_data/radium/web/profiles/*/Cache", wx + "/Documents/app_data/radium/web/profiles/*/Service Worker/CacheStorage",
			"~/Library/Containers/com.tencent.WeWorkMac/Data/Library/Application Support/WXWork/Log"},
			Note: i18n.T("日志和小程序、内置浏览器的缓存，下次用到时会重新下载。")},
		{ID: "comms.wechat.media", Title: i18n.T("微信聊天图片视频"), Group: i18n.T("微信"), Guard: wechat, Target: Whole, Custom: wechatMedia, Note: commsNote},
	}
	return inCategory(CatComms, r)
}

// wechatMedia 列出微信聊天里本月以前的图片视频：msg/video/<年-月> 和 msg/attach/<会话>/<年-月>，每个月份目录是一项。
// 按目录名里的月份判断（目录的修改时间会因为翻看旧图而变，不可靠）；本月的不列。
// 用户自己收发的文件（msg/file）和数据库不碰。
func wechatMedia(ctx context.Context, env *Env) ([]Candidate, error) {
	root := env.expand("~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files")
	var out []Candidate
	thisMonth := env.Now().Format("2006-01")
	add := func(glob, label string) {
		ms, _ := filepath.Glob(filepath.Join(root, glob))
		for _, m := range ms {
			info, err := os.Lstat(m)
			name := filepath.Base(m)
			if err != nil || !info.IsDir() || !monthDir.MatchString(name) || name >= thisMonth {
				continue
			}
			if validatePath(m, env, true) != nil {
				continue
			}
			title := label + " " + filepath.Base(m)
			if label == i18n.T("图片") {
				title += i18n.T(" · 会话 ") + filepath.Base(filepath.Dir(m))[:min(6, len(filepath.Base(filepath.Dir(m))))]
			}
			out = append(out, Candidate{Path: m, Target: Whole, Title: title})
		}
	}
	add("*/msg/video/*", i18n.T("视频"))
	add("*/msg/attach/*/*", i18n.T("图片"))
	return out, ctx.Err()
}

var monthDir = regexp.MustCompile(`^\d{4}-\d{2}$`)

// browserRules：Mole lib/clean/user.sh clean_browsers（:2059）。浏览器在运行就整条跳过。
// Firefox 的进程名是小写 firefox（Mole :795 用的是 Firefox，可能匹配不到）。
func browserRules() []Rule {
	chrome := []string{"Google Chrome", "Google Chrome Helper"}
	r := []Rule{
		{ID: "browser.chrome", Title: i18n.T("Chrome 缓存"), Paths: []string{"~/Library/Caches/Google/Chrome"}, Guard: chrome, Default: true},
		{ID: "browser.chrome.profile", Title: i18n.T("Chrome 代码与图形缓存"), Guard: chrome, Default: true, Paths: []string{
			"~/Library/Application Support/Google/Chrome/*/Code Cache", "~/Library/Application Support/Google/Chrome/*/GPUCache",
			"~/Library/Application Support/Google/Chrome/*/DawnCache", "~/Library/Application Support/Google/Chrome/*/GrShaderCache",
			"~/Library/Application Support/Google/Chrome/ShaderCache", "~/Library/Application Support/Google/Chrome/GrShaderCache"}},
		{ID: "browser.edge", Title: i18n.T("Edge 缓存"), Paths: []string{"~/Library/Caches/com.microsoft.edgemac"}, Guard: []string{"Microsoft Edge"}, Default: true},
		{ID: "browser.brave", Title: i18n.T("Brave 缓存"), Paths: []string{"~/Library/Caches/BraveSoftware/Brave-Browser"}, Guard: []string{"Brave Browser"}, Default: true},
		{ID: "browser.arc", Title: i18n.T("Arc 缓存"), Paths: []string{"~/Library/Caches/company.thebrowser.Browser"}, Guard: []string{"Arc"}, Default: true},
		{ID: "browser.firefox", Title: i18n.T("Firefox 缓存"), Paths: []string{"~/Library/Caches/Firefox/Profiles/*/cache2"}, Guard: []string{"firefox"}, Default: true},
		{ID: "browser.safari", Title: i18n.T("Safari 缓存"), Paths: []string{"~/Library/Caches/com.apple.Safari"}, Guard: []string{"Safari"}, Default: true},
	}
	for i := range r {
		r[i].Note = i18n.T("浏览器下次打开网页时会重新下载，不影响书签、密码和登录状态。")
	}
	return inCategory(CatBrowser, r)
}

// logRules：Mole lib/clean/user.sh _clean_user_log_directory（:224）、DiagnosticReports（:1261）。
func logRules() []Rule {
	r := []Rule{
		{ID: "logs.user", Title: i18n.T("日志"), Paths: []string{"~/Library/Logs/*", "~/Library/DiagnosticReports"}, Default: true,
			Exclude: []string{"Claude"}, // 归 AI 工具
			Note:    i18n.T("只是日志和崩溃报告，删了不影响使用。")},
	}
	return inCategory(CatLogs, r)
}

// trashRules：Mole lib/clean/user.sh clean_trash（:42）。默认不勾（作者拍板）。
func trashRules() []Rule {
	r := []Rule{
		{ID: "trash.empty", Title: i18n.T("清空废纸篓"), Paths: []string{"~/.Trash"}, AnyName: true, Default: false,
			Note: i18n.T("清空后无法恢复。外置磁盘上的废纸篓不动。")},
	}
	return inCategory(CatTrash, r)
}

func inCategory(cat string, rs []Rule) []Rule {
	for i := range rs {
		rs[i].Category = cat
	}
	return rs
}
