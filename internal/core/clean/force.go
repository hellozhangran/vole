// SPDX-License-Identifier: GPL-3.0-only

package clean

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// currentUID 是当前用户；测试里可以换掉，模拟「属主不是当前用户」。
var currentUID = os.Getuid

// removeAllWritable 删除 p（已经通过删前校验）。os.RemoveAll 因为只读目录失败（EACCES / EPERM）时，
// 给 p 下面属主是当前用户的只读目录加上 u+w 再删一次；还失败才报错。
//   - 只改目录（删文件看的是所在目录的权限），只改属主是当前用户的；root 或别人的照样报「没有权限」；
//   - 遍历不跟随 symlink；改权限用 O_NOFOLLOW 打开再 fchmod，路径被换成 symlink 也不会改到外面去；
//   - 只在 p 这棵树里改（extra 是额外允许改的目录，如「删内容、留目录」时项本身的目录），不越界。
func removeAllWritable(p string, extra ...string) error {
	err := os.RemoveAll(p)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	for _, d := range extra {
		_ = makeWritable(d)
	}
	root := filepath.Clean(p)
	_ = filepath.WalkDir(root, func(q string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() { // d.IsDir() 对 symlink 为 false：不跟进去
			return nil
		}
		if q != root && !strings.HasPrefix(q, root+string(filepath.Separator)) {
			return fs.SkipDir
		}
		_ = makeWritable(q)
		return nil
	})
	return os.RemoveAll(p)
}

// makeWritable 给一个目录加 u+w：不跟随 symlink，只改属主是当前用户的；已经可写就不动。
func makeWritable(dir string) error {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Uid != uint32(currentUID()) {
		return fs.ErrPermission
	}
	if info.Mode().Perm()&0o200 != 0 {
		return nil
	}
	return f.Chmod(info.Mode().Perm() | 0o200)
}
