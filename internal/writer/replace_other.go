//go:build !windows

package writer

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
