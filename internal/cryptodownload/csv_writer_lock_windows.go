//go:build windows

package cryptodownload

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type CSVWriterLock struct {
	handle windows.Handle
	path   string
}

func AcquireCSVWriterLock(rawDir, chain, address, fingerprint string, kind CSVCheckpointKind) (*CSVWriterLock, error) {
	path, err := csvWriterLockPath(rawDir, chain, address, fingerprint, kind)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create CSV writer lock directory: %w", err)
	}
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("encode CSV writer lock path: %w", err)
	}
	handle, err := windows.CreateFile(path16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, &CSVWriterLockError{Path: path, Err: err}
		}
		return nil, fmt.Errorf("acquire CSV writer lock %q: %w", path, err)
	}
	return &CSVWriterLock{handle: handle, path: path}, nil
}

func (l *CSVWriterLock) Close() error {
	if l == nil || l.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(l.handle)
	l.handle = windows.InvalidHandle
	removeErr := os.Remove(l.path)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(err, removeErr)
	}
	return err
}
