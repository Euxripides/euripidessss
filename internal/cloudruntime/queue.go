package cloudruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileInfo Manifest 文件条目（Phase 4 §22）。
type FileInfo struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

// uploadParquetFiles 上传本地 parquet 产物到 leased 目录并返回 Manifest 文件列表。
func uploadParquetFiles(ctx context.Context, store ObjectStore, outDir, leasedDir string) ([]FileInfo, error) {
	var local []string
	_ = filepath.Walk(outDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(p, ".parquet") {
			local = append(local, p)
		}
		return nil
	})
	sort.Strings(local)
	var files []FileInfo
	for _, p := range local {
		body, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(outDir, p)
		if err != nil {
			return nil, err
		}
		remoteKey := leasedDir + "/token_transfers/" + filepath.ToSlash(rel)
		sum := sha256.Sum256(body)
		if err := store.Put(ctx, remoteKey, body); err != nil {
			return nil, err
		}
		files = append(files, FileInfo{
			Path: "token_transfers/" + filepath.ToSlash(rel), Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		})
	}
	return files, nil
}

// buildManifest 构建 Phase 4 §22 Manifest。
func buildManifest(job *Job, files []FileInfo) map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	return map[string]any{
		"schema_version": 1,
		"job_id":         job.ID,
		"chunk_id":       job.ChunkID,
		"provider":       "SQD_CLOUD_EXPORT",
		"chain_id":       56,
		"dataset":        "token_transfer",
		"from_block":     job.FromBlock,
		"to_block":       job.ToBlock,
		"address_count":  len(job.Addresses),
		"addresses":      job.Addresses,
		"row_count":      job.Rows,
		"files":          files,
		"started_at":     job.StartedAt,
		"completed_at":   now,
		"completed":      true,
	}
}

// ParseManifest 解析远端 Manifest。
func ParseManifest(payload []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, err
	}
	return m, nil
}
