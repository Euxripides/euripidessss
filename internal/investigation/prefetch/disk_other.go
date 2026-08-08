//go:build !windows

package prefetch

import "golang.org/x/sys/unix"

// DiskUsage 返回路径所在文件系统的使用率（0-1）。
func DiskUsage(path string) (float64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	total := st.Blocks
	avail := st.Bavail
	if total == 0 {
		return 0, nil
	}
	return 1 - float64(avail)/float64(total), nil
}

