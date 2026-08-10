//go:build windows

package cryptodownload

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func TestCSVBrowserProcessCancellationStopsDescendant(t *testing.T) {
	pythonPath, err := resolveCrawl4AIPython()
	if err != nil {
		t.Skipf("Python unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", strings.Join([]string{
		"import subprocess,sys,time",
		"child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'])",
		"print(child.pid,flush=True)",
		"time.sleep(60)",
	}, ";"))
	command.Env = csvBrowserSubprocessEnv()
	stdout, _, runErr := runCSVBrowserCommand(command, nil)
	if runErr == nil {
		t.Fatal("runCSVBrowserCommand() error = nil, want cancellation")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(stdout)))
	if err != nil || pid <= 0 {
		t.Fatalf("child PID output = %q, error = %v", stdout, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if openErr != nil {
			return
		}
		var exitCode uint32
		statusErr := windows.GetExitCodeProcess(process, &exitCode)
		windows.CloseHandle(process)
		if statusErr != nil || exitCode != windowsStillActive {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("browser descendant process %d remained active after cancellation", pid)
}
