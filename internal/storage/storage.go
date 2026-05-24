package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FileStorage implements the model.Storage interface using the filesystem
// Future: implement a DatabaseStorage for DB integration
type FileStorage struct {
	UploadDir string
	OutputDir string
}

func NewFileStorage(uploadDir, outputDir string) *FileStorage {
	return &FileStorage{
		UploadDir: uploadDir,
		OutputDir: outputDir,
	}
}

// Session represents an upload/process session
type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	FilePath  string    `json:"file_path,omitempty"`
	Status    string    `json:"status"`
}

func (fs *FileStorage) CreateSession() (*Session, error) {
	id := uuid.New().String()[:12]
	session := &Session{
		ID:        id,
		CreatedAt: time.Now(),
		Status:    "created",
	}
	return session, nil
}

func (fs *FileStorage) GetSession(id string) (*Session, error) {
	sessionDir := filepath.Join(fs.UploadDir, "flow_sessions", id)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return &Session{ID: id, Status: "exists"}, nil
}

func (fs *FileStorage) ListSessions() ([]Session, error) {
	sessionsDir := filepath.Join(fs.UploadDir, "flow_sessions")
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return nil, nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, e := range entries {
		if e.IsDir() {
			sessions = append(sessions, Session{
				ID: e.Name(), Status: "exists",
			})
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID < sessions[j].ID
	})
	return sessions, nil
}

func (fs *FileStorage) SaveFile(sessionDir, subDir, filename string, data []byte) (string, error) {
	dir := filepath.Join(sessionDir, subDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func (fs *FileStorage) SaveUploadFile(fileData []byte, filename string, sessionID string) (string, error) {
	sessionDir := filepath.Join(fs.UploadDir, "flow_sessions", sessionID)
	return fs.SaveFile(sessionDir, "uploads", filename, fileData)
}

func (fs *FileStorage) ListOutputs() ([]map[string]interface{}, error) {
	if _, err := os.Stat(fs.OutputDir); os.IsNotExist(err) {
		return nil, nil
	}
	entries, err := os.ReadDir(fs.OutputDir)
	if err != nil {
		return nil, err
	}
	var files []map[string]interface{}
	for _, e := range entries {
		if !e.IsDir() {
			info, _ := e.Info()
			files = append(files, map[string]interface{}{
				"name":       e.Name(),
				"path":       e.Name(),
				"size":       info.Size(),
				"updated_at": info.ModTime().Unix(),
			})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i]["name"].(string) < files[j]["name"].(string)
	})
	return files, nil
}

func (fs *FileStorage) GetOutput(filename string) ([]byte, string, error) {
	// Allow partial path matching
	path := filepath.Join(fs.OutputDir, filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Try to find by partial match
		entries, _ := filepath.Glob(filepath.Join(fs.OutputDir, "*" + filename + "*"))
		if len(entries) > 0 {
			path = entries[0]
		} else {
			return nil, "", fmt.Errorf("output not found: %s", filename)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	// Detect content type
	ext := strings.ToLower(filepath.Ext(path))
	contentType := "application/octet-stream"
	switch ext {
	case ".csv":
		contentType = "text/csv; charset=utf-8-sig"
	case ".xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".zip":
		contentType = "application/zip"
	case ".json":
		contentType = "application/json"
	}
	return data, contentType, nil
}
