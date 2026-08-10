//go:build windows

package api

import "golang.org/x/sys/windows"

func smartDownloadDiskFreeBytes(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
