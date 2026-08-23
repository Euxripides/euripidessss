package smartdownload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	storeSnapshotVersion      = 1
	storeSnapshotDataName     = ".store-snapshot-v1.json"
	storeSnapshotManifestName = ".store-snapshot-v1.meta.json"
)

var storeStateDirectories = []string{"packs", "batches", "addresses", "datasets", "ranges"}

type storeSnapshot struct {
	Version   int                    `json:"version"`
	Batches   map[string]*BatchJob   `json:"batches"`
	Addresses map[string]*AddressJob `json:"addresses"`
	Datasets  map[string]*DatasetJob `json:"datasets"`
	Ranges    map[string]*RangeJob   `json:"ranges"`
}

type storeSnapshotDirectoryStamp struct {
	Name             string `json:"name"`
	Exists           bool   `json:"exists"`
	ModifiedUnixNano int64  `json:"modified_unix_nano,omitempty"`
}

type storeSnapshotCounts struct {
	Batches   int `json:"batches"`
	Addresses int `json:"addresses"`
	Datasets  int `json:"datasets"`
	Ranges    int `json:"ranges"`
}

type storeSnapshotManifest struct {
	Version     int                           `json:"version"`
	GeneratedAt time.Time                     `json:"generated_at"`
	DataSHA256  string                        `json:"data_sha256"`
	DataBytes   int                           `json:"data_bytes"`
	Directories []storeSnapshotDirectoryStamp `json:"directories"`
	Counts      storeSnapshotCounts           `json:"counts"`
}

// loadSnapshot 只把快照当作经过校验的启动缓存。目录版本、长度、摘要、记录
// 数量或 ID 任一不一致都会返回 false，由调用方回退到 Packs + 独立 JSON。
func (s *Store) loadSnapshot() bool {
	manifestPayload, err := os.ReadFile(filepath.Join(s.root, storeSnapshotManifestName))
	if err != nil {
		return false
	}
	var manifest storeSnapshotManifest
	if json.Unmarshal(manifestPayload, &manifest) != nil || manifest.Version != storeSnapshotVersion {
		return false
	}
	before, err := storeSnapshotDirectoryStamps(s.root)
	if err != nil || !equalStoreSnapshotDirectoryStamps(before, manifest.Directories) {
		return false
	}

	data, err := os.ReadFile(filepath.Join(s.root, storeSnapshotDataName))
	if err != nil || len(data) != manifest.DataBytes || snapshotSHA256(data) != manifest.DataSHA256 {
		return false
	}
	var snapshot storeSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Version != storeSnapshotVersion {
		return false
	}
	if !validStoreSnapshot(snapshot, manifest.Counts) {
		return false
	}
	after, err := storeSnapshotDirectoryStamps(s.root)
	if err != nil || !equalStoreSnapshotDirectoryStamps(after, manifest.Directories) {
		return false
	}

	s.batches = snapshot.Batches
	s.addresses = snapshot.Addresses
	s.datasets = snapshot.Datasets
	s.ranges = snapshot.Ranges
	return true
}

func (s *Store) writeSnapshot() error {
	s.snapshotWriteMu.Lock()
	defer s.snapshotWriteMu.Unlock()

	s.mu.Lock()
	if s.activeWrites != 0 {
		s.mu.Unlock()
		return fmt.Errorf("smartdownload snapshot: %d active writes", s.activeWrites)
	}
	revision := s.revision
	snapshot := storeSnapshot{
		Version:   storeSnapshotVersion,
		Batches:   copyStoreMap(s.batches),
		Addresses: copyStoreMap(s.addresses),
		Datasets:  copyStoreMap(s.datasets),
		Ranges:    copyStoreMap(s.ranges),
	}
	s.mu.Unlock()

	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	dataPath := filepath.Join(s.root, storeSnapshotDataName)
	manifestPath := filepath.Join(s.root, storeSnapshotManifestName)
	_ = os.Remove(manifestPath)
	if err := writeStoreSnapshotFile(dataPath, data); err != nil {
		return err
	}

	// beginMutation 使用同一个 mu 在触碰权威文件前登记；持锁完成目录采样和
	// manifest 原子提交，可消除“快照已提交、状态文件尚未登记”的崩溃窗口。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeWrites != 0 || s.revision != revision {
		return fmt.Errorf("smartdownload snapshot changed during write")
	}
	stamps, err := storeSnapshotDirectoryStamps(s.root)
	if err != nil {
		return err
	}
	manifest := storeSnapshotManifest{
		Version:     storeSnapshotVersion,
		GeneratedAt: time.Now().UTC(),
		DataSHA256:  snapshotSHA256(data),
		DataBytes:   len(data),
		Directories: stamps,
		Counts: storeSnapshotCounts{
			Batches: len(snapshot.Batches), Addresses: len(snapshot.Addresses),
			Datasets: len(snapshot.Datasets), Ranges: len(snapshot.Ranges),
		},
	}
	manifestPayload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeStoreSnapshotFile(manifestPath, manifestPayload)
}

func storeSnapshotDirectoryStamps(root string) ([]storeSnapshotDirectoryStamp, error) {
	stamps := make([]storeSnapshotDirectoryStamp, 0, len(storeStateDirectories))
	for _, name := range storeStateDirectories {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			if os.IsNotExist(err) {
				stamps = append(stamps, storeSnapshotDirectoryStamp{Name: name})
				continue
			}
			return nil, err
		}
		stamps = append(stamps, storeSnapshotDirectoryStamp{
			Name: name, Exists: true, ModifiedUnixNano: info.ModTime().UnixNano(),
		})
	}
	return stamps, nil
}

func equalStoreSnapshotDirectoryStamps(left, right []storeSnapshotDirectoryStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validStoreSnapshot(snapshot storeSnapshot, counts storeSnapshotCounts) bool {
	if snapshot.Batches == nil || snapshot.Addresses == nil || snapshot.Datasets == nil || snapshot.Ranges == nil {
		return false
	}
	if len(snapshot.Batches) != counts.Batches || len(snapshot.Addresses) != counts.Addresses ||
		len(snapshot.Datasets) != counts.Datasets || len(snapshot.Ranges) != counts.Ranges {
		return false
	}
	for id, value := range snapshot.Batches {
		if value == nil || value.ID != id {
			return false
		}
	}
	for id, value := range snapshot.Addresses {
		if value == nil || value.ID != id {
			return false
		}
	}
	for id, value := range snapshot.Datasets {
		if value == nil || value.ID != id {
			return false
		}
	}
	for id, value := range snapshot.Ranges {
		if value == nil || value.ID != id {
			return false
		}
	}
	return true
}

func copyStoreMap[T any](source map[string]*T) map[string]*T {
	copy := make(map[string]*T, len(source))
	for id, value := range source {
		copy[id] = value
	}
	return copy
}

func snapshotSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeStoreSnapshotFile(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
