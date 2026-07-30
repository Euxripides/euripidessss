//go:build windows

package parquetdownload

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32Disk        = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExW = kernel32Disk.NewProc("GetDiskFreeSpaceExW")
)

func diskFreeBytes(path string) (uint64, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeAvailable uint64
	var totalBytes uint64
	var totalFree uint64
	result, _, callErr := getDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if result == 0 {
		return 0, fmt.Errorf("读取磁盘剩余空间失败: %w", callErr)
	}
	return freeAvailable, nil
}
