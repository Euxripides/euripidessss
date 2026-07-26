package cryptodownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CSVCheckpointStore struct {
	path        string
	fingerprint string
	rename      func(string, string) error
}

func NewCSVCheckpointStore(rawDir, chain, address, fingerprint string) (*CSVCheckpointStore, error) {
	normalizedChain, err := csvCheckpointPathPart(chain)
	if err != nil {
		return nil, err
	}
	normalizedAddress, err := csvCheckpointPathPart(address)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rawDir) == "" {
		return nil, fmt.Errorf("checkpoint raw directory is empty")
	}
	return &CSVCheckpointStore{
		path:        filepath.Join(rawDir, "csv_"+normalizedChain, normalizedAddress, "export_state.json"),
		fingerprint: fingerprint,
		rename:      os.Rename,
	}, nil
}

func (s *CSVCheckpointStore) Path() string {
	return s.path
}

func (s *CSVCheckpointStore) Load() (CSVCheckpointState, error) {
	encoded, err := os.ReadFile(s.path)
	if err != nil {
		return CSVCheckpointState{}, fmt.Errorf("read CSV checkpoint: %w", err)
	}
	var state CSVCheckpointState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return CSVCheckpointState{}, &CSVCheckpointError{Kind: ErrCSVCheckpointDecode, Path: s.path, Err: err}
	}
	if state.Version != csvCheckpointVersion {
		return CSVCheckpointState{}, &CSVCheckpointError{Kind: ErrCSVCheckpointVersion, Path: s.path, Err: fmt.Errorf("got %d, want %d", state.Version, csvCheckpointVersion)}
	}
	if state.ConfigFingerprint != s.fingerprint {
		return CSVCheckpointState{}, &CSVCheckpointError{Kind: ErrCSVCheckpointStale, Path: s.path, Err: fmt.Errorf("configuration fingerprint changed")}
	}
	if err := validateCSVCheckpoint(state); err != nil {
		return CSVCheckpointState{}, &CSVCheckpointError{Kind: ErrCSVCheckpointManifest, Path: s.path, Err: err}
	}
	return state, nil
}

func (s *CSVCheckpointStore) Save(state CSVCheckpointState) (err error) {
	if state.Version != csvCheckpointVersion {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointVersion, Path: s.path, Err: fmt.Errorf("got %d", state.Version)}
	}
	if state.ConfigFingerprint != s.fingerprint {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointStale, Path: s.path, Err: fmt.Errorf("configuration fingerprint changed")}
	}
	if err := validateCSVCheckpoint(state); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointManifest, Path: s.path, Err: err}
	}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".export_state-*.tmp")
	if err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	tempPath := temp.Name()
	tempOpen := true
	defer func() {
		var cleanupErr error
		if tempOpen {
			cleanupErr = temp.Close()
		}
		removeErr := os.Remove(tempPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, removeErr)
		}
		if cleanupErr != nil {
			err = errors.Join(err, &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: cleanupErr})
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	if err := temp.Sync(); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	if err := temp.Close(); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	tempOpen = false
	if err := s.rename(tempPath, s.path); err != nil {
		return &CSVCheckpointError{Kind: ErrCSVCheckpointAtomicWrite, Path: s.path, Err: err}
	}
	return nil
}

func csvCheckpointPathPart(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || normalized == "." || normalized == ".." || strings.ContainsAny(normalized, `/\\`) {
		return "", fmt.Errorf("invalid checkpoint path component %q", value)
	}
	return normalized, nil
}
