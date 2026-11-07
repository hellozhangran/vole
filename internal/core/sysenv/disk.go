package sysenv

import "syscall"

// Disk 是一个卷的容量。
type Disk struct {
	Total int64 // 总容量（字节）
	Free  int64 // 当前用户可用（字节）
}

// Used 返回已用空间。
func (d Disk) Used() int64 { return d.Total - d.Free }

// DiskUsage 用 statfs 读 path 所在卷的容量。APFS 上系统卷与数据卷共享容器，
// 对 "/" 取到的就是 Finder 显示的整盘容量与可用。
func DiskUsage(path string) (Disk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{}, err
	}
	bs := int64(st.Bsize)
	return Disk{Total: int64(st.Blocks) * bs, Free: int64(st.Bavail) * bs}, nil
}
