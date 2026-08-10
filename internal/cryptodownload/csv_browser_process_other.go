//go:build !windows

package cryptodownload

import "os/exec"

func prepareCSVBrowserProcess(_ *exec.Cmd) {}

func attachCSVBrowserProcessTree(_ *exec.Cmd) (func(), error) {
	return func() {}, nil
}
