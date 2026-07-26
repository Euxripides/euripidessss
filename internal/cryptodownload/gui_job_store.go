package cryptodownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type GUIJobStore struct {
	dir    string
	mu     sync.Mutex
	rename func(string, string) error
}

func NewGUIJobStore(configDir string) (*GUIJobStore, error) {
	base := strings.TrimSpace(configDir)
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user config directory: %w", err)
		}
	}
	return &GUIJobStore{dir: filepath.Join(base, "wallet-exporter", "jobs"), rename: os.Rename}, nil
}

func (s *GUIJobStore) Path(id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return "", fmt.Errorf("invalid GUI job id %q", id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func (s *GUIJobStore) Save(record GUIJobRecord) (err error) {
	path, err := s.Path(record.ID)
	if err != nil {
		return err
	}
	if record.Version != guiJobStoreVersion {
		return fmt.Errorf("unsupported GUI job version %d", record.Version)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode GUI job: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create GUI jobs directory: %w", err)
	}
	temp, err := os.CreateTemp(s.dir, ".job-*.tmp")
	if err != nil {
		return fmt.Errorf("create GUI job temp file: %w", err)
	}
	tempPath := temp.Name()
	tempOpen := true
	defer func() {
		var closeErr error
		if tempOpen {
			closeErr = temp.Close()
		}
		removeErr := os.Remove(tempPath)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure GUI job temp file: %w", err)
	}
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write GUI job temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync GUI job temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close GUI job temp file: %w", err)
	}
	tempOpen = false
	if err := s.rename(tempPath, path); err != nil {
		return fmt.Errorf("replace GUI job file: %w", err)
	}
	return nil
}

func (s *GUIJobStore) LoadAll() ([]GUIJobRecord, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read GUI jobs directory: %w", err)
	}
	records := make([]GUIJobRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		encoded, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read GUI job %s: %w", entry.Name(), readErr)
		}
		var record GUIJobRecord
		if decodeErr := json.Unmarshal(encoded, &record); decodeErr != nil {
			return nil, fmt.Errorf("decode GUI job %s: %w", entry.Name(), decodeErr)
		}
		if record.Version != guiJobStoreVersion {
			return nil, fmt.Errorf("GUI job %s has unsupported version %d", entry.Name(), record.Version)
		}
		if record.ID+".json" != entry.Name() {
			return nil, fmt.Errorf("GUI job id does not match filename %s", entry.Name())
		}
		records = append(records, record)
	}
	return records, nil
}
