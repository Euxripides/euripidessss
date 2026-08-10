package semanticjobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var ErrNotFound = errors.New("semantic job not found")

type Store interface {
	Save(Job) error
	Get(id string) (Job, error)
	List() ([]Job, error)
}

type FileStore struct{ root string }

var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("semantic job store root is required")
	}
	clean, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve semantic job store: %w", err)
	}
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return nil, fmt.Errorf("create semantic job store: %w", err)
	}
	return &FileStore{root: clean}, nil
}

func (s *FileStore) path(id string) (string, error) {
	if !jobIDPattern.MatchString(id) {
		return "", errors.New("invalid semantic job id")
	}
	return filepath.Join(s.root, id+".json"), nil
}

func (s *FileStore) Save(job Job) error {
	path, err := s.path(job.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return fmt.Errorf("encode semantic job: %w", err)
	}
	temp, err := os.CreateTemp(s.root, job.ID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create semantic job temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write semantic job: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync semantic job: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close semantic job: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("publish semantic job: %w", err)
	}
	return nil
}

func (s *FileStore) Get(id string) (Job, error) {
	path, err := s.path(id)
	if err != nil {
		return Job{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("read semantic job: %w", err)
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, fmt.Errorf("decode semantic job %s: %w", id, err)
	}
	return job, nil
}

func (s *FileStore) List() ([]Job, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list semantic jobs: %w", err)
	}
	jobs := make([]Job, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		job, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs, nil
}
