// SPDX-License-Identifier: GPL-3.0-only
// 校验语义参考自 tw93/Mole lib/core/file_ops.sh validate_path_for_deletion（:1160，commit 6d1af75，GPL-3.0），
// 用 Go 重写；比 Mole 更严：路径必须在家目录下，任何一级上级目录是 symlink 都拒绝。

package clean

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// validatePath 校验一个要删除的路径。返回的错误是给用户看的原因。
//   - 绝对路径、已规范化（不含 .. 和多余的 /）、不含控制字符；
//   - 必须在家目录下，且不是关键系统路径；
//   - 每一级上级目录都不能是 symlink：用真实路径比对，防止 ~/Library/Caches 被链到别处后删到别的目录；
//   - 不在保护名单和白名单里。checkName 为 true 时还检查名字本身（真正被删的那一层都要查）；
//     「删内容、留目录」的根目录名字可以受保护（如 ~/Library/Caches/com.apple.dt.Xcode），它的子项逐个再查。
func validatePath(p string, env *Env, checkName bool) error {
	if p == "" || !filepath.IsAbs(p) {
		return fmt.Errorf("不是绝对路径")
	}
	if p != filepath.Clean(p) {
		return fmt.Errorf("路径不规范")
	}
	if strings.ContainsFunc(p, unicode.IsControl) {
		return fmt.Errorf("路径含控制字符")
	}
	if !strings.HasPrefix(p, env.Home+"/") {
		return fmt.Errorf("不在家目录下")
	}
	if criticalPath(p, env.Home) {
		return fmt.Errorf("系统关键路径")
	}
	parent := filepath.Dir(p)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("无法确认上级目录：%v", err)
	}
	if real != parent {
		return fmt.Errorf("上级目录经过符号链接（实际指向 %s）", real)
	}
	if protectedPath(p, env.Home) || (checkName && protectedName(filepath.Base(p))) {
		return fmt.Errorf("在保护名单里")
	}
	if whitelisted(p, env) {
		return fmt.Errorf("在白名单里")
	}
	return nil
}
