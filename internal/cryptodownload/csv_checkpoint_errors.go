package cryptodownload

import (
	"errors"
	"fmt"
)

var (
	ErrCSVCheckpointDecode      = errors.New("decode CSV checkpoint")
	ErrCSVCheckpointVersion     = errors.New("unsupported CSV checkpoint version")
	ErrCSVCheckpointStale       = errors.New("stale CSV checkpoint")
	ErrCSVCheckpointManifest    = errors.New("invalid CSV checkpoint manifest")
	ErrCSVCheckpointAtomicWrite = errors.New("atomic CSV checkpoint write")
)

type CSVCheckpointError struct {
	Kind error
	Path string
	Err  error
}

func (e *CSVCheckpointError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Kind, e.Path)
	}
	return fmt.Sprintf("%s %s: %v", e.Kind, e.Path, e.Err)
}

func (e *CSVCheckpointError) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}
