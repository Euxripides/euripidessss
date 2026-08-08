//go:build windows

package prefetch

import (
	"golang.org/x/sys/windows"
)

// DiskUsage 返回路径所在卷的使用率（0-1）。
func DiskUsage(path string) (float64, error) {
	var free, total, avail uint64
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &avail); err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, nil
	}
	return 1 - float64(avail)/float64(total), nil
}

