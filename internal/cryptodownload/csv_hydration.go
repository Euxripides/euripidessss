package cryptodownload

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CSVHydrationMismatchError struct {
	Path   string
	Reason string
}

func (e *CSVHydrationMismatchError) Error() string {
	return fmt.Sprintf("CSV hydration checkpoint/disk mismatch at %s: %s", e.Path, e.Reason)
}

type csvKindHydration struct {
	Mapped           []map[string]any
	Raw              []map[string]string
	Headers          []string
	SeenRows         map[string]bool
	NextSegment      int
	NextEndExclusive int64
}

func (c *CSVExportClient) hydrateCSVKind(cfg Config, chain string, kind csvExportKind, start, end int64) (csvKindHydration, error) {
	empty := csvKindHydration{SeenRows: map[string]bool{}, NextSegment: 1, NextEndExclusive: end}
	if c.rawDir == "" {
		return empty, nil
	}
	store, err := NewCSVCheckpointStore(c.rawDir, chain, cfg.Address, csvRawFingerprint(cfg, chain))
	if err != nil {
		return csvKindHydration{}, fmt.Errorf("create hydration checkpoint store: %w", err)
	}
	state, err := store.Load()
	if err == nil {
		checkpoint, exists := state.Kinds[CSVCheckpointKind(kind.Name)]
		if exists {
			return hydrateCSVCheckpointKind(cfg, chain, kind, filepath.Dir(store.Path()), checkpoint, end)
		}
		return c.migrateLegacyCSVKind(cfg, chain, kind, start, end, store, state)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return csvKindHydration{}, fmt.Errorf("load hydration checkpoint: %w", err)
	}
	state = NewCSVCheckpointState(cfg.Address, chain, csvRawFingerprint(cfg, chain))
	return c.migrateLegacyCSVKind(cfg, chain, kind, start, end, store, state)
}

func (c *CSVExportClient) migrateLegacyCSVKind(cfg Config, chain string, kind csvExportKind, start, end int64, store *CSVCheckpointStore, state CSVCheckpointState) (csvKindHydration, error) {
	dir := filepath.Dir(store.Path())
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return csvKindHydration{SeenRows: map[string]bool{}, NextSegment: 1, NextEndExclusive: end}, nil
	} else if err != nil {
		return csvKindHydration{}, fmt.Errorf("stat legacy CSV directory %s: %w", dir, err)
	}
	scan, err := ScanLegacyCSV(dir, cfg.Address, kind)
	if err != nil {
		return csvKindHydration{}, fmt.Errorf("scan legacy CSV for hydration: %w", err)
	}
	if len(scan.Segments) == 0 {
		return csvKindHydration{SeenRows: map[string]bool{}, NextSegment: 1, NextEndExclusive: end}, nil
	}
	if err := ValidateLegacyCSVMigrationRange(cfg.CSVStartTime, cfg.CSVEndTime); err != nil {
		return csvKindHydration{}, err
	}
	checkpoint := CSVKindCheckpoint{NextStart: scan.LastUnix - 1, EndTime: end, Segments: make([]CSVSegmentManifest, 0, len(scan.Segments))}
	for _, segment := range scan.Segments {
		body, readErr := os.ReadFile(segment.Path)
		if readErr != nil {
			return csvKindHydration{}, fmt.Errorf("read legacy segment %s: %w", segment.Path, readErr)
		}
		sum := sha256.Sum256(body)
		checkpoint.Segments = append(checkpoint.Segments, CSVSegmentManifest{StartTime: segment.LastUnix, EndTime: segment.FirstUnix, File: filepath.Base(segment.Path), Rows: int64(segment.Rows), SHA256: hex.EncodeToString(sum[:])})
	}
	state.Kinds[CSVCheckpointKind(kind.Name)] = checkpoint
	if err := store.Save(state); err != nil {
		return csvKindHydration{}, fmt.Errorf("save migrated hydration checkpoint: %w", err)
	}
	return hydrateCSVCheckpointKind(cfg, chain, kind, dir, checkpoint, end)
}

func hydrateCSVCheckpointKind(cfg Config, chain string, kind csvExportKind, dir string, checkpoint CSVKindCheckpoint, fallbackEnd int64) (csvKindHydration, error) {
	result := csvKindHydration{SeenRows: map[string]bool{}, NextSegment: len(checkpoint.Segments) + 1, NextEndExclusive: checkpoint.NextStart}
	if result.NextEndExclusive <= 0 {
		result.NextEndExclusive = fallbackEnd
	}
	wantFiles := make(map[string]bool, len(checkpoint.Segments))
	for _, manifest := range checkpoint.Segments {
		wantFiles[manifest.File] = true
		path := filepath.Join(dir, manifest.File)
		body, err := os.ReadFile(path)
		if err != nil {
			return csvKindHydration{}, &CSVHydrationMismatchError{Path: path, Reason: err.Error()}
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != manifest.SHA256 {
			return csvKindHydration{}, &CSVHydrationMismatchError{Path: path, Reason: "SHA-256 differs from checkpoint"}
		}
		records, headers, err := parseCSVRecordsForKind(kind, body, cfg.Address)
		if err != nil || int64(len(records)) != manifest.Rows || !csvValidateAddress(records, cfg.Address) {
			return csvKindHydration{}, &CSVHydrationMismatchError{Path: path, Reason: fmt.Sprintf("rows/address/CSV invalid: rows=%d want=%d err=%v", len(records), manifest.Rows, err)}
		}
		if len(result.Headers) == 0 {
			result.Headers = headers
		}
		mapped, raw := mapNewCSVRecords(cfg.Address, strings.ToUpper(chain), kind, records, result.SeenRows)
		result.Mapped = append(result.Mapped, mapped...)
		result.Raw = append(result.Raw, raw...)
	}
	paths, err := filepath.Glob(filepath.Join(dir, kind.Name+"_segment_*.csv"))
	if err != nil {
		return csvKindHydration{}, fmt.Errorf("glob hydrated CSV segments: %w", err)
	}
	for _, path := range paths {
		if !wantFiles[filepath.Base(path)] {
			return csvKindHydration{}, &CSVHydrationMismatchError{Path: path, Reason: "segment exists on disk but not in checkpoint"}
		}
	}
	if checkpoint.Complete || len(checkpoint.Segments) > 0 && checkpoint.Segments[len(checkpoint.Segments)-1].Rows < csvMaxRowsPerExport {
		result.NextEndExclusive = cfg.CSVStartTime
	}
	return result, nil
}
