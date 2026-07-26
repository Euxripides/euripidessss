package cryptodownload

import (
	"errors"
	"fmt"
	"path/filepath"
)

var ErrCSVWriterLocked = errors.New("CSV writer lock conflict")

type CSVWriterLockError struct {
	Path string
	Err  error
}

func (e *CSVWriterLockError) Error() string {
	return fmt.Sprintf("%v %q: %v", ErrCSVWriterLocked, e.Path, e.Err)
}
func (e *CSVWriterLockError) Unwrap() []error { return []error{ErrCSVWriterLocked, e.Err} }

func csvWriterLockPath(rawDir, chain, address, fingerprint string, kind CSVCheckpointKind) (string, error) {
	parts := []string{chain, address, fingerprint, string(kind)}
	for index, part := range parts {
		normalized, err := csvCheckpointPathPart(part)
		if err != nil {
			return "", fmt.Errorf("writer lock key part %d: %w", index, err)
		}
		parts[index] = normalized
	}
	return filepath.Join(rawDir, "csv_"+parts[0], parts[1], "."+parts[2]+"."+parts[3]+".lock"), nil
}
