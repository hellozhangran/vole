// SPDX-License-Identifier: GPL-3.0-only

package fsops

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// DiskUsage 统计 root 实际占用的磁盘空间，口径同 `du -sk`：占用块数 × 512，不跟随 symlink，
// 同一个硬链接文件只算一次。读不了的子目录跳过，并返回第一个权限错误（大小是能读到的部分）。
func DiskUsage(ctx context.Context, root string) (int64, error) {
	var total int64
	var permErr error
	seen := map[[2]uint64]bool{}
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if n++; n%2048 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				if permErr == nil {
					permErr = err
				}
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil // 扫描过程中被删了
			}
			return err
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		if st.Nlink > 1 && !d.IsDir() {
			k := [2]uint64{uint64(st.Dev), st.Ino}
			if seen[k] {
				return nil
			}
			seen[k] = true
		}
		total += st.Blocks * 512
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, permErr
}

// Identity 是扫描时记下的身份，删除前比对，防止路径在扫描后被换成别的东西。
type Identity struct {
	Dev, Ino uint64
	Mode     uint32 // 文件类型位
}

// IdentityOf 读路径（不跟随 symlink）的身份。
func IdentityOf(p string) (Identity, os.FileInfo, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return Identity{}, nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}, info, errors.New("读不到文件身份")
	}
	return Identity{Dev: uint64(st.Dev), Ino: st.Ino, Mode: uint32(st.Mode) & syscall.S_IFMT}, info, nil
}
