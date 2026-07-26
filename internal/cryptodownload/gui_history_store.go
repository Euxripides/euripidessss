package cryptodownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const guiDownloadHistoryVersion = 1

var errGUIDownloadHistoryNotFound = errors.New("download history record not found")

type GUIDownloadHistoryStore struct {
	path   string
	mu     sync.Mutex
	rename func(string, string) error
}

type guiDownloadHistoryFile struct {
	Version int             `json:"version"`
	Records []GUIJobRecord  `json:"records"`
	Deleted map[string]bool `json:"deleted,omitempty"`
}

func NewGUIDownloadHistoryStore(configDir string) (*GUIDownloadHistoryStore, error) {
	base := strings.TrimSpace(configDir)
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user config directory: %w", err)
		}
	}
	return &GUIDownloadHistoryStore{
		path:   filepath.Join(base, "wallet-exporter", "download-history.json"),
		rename: os.Rename,
	}, nil
}

func (s *GUIDownloadHistoryStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *GUIDownloadHistoryStore) Save(record GUIJobRecord) error {
	if err := validateGUIDownloadHistoryRecord(record); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	if file.Deleted[record.ID] {
		return nil
	}
	for index := range file.Records {
		if file.Records[index].ID == record.ID {
			file.Records[index] = record
			return s.saveLocked(file)
		}
	}
	file.Records = append(file.Records, record)
	return s.saveLocked(file)
}

func (s *GUIDownloadHistoryStore) LoadAll() ([]GUIJobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	records := make([]GUIJobRecord, 0, len(file.Records))
	for _, record := range file.Records {
		if !file.Deleted[record.ID] {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID > records[j].ID
	})
	return records, nil
}

func (s *GUIDownloadHistoryStore) Find(id string) (GUIJobRecord, error) {
	if err := validateGUIDownloadHistoryID(id); err != nil {
		return GUIJobRecord{}, err
	}
	records, err := s.LoadAll()
	if err != nil {
		return GUIJobRecord{}, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, nil
		}
	}
	return GUIJobRecord{}, errGUIDownloadHistoryNotFound
}

func (s *GUIDownloadHistoryStore) Delete(id string) error {
	if err := validateGUIDownloadHistoryID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.loadLocked()
	if err != nil {
		return err
	}
	found := false
	for _, record := range file.Records {
		if record.ID == id {
			found = true
			break
		}
	}
	if !found || file.Deleted[id] {
		return errGUIDownloadHistoryNotFound
	}
	file.Deleted[id] = true
	return s.saveLocked(file)
}

func (s *GUIDownloadHistoryStore) loadLocked() (guiDownloadHistoryFile, error) {
	file := guiDownloadHistoryFile{Version: guiDownloadHistoryVersion, Deleted: map[string]bool{}}
	encoded, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, fmt.Errorf("read download history: %w", err)
	}
	if err := json.Unmarshal(encoded, &file); err != nil {
		return file, fmt.Errorf("decode download history: %w", err)
	}
	if file.Version != guiDownloadHistoryVersion {
		return file, fmt.Errorf("unsupported download history version %d", file.Version)
	}
	if file.Deleted == nil {
		file.Deleted = map[string]bool{}
	}
	return file, nil
}

func (s *GUIDownloadHistoryStore) saveLocked(file guiDownloadHistoryFile) (err error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create download history directory: %w", err)
	}
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode download history: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".download-history-*.tmp")
	if err != nil {
		return fmt.Errorf("create download history temp file: %w", err)
	}
	tempPath := temp.Name()
	tempOpen := true
	defer func() {
		if tempOpen {
			err = errors.Join(err, temp.Close())
		}
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure download history temp file: %w", err)
	}
	if _, err := temp.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write download history temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync download history temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close download history temp file: %w", err)
	}
	tempOpen = false
	if err := s.rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace download history file: %w", err)
	}
	return nil
}

func validateGUIDownloadHistoryRecord(record GUIJobRecord) error {
	if record.Version != guiJobStoreVersion {
		return fmt.Errorf("unsupported GUI job version %d", record.Version)
	}
	return validateGUIDownloadHistoryID(record.ID)
}

func validateGUIDownloadHistoryID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("invalid download history id %q", id)
	}
	return nil
}
