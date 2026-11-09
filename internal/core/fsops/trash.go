// SPDX-License-Identifier: GPL-3.0-only
// 移植自 tw93/Mole cmd/analyze/delete.go（commit 6d1af75，GPL-3.0）：
// moveToTrashViaFilesystem（:189）、trashDirectoryForPath（:219）、ensureOwnedTrashDirectory（:261）、moveToTrashViaFinder（:287）。
// 改动：去掉 bubbletea 包装和 /usr/bin/trash 那一路（macOS 15 才有）；家目录、是否允许 Finder 兜底改由 Env 传入，
// 便于在临时目录里测试；移动前先走 Vole 的 validatePath。

package fsops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// TrashEnv 是移到废纸篓需要的环境。
type TrashEnv struct {
	Home   string           // 家目录（判断用 ~/.Trash 还是卷上的 .Trashes）
	Now    func() time.Time // 重名时拼进新名字
	Finder bool             // 允许用 Finder 兜底
}

// MoveToTrash 把路径移到废纸篓（用户内容用这个，删错了能找回）。调用方先校验路径；
// 同一磁盘上用 renameatx_np(RENAME_EXCL) 改名进废纸篓，绝不覆盖废纸篓里的同名项；不行再用 Finder 兜底。
func MoveToTrash(ctx context.Context, env TrashEnv, p string) error {
	if err := moveToTrashViaFilesystem(env, p); err == nil {
		return nil
	} else if !env.Finder {
		return err
	}
	return MoveToTrashViaFinder(ctx, p)
}

func moveToTrashViaFilesystem(env TrashEnv, absPath string) error {
	trashDir, err := trashDirectoryForPath(env, absPath)
	if err != nil {
		return err
	}
	base := filepath.Base(absPath)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return fmt.Errorf("废纸篓里的名字不合法")
	}
	stamp := env.Now().UnixNano()
	for attempt := range 100 {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s.%d.%d.%d", base, stamp, os.Getpid(), attempt)
		}
		err = unix.RenameatxNp(unix.AT_FDCWD, absPath, unix.AT_FDCWD, filepath.Join(trashDir, name), unix.RENAME_EXCL)
		if err == nil {
			return nil
		}
		if err != syscall.EEXIST {
			return fmt.Errorf("移到废纸篓失败：%w", err)
		}
	}
	return fmt.Errorf("废纸篓里找不到不重名的名字")
}

// trashDirectoryForPath：和家目录同一个卷就用 ~/.Trash，否则用卷上的 .Trashes/<uid>。
func trashDirectoryForPath(env TrashEnv, absPath string) (string, error) {
	var pathFS, homeFS unix.Statfs_t
	if err := unix.Statfs(absPath, &pathFS); err != nil {
		return "", fmt.Errorf("读不到所在磁盘：%w", err)
	}
	if err := unix.Statfs(env.Home, &homeFS); err != nil {
		return "", fmt.Errorf("读不到家目录所在磁盘：%w", err)
	}
	if pathFS.Fsid == homeFS.Fsid {
		dir := filepath.Join(env.Home, ".Trash")
		return dir, ensureOwnedTrashDirectory(dir, true)
	}
	mount := strings.TrimRight(string(pathFS.Mntonname[:]), "\x00")
	if mount == "" {
		return "", fmt.Errorf("所在磁盘没有挂载点")
	}
	root := filepath.Join(mount, ".Trashes")
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("这个磁盘的废纸篓不可用：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("这个磁盘的废纸篓不是普通目录")
	}
	dir := filepath.Join(root, fmt.Sprint(os.Getuid()))
	return dir, ensureOwnedTrashDirectory(dir, true)
}

func ensureOwnedTrashDirectory(p string, create bool) error {
	info, err := os.Lstat(p)
	if os.IsNotExist(err) && create {
		if err := os.Mkdir(p, 0o700); err != nil {
			return fmt.Errorf("创建废纸篓目录失败：%w", err)
		}
		info, err = os.Lstat(p)
	}
	if err != nil {
		return fmt.Errorf("读不到废纸篓目录：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("废纸篓不是普通目录")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("废纸篓不属于当前用户")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("废纸篓可被其他用户写入")
	}
	return nil
}

// MoveToTrashViaFinder 是少见磁盘布局下的最后兜底（最多等 30 秒）。
func MoveToTrashViaFinder(ctx context.Context, absPath string) error {
	return MoveToTrashViaFinderWithin(ctx, absPath, 30*time.Second)
}

// MoveToTrashViaFinderWithin 让 Finder 把路径移到废纸篓；root 拥有的文件 Finder 会弹系统的管理员密码框，所以等待时间由调用方定。
func MoveToTrashViaFinderWithin(ctx context.Context, absPath string, wait time.Duration) error {
	escaped := strings.ReplaceAll(absPath, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e",
		fmt.Sprintf(`tell application "Finder" to delete POSIX file "%s"`, escaped)).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("移到废纸篓超时")
		}
		return fmt.Errorf("移到废纸篓失败：%s", strings.TrimSpace(string(out)))
	}
	return nil
}
