package smartdownload

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strings"
)

type ReparseProgress func(completed, total, lastBlock uint64) error

// ReparseCertified rewrites existing certified Parquet through the current
// canonical writer. It cannot invoke a Provider or Downloader.
func (s *Service) ReparseCertified(ctx context.Context, chainKey, dataset string, fromBlock, toBlock uint64, parserVersion string, progress ReparseProgress) error {
	if s == nil || strings.TrimSpace(chainKey) == "" || strings.TrimSpace(dataset) == "" || strings.TrimSpace(parserVersion) == "" || toBlock < fromBlock {
		return fmt.Errorf("invalid certified reparse scope")
	}
	s.mu.Lock()
	writer := s.indexedWriter
	s.mu.Unlock()
	if writer == nil {
		return fmt.Errorf("canonical writer is unavailable")
	}
	var entries []*IndexedResult
	for _, entry := range s.results.List() {
		if entry == nil || !strings.EqualFold(entry.ChainKey, chainKey) || entry.Dataset != dataset || entry.Certification != "CERTIFIED" || entry.MergedParquet == "" || entry.ToBlock < fromBlock || entry.FromBlock > toBlock {
			continue
		}
		if info, err := os.Stat(entry.MergedParquet); err != nil || info.IsDir() {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no certified source artifact covers the requested range")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].FromBlock < entries[j].FromBlock })
	next := fromBlock
	for _, entry := range entries {
		lo, hi := maxReparse(entry.FromBlock, fromBlock), minReparse(entry.ToBlock, toBlock)
		if hi < next {
			continue
		}
		if lo > next {
			return fmt.Errorf("certified source coverage gap at block %d", next)
		}
		if hi == ^uint64(0) {
			next = hi
			break
		}
		next = hi + 1
	}
	if next <= toBlock {
		return fmt.Errorf("certified source coverage ends before block %d", toBlock)
	}
	total := toBlock - fromBlock + 1
	var completed uint64
	for _, entry := range entries {
		lo, hi := maxReparse(entry.FromBlock, fromBlock), minReparse(entry.ToBlock, toBlock)
		digest := sha256.Sum256([]byte(entry.DatasetJobID + "\x00" + parserVersion + fmt.Sprintf("\x00%d\x00%d", lo, hi)))
		result, err := writer.WriteIndexed(ctx, IndexedWriteRequest{
			DatasetJobID: fmt.Sprintf("reparse_%x", digest[:12]), ChainKey: entry.ChainKey, ChainID: entry.ChainID,
			Dataset: entry.Dataset, Address: entry.Address, FromBlock: lo, ToBlock: hi, MergedParquet: entry.MergedParquet,
			SourceProvider: "CERTIFIED_REPARSE", ParserVersion: parserVersion, NormalizerVersion: "canonical-writer-v2", SchemaVersion: 2,
		})
		if err != nil {
			return fmt.Errorf("reparse %s: %w", entry.DatasetJobID, err)
		}
		if result.InputRows != result.InsertedRows+result.RejectedRows {
			return fmt.Errorf("reparse reconciliation failed for %s", entry.DatasetJobID)
		}
		covered := hi - fromBlock + 1
		if covered > completed {
			completed = covered
		}
		if progress != nil {
			if err := progress(completed, total, hi); err != nil {
				return err
			}
		}
	}
	return nil
}

func minReparse(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxReparse(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
