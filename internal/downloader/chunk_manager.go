package downloader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	datasetwriter "github.com/etl/backend/internal/writer"
)

const DefaultChunkSize int64 = 32 << 20

type Chunk struct {
	Index int   `json:"index"`
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

func (c Chunk) Size() int64 {
	if c.End < c.Start {
		return 0
	}
	return c.End - c.Start + 1
}

type Checkpoint struct {
	SchemaVersion string         `json:"schema_version"`
	URL           string         `json:"url"`
	ETag          string         `json:"etag"`
	SizeBytes     int64          `json:"size_bytes"`
	ChunkSize     int64          `json:"chunk_size"`
	Completed     map[int]string `json:"completed"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type checkpointStore struct {
	mu   sync.Mutex
	path string
	item Checkpoint
}

func newCheckpointStore(path string, source Source, chunkSize int64) (*checkpointStore, error) {
	store := &checkpointStore{
		path: path,
		item: Checkpoint{
			SchemaVersion: "1.4.1",
			URL:           source.URL,
			ETag:          source.ETag,
			SizeBytes:     source.SizeBytes,
			ChunkSize:     chunkSize,
			Completed:     map[int]string{},
			UpdatedAt:     time.Now(),
		},
	}
	if err := store.load(source, chunkSize); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *checkpointStore) load(source Source, chunkSize int64) error {
	var stored Checkpoint
	if err := readJSON(s.path, &stored); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取下载检查点: %w", err)
	}
	if stored.URL != source.URL || stored.ETag != source.ETag ||
		stored.SizeBytes != source.SizeBytes || stored.ChunkSize != chunkSize {
		_ = os.Remove(s.path)
		_ = os.RemoveAll(chunksDir(s.path))
		return nil
	}
	if stored.Completed == nil {
		stored.Completed = map[int]string{}
	}
	s.item = stored
	return nil
}

func (s *checkpointStore) completed(chunks []Chunk) map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[int]bool, len(s.item.Completed))
	for _, chunk := range chunks {
		path, exists := s.item.Completed[chunk.Index]
		if !exists {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && info.Size() == chunk.Size() {
			result[chunk.Index] = true
			continue
		}
		delete(s.item.Completed, chunk.Index)
	}
	return result
}

func (s *checkpointStore) mark(chunk Chunk, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.item.Completed[chunk.Index] = path
	s.item.UpdatedAt = time.Now()
	return datasetwriter.WriteJSONAtomic(s.path, s.item)
}

func (s *checkpointStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.item.UpdatedAt = time.Now()
	return datasetwriter.WriteJSONAtomic(s.path, s.item)
}

func planChunks(size, chunkSize int64) []Chunk {
	if size <= 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	count := int((size + chunkSize - 1) / chunkSize)
	chunks := make([]Chunk, 0, count)
	for index, start := 0, int64(0); start < size; index, start = index+1, start+chunkSize {
		end := start + chunkSize - 1
		if end >= size {
			end = size - 1
		}
		chunks = append(chunks, Chunk{Index: index, Start: start, End: end})
	}
	return chunks
}

func PlanChunks(size, chunkSize int64) []Chunk {
	return planChunks(size, chunkSize)
}

func chunkPath(checkpointPath string, index int) string {
	return filepath.Join(chunksDir(checkpointPath), fmt.Sprintf("chunk-%06d.part", index))
}

func chunksDir(checkpointPath string) string {
	return checkpointPath + ".chunks"
}

func completedBytes(chunks []Chunk, completed map[int]bool) int64 {
	var total int64
	for _, chunk := range chunks {
		if completed[chunk.Index] {
			total += chunk.Size()
		}
	}
	return total
}

func sortedChunkPaths(checkpointPath string, chunks []Chunk) []string {
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Index < chunks[j].Index })
	paths := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		paths = append(paths, chunkPath(checkpointPath, chunk.Index))
	}
	return paths
}
