// SPDX-License-Identifier: GPL-3.0-only
// 名单移植自 tw93/Mole lib/core/app_protection_data.sh（commit 6d1af75，GPL-3.0）：
// APPLE_UNINSTALLABLE_APPS（:163）、OFFICIAL_UNINSTALLER_RULES（:179）；判断方式同 lib/core/app_protection.sh
// should_protect_from_uninstall（:143）、official_uninstaller_vendor（:161）。

package apps

import (
	"path"
	"strings"

	"vole/internal/core/fsops"
)

// appleUninstallable：Apple 自己的、可以卸的 app（App Store 或开发者网站装的）。
var appleUninstallable = []string{
	"com.apple.dt.*", "com.apple.FinalCut*", "com.apple.Motion", "com.apple.Compressor", "com.apple.logic*",
	"com.apple.garageband*", "com.apple.iMovie", "com.apple.iWork.*", "com.apple.MainStage*", "com.apple.server.*",
	"com.apple.Playgrounds",
}

// officialUninstaller：要用厂商自己的卸载器的 app（会留下 daemon、系统扩展，只有厂商工具删得干净）。
var officialUninstaller = []struct {
	vendor   string
	prefixes []string
	names    []string
}{
	{"ESET", []string{"com.eset."}, []string{"eset management agent", "eset remote administrator agent", "eset endpoint security", "eset endpoint antivirus"}},
	{"Jamf", []string{"com.jamf.", "com.jamfsoftware."}, []string{"jamf connect", "jamf protect", "jamf self service"}},
	{"CrowdStrike", []string{"com.crowdstrike."}, []string{"crowdstrike", "falcon"}},
	{"SentinelOne", []string{"com.sentinelone.", "com.sentinel-labs."}, []string{"sentinelone", "sentinel agent"}},
	{"GlobalProtect", []string{"com.paloaltonetworks."}, []string{"globalprotect"}},
	{"Cisco", []string{"com.cisco.anyconnect", "com.cisco.secureclient"}, []string{"cisco secure client", "cisco anyconnect"}},
	{"Alibaba Cloud Security", []string{"com.alibaba.security."}, nil},
	{"Adobe Genuine Service", []string{"com.adobe.gcclient", "gocart."}, []string{"adobe genuine service"}},
	{"Avast", []string{"com.avast.aafm", "com.avast.avast", "com.avast.hub"}, nil},
	{"AVG", []string{"com.avg.hub"}, []string{"avgantivirus", "avg antivirus"}},
	{"Malwarebytes", []string{"com.malwarebytes.mbam"}, nil},
	{"TeamViewer", []string{"com.teamviewer."}, nil},
	{"Bitdefender", []string{"com.bitdefender.antivirusformac"}, nil},
	{"Intego", []string{"com.intego.virusbarrier.", "com.intego.virusbarrier-", "com.intego.app.netbarrier"}, nil},
	{"Kaspersky", []string{"com.kaspersky.kav"}, nil},
	{"Norton", []string{"com.symantec.nfm"}, nil},
	{"Trend Micro", []string{"com.trendmicro.tmsm", "com.trendmicro.icore"}, nil},
}

// systemApp：系统关键 app，不出现在列表里（先放行 Apple 可卸载名单，再查系统关键名单；区分大小写，同 Mole）。
func systemApp(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	for _, g := range appleUninstallable {
		if ok, _ := path.Match(g, bundleID); ok {
			return false
		}
	}
	for _, g := range fsops.SystemCriticalBundles {
		if ok, _ := path.Match(g, bundleID); ok {
			return true
		}
	}
	return false
}

// officialVendor：要用厂商卸载器时返回厂商名（bundle id 前缀，或显示名 / 文件名里含规则片段）。
func officialVendor(bundleID, name, file string) string {
	id := strings.ToLower(bundleID)
	n, f := strings.ToLower(name), strings.ToLower(file)
	for _, r := range officialUninstaller {
		for _, p := range r.prefixes {
			if strings.HasPrefix(id, p) {
				return r.vendor
			}
		}
		for _, frag := range r.names {
			if strings.Contains(n, frag) || strings.Contains(f, frag) {
				return r.vendor
			}
		}
	}
	return ""
}
