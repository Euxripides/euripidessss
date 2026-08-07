package parquetdownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SQDCheckpointStatus represents the state of a checkpointed job.
type SQDCheckpointStatus string

const (
	SQDCheckpointPending      SQDCheckpointStatus = "pending"
	SQDCheckpointDownloading  SQDCheckpointStatus = "downloading"
	SQDCheckpointSuccess      SQDCheckpointStatus = "success"
	SQDCheckpointWaitingRetry SQDCheckpointStatus = "waiting_retry"
	SQDCheckpointFailed       SQDCheckpointStatus = "failed"

	// Backward-compatible aliases
	SQDCheckpointInProgress = SQDCheckpointDownloading
	SQDCheckpointCompleted  = SQDCheckpointSuccess
)

// SQDBlockChunk represents a segment of block range to process.
type SQDBlockChunk struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
}

// SQDCheckpoint persists the state of a SQD ingestion job, enabling
// recovery after failures (503, network errors, restarts).
type SQDCheckpoint struct {
	JobID           string                `json:"job_id"`
	Chain           string                `json:"chain"`
	Dataset         string                `json:"dataset"`
	StartBlock      uint64                `json:"start_block"`
	EndBlock        uint64                `json:"end_block"`
	CurrentBlock    uint64                `json:"current_block"`
	CompletedChunks []SQDBlockChunk       `json:"completed_chunks"`
	PendingChunks   []SQDBlockChunk       `json:"pending_chunks"`
	Manifest        SQDCheckpointManifest `json:"manifest"`
	Status          SQDCheckpointStatus   `json:"status"`
	Error           string                `json:"error,omitempty"`
}

// SQDCheckpointManifest holds metadata about the checkpoint.
type SQDCheckpointManifest struct {
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	TotalBlocks     uint64    `json:"total_blocks"`
	CompletedBlocks uint64    `json:"completed_blocks"`
	ChunkSize       uint64    `json:"chunk_size"`
}

// DefaultSQDChunkSize is the default number of blocks per batch.
const DefaultSQDChunkSize = 50000

// SQDCheckpointStore manages checkpoint persistence.
type SQDCheckpointStore struct {
	mu  sync.Mutex
	dir string
}

// NewSQDCheckpointStore creates a checkpoint store rooted at dir.
func NewSQDCheckpointStore(dataRoot string) *SQDCheckpointStore {
	dir := filepath.Join(dataRoot, "checkpoints")
	return &SQDCheckpointStore{dir: dir}
}

// Create initializes a new checkpoint and splits the block range into chunks.
func (s *SQDCheckpointStore) Create(jobID, chain, dataset string, startBlock, endBlock uint64, chunkSize uint64) (*SQDCheckpoint, error) {
	if chunkSize == 0 {
		chunkSize = DefaultSQDChunkSize
	}

	chunks := splitBlockRange(startBlock, endBlock, chunkSize)
	now := time.Now().UTC()

	cp := &SQDCheckpoint{
		JobID:           jobID,
		Chain:           chain,
		Dataset:         dataset,
		StartBlock:      startBlock,
		EndBlock:        endBlock,
		CurrentBlock:    startBlock,
		CompletedChunks: make([]SQDBlockChunk, 0),
		PendingChunks:   chunks,
		Status:          SQDCheckpointPending,
		Manifest: SQDCheckpointManifest{
			CreatedAt:       now,
			UpdatedAt:       now,
			TotalBlocks:     endBlock - startBlock + 1,
			CompletedBlocks: 0,
			ChunkSize:       chunkSize,
		},
	}
	return cp, s.Save(cp)
}

// Load reads a checkpoint from disk.
func (s *SQDCheckpointStore) Load(jobID string) (*SQDCheckpoint, error) {
	path := s.path(jobID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("checkpoint not found for job %s", jobID)
	}
	if err != nil {
		return nil, err
	}
	var cp SQDCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("parse checkpoint: %w", err)
	}
	return &cp, nil
}

// Save persists a checkpoint to disk.
func (s *SQDCheckpointStore) Save(cp *SQDCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp.Manifest.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	path := s.path(cp.JobID)
	return os.WriteFile(path, data, 0644)
}

// AdvanceChunk marks a chunk as completed and updates current_block.
func (s *SQDCheckpointStore) AdvanceChunk(jobID string, chunk SQDBlockChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, err := s.loadLocked(jobID)
	if err != nil {
		return err
	}

	// Move chunk from pending to completed
	cp.CompletedChunks = append(cp.CompletedChunks, chunk)
	remaining := make([]SQDBlockChunk, 0, len(cp.PendingChunks))
	for _, c := range cp.PendingChunks {
		if c.From != chunk.From || c.To != chunk.To {
			remaining = append(remaining, c)
		}
	}
	cp.PendingChunks = remaining
	cp.CurrentBlock = chunk.To
	cp.Manifest.CompletedBlocks += (chunk.To - chunk.From + 1)
	cp.Manifest.UpdatedAt = time.Now().UTC()

	if len(cp.PendingChunks) == 0 {
		cp.Status = SQDCheckpointSuccess
	} else {
		cp.Status = SQDCheckpointDownloading
	}

	return s.saveLocked(cp)
}

// MarkWaitingRetry marks a chunk as needing retry (temporary failure).
// Unlike MarkFailed, this does not mark the entire job as failed.
func (s *SQDCheckpointStore) MarkWaitingRetry(jobID string, chunk SQDBlockChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, err := s.loadLocked(jobID)
	if err != nil {
		return err
	}
	// Move chunk back to pending (it will be retried)
	// Ensure it's not already in pending
	alreadyPending := false
	for _, c := range cp.PendingChunks {
		if c.From == chunk.From && c.To == chunk.To {
			alreadyPending = true
			break
		}
	}
	if !alreadyPending {
		cp.PendingChunks = append(cp.PendingChunks, chunk)
	}
	cp.Status = SQDCheckpointWaitingRetry
	cp.Manifest.UpdatedAt = time.Now().UTC()
	return s.saveLocked(cp)
}

// MarkFailed records an error on the checkpoint.
func (s *SQDCheckpointStore) MarkFailed(jobID string, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp, err := s.loadLocked(jobID)
	if err != nil {
		return err
	}
	cp.Status = SQDCheckpointFailed
	cp.Error = errMsg
	cp.Manifest.UpdatedAt = time.Now().UTC()
	return s.saveLocked(cp)
}

// Delete removes a checkpoint.
func (s *SQDCheckpointStore) Delete(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.path(jobID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// List returns all checkpoint job IDs.
func (s *SQDCheckpointStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

func (s *SQDCheckpointStore) path(jobID string) string {
	return filepath.Join(s.dir, jobID+".json")
}

func (s *SQDCheckpointStore) loadLocked(jobID string) (*SQDCheckpoint, error) {
	path := s.path(jobID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cp SQDCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

func (s *SQDCheckpointStore) saveLocked(cp *SQDCheckpoint) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path(cp.JobID), data, 0644)
}

// splitBlockRange divides a block range into chunks of chunkSize.
func splitBlockRange(from, to, chunkSize uint64) []SQDBlockChunk {
	if from > to || chunkSize == 0 {
		return nil
	}
	var chunks []SQDBlockChunk
	for current := from; current <= to; current += chunkSize {
		end := current + chunkSize - 1
		if end > to {
			end = to
		}
		chunks = append(chunks, SQDBlockChunk{From: current, To: end})
	}
	return chunks
}
