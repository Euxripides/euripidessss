//go:build !windows

package duckdb

import (
	"os"
	"syscall"
)

// processAlive 检测 PID 对应进程是否存在（POSIX：Signal 0 探活）。
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
