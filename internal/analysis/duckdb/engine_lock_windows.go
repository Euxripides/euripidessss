//go:build windows

package duckdb

import "syscall"

// processAlive 检测 PID 对应进程是否存在（Windows：OpenProcess 句柄）。
func processAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(h)
	return true
}
