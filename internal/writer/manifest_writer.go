package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const ManifestSchemaVersion = "1.4.1"

type Checksum struct {
	Path      string    `json:"path"`
	Algorithm string    `json:"algorithm"`
	Value     string    `json:"value"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

func ComputeChecksums(paths []string) ([]Checksum, error) {
	unique := make(map[string]struct{}, len(paths))
	items := make([]Checksum, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if _, exists := unique[clean]; exists {
			continue
		}
		unique[clean] = struct{}{}
		file, err := os.Open(clean)
		if err != nil {
			return nil, fmt.Errorf("打开校验文件 %s: %w", clean, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("计算 SHA256 %s: %w", clean, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("关闭校验文件 %s: %w", clean, closeErr)
		}
		info, err := os.Stat(clean)
		if err != nil {
			return nil, fmt.Errorf("读取文件信息 %s: %w", clean, err)
		}
		items = append(items, Checksum{
			Path:      clean,
			Algorithm: "SHA256",
			Value:     hex.EncodeToString(hash.Sum(nil)),
			SizeBytes: info.Size(),
			CreatedAt: time.Now(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func WriteJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if _, err := temp.Write(content); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFile(tempPath, path); err != nil {
		return fmt.Errorf("原子提交 %s: %w", path, err)
	}
	committed = true
	return nil
}
