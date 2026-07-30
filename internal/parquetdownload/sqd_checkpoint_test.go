package parquetdownload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQDCheckpointStore_CreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewSQDCheckpointStore(dir)

	cp, err := store.Create("job-001", "bsc", "binance-mainnet", 1000000, 1100000, 25000)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if cp.JobID != "job-001" {
		t.Errorf("expected job-001, got %s", cp.JobID)
	}
	if cp.Status != SQDCheckpointPending {
		t.Errorf("expected pending, got %s", cp.Status)
	}
	if cp.StartBlock != 1000000 {
		t.Errorf("expected start 1000000, got %d", cp.StartBlock)
	}
	if cp.EndBlock != 1100000 {
		t.Errorf("expected end 1100000, got %d", cp.EndBlock)
	}

	// Should have 5 chunks (1000000→1024999, 1025000→1049999, 1050000→1074999, 1075000→1099999, 1100000→1100000)
	expectedChunks := 5
	if len(cp.PendingChunks) != expectedChunks {
		t.Errorf("expected %d pending chunks, got %d", expectedChunks, len(cp.PendingChunks))
	}

	if cp.Manifest.TotalBlocks != 100001 {
		t.Errorf("expected 100001 total blocks, got %d", cp.Manifest.TotalBlocks)
	}

	// Reload
	loaded, err := store.Load("job-001")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.JobID != cp.JobID {
		t.Errorf("loaded JobID mismatch: %s vs %s", loaded.JobID, cp.JobID)
	}
}

func TestSQDCheckpointStore_AdvanceChunk(t *testing.T) {
	dir := t.TempDir()
	store := NewSQDCheckpointStore(dir)

	_, err := store.Create("job-002", "bsc", "binance-mainnet", 100, 200, 50)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Advance first chunk (100→149)
	err = store.AdvanceChunk("job-002", SQDBlockChunk{From: 100, To: 149})
	if err != nil {
		t.Fatalf("AdvanceChunk failed: %v", err)
	}

	cp, err := store.Load("job-002")
	if err != nil {
		t.Fatalf("Load after advance: %v", err)
	}

	if cp.CurrentBlock != 149 {
		t.Errorf("expected current_block 149, got %d", cp.CurrentBlock)
	}
	if cp.Status != SQDCheckpointInProgress {
		t.Errorf("expected in_progress, got %s", cp.Status)
	}
	if len(cp.CompletedChunks) != 1 {
		t.Errorf("expected 1 completed chunk, got %d", len(cp.CompletedChunks))
	}
	if cp.Manifest.CompletedBlocks != 50 {
		t.Errorf("expected 50 completed blocks, got %d", cp.Manifest.CompletedBlocks)
	}

	// Advance remaining chunks (150→199, 200→200)
	err = store.AdvanceChunk("job-002", SQDBlockChunk{From: 150, To: 199})
	if err != nil {
		t.Fatalf("AdvanceChunk 2 failed: %v", err)
	}
	err = store.AdvanceChunk("job-002", SQDBlockChunk{From: 200, To: 200})
	if err != nil {
		t.Fatalf("AdvanceChunk 3 failed: %v", err)
	}

	cp, _ = store.Load("job-002")
	if cp.Status != SQDCheckpointCompleted {
		t.Errorf("expected completed, got %s", cp.Status)
	}
	if len(cp.PendingChunks) != 0 {
		t.Errorf("expected 0 pending, got %d", len(cp.PendingChunks))
	}
}

func TestSQDCheckpointStore_MarkFailed(t *testing.T) {
	dir := t.TempDir()
	store := NewSQDCheckpointStore(dir)

	_, err := store.Create("job-err", "bsc", "binance-mainnet", 1, 100, 50)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = store.MarkFailed("job-err", "503 No available workers")
	if err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	cp, _ := store.Load("job-err")
	if cp.Status != SQDCheckpointFailed {
		t.Errorf("expected failed, got %s", cp.Status)
	}
	if cp.Error != "503 No available workers" {
		t.Errorf("expected error message, got %s", cp.Error)
	}
}

func TestSQDCheckpointStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewSQDCheckpointStore(dir)

	_, err := store.Create("job-del", "bsc", "binance-mainnet", 1, 100, 50)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify file exists
	path := filepath.Join(dir, "checkpoints", "job-del.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}

	err = store.Delete("job-del")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should be gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should not exist after delete")
	}
}

func TestSplitBlockRange(t *testing.T) {
	chunks := splitBlockRange(1, 10, 3)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}
	if chunks[0].From != 1 || chunks[0].To != 3 {
		t.Errorf("chunk 0: %d→%d", chunks[0].From, chunks[0].To)
	}
	if chunks[1].From != 4 || chunks[1].To != 6 {
		t.Errorf("chunk 1: %d→%d", chunks[1].From, chunks[1].To)
	}
	if chunks[2].From != 7 || chunks[2].To != 9 {
		t.Errorf("chunk 2: %d→%d", chunks[2].From, chunks[2].To)
	}
	if chunks[3].From != 10 || chunks[3].To != 10 {
		t.Errorf("chunk 3: %d→%d", chunks[3].From, chunks[3].To)
	}
}

func TestSQDCheckpointStore_Recovery(t *testing.T) {
	dir := t.TempDir()
	store := NewSQDCheckpointStore(dir)

	// Simulate: create job, complete 2 of 5 chunks, fail
	_, err := store.Create("recovery-1", "bsc", "binance-mainnet", 1000, 1249, 50)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	store.AdvanceChunk("recovery-1", SQDBlockChunk{From: 1000, To: 1049})
	store.AdvanceChunk("recovery-1", SQDBlockChunk{From: 1050, To: 1099})

	// Mark failed (503)
	store.MarkFailed("recovery-1", "503 No available workers")

	// Recovery: reload and continue from pending chunks
	cp, _ := store.Load("recovery-1")
	if len(cp.PendingChunks) != 3 {
		t.Fatalf("expected 3 pending chunks, got %d", len(cp.PendingChunks))
	}

	// Resume from first pending chunk
	nextChunk := cp.PendingChunks[0]
	if nextChunk.From != 1100 {
		t.Errorf("expected resume from 1100, got %d", nextChunk.From)
	}

	// Continue processing
	store.AdvanceChunk("recovery-1", nextChunk)
	store.AdvanceChunk("recovery-1", cp.PendingChunks[1])
	store.AdvanceChunk("recovery-1", cp.PendingChunks[2])

	cp, _ = store.Load("recovery-1")
	if cp.Status != SQDCheckpointCompleted {
		t.Errorf("expected completed after recovery, got %s", cp.Status)
	}
}
