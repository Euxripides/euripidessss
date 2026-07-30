package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	datasetwriter "github.com/etl/backend/internal/writer"
)

func TestPlanChunksForFiveAndTenGiB(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	five := PlanChunks(5*gib, DefaultChunkSize)
	ten := PlanChunks(10*gib, DefaultChunkSize)
	if len(five) != 160 || len(ten) != 320 {
		t.Fatalf("unexpected chunk count: 5GiB=%d 10GiB=%d", len(five), len(ten))
	}
	if five[0].Start != 0 || five[len(five)-1].End != 5*gib-1 {
		t.Fatalf("invalid 5GiB plan: first=%+v last=%+v", five[0], five[len(five)-1])
	}
}

func TestDownloadResumesCompletedRangeChunks(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 256*1024)
	etag := `"resume-test"`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("If-Match") != etag {
			t.Errorf("missing If-Match: %q", request.Header.Get("If-Match"))
		}
		header := strings.TrimPrefix(request.Header.Get("Range"), "bytes=")
		parts := strings.Split(header, "-")
		start, _ := strconv.ParseInt(parts[0], 10, 64)
		end, _ := strconv.ParseInt(parts[1], 10, 64)
		writer.Header().Set("ETag", etag)
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(payload[start : end+1])
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "download.parquet")
	checkpointPath := destination + ".download.json"
	source := Source{URL: server.URL, ETag: etag, SizeBytes: int64(len(payload))}
	chunkSize := int64(1 << 20)
	chunks := PlanChunks(source.SizeBytes, chunkSize)
	if err := os.MkdirAll(chunksDir(checkpointPath), 0755); err != nil {
		t.Fatal(err)
	}
	firstPath := chunkPath(checkpointPath, 0)
	if err := os.WriteFile(firstPath, payload[:chunkSize], 0644); err != nil {
		t.Fatal(err)
	}
	checkpoint := Checkpoint{
		SchemaVersion: "1.4.1",
		URL:           source.URL,
		ETag:          etag,
		SizeBytes:     source.SizeBytes,
		ChunkSize:     chunkSize,
		Completed:     map[int]string{0: firstPath},
	}
	if err := datasetwriter.WriteJSONAtomic(checkpointPath, checkpoint); err != nil {
		t.Fatal(err)
	}
	result, err := Download(context.Background(), source, destination, Options{
		Client:         server.Client(),
		Workers:        3,
		ChunkSize:      chunkSize,
		CheckpointPath: checkpointPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResumedChunks != 1 || result.TotalChunks != len(chunks) {
		t.Fatalf("unexpected resume result: %+v", result)
	}
	if requests.Load() != int32(len(chunks)-1) {
		t.Fatalf("expected %d range requests, got %d", len(chunks)-1, requests.Load())
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, payload) {
		t.Fatal("merged file differs from source")
	}
	expected := sha256.Sum256(payload)
	if result.SHA256 != hex.EncodeToString(expected[:]) {
		t.Fatalf("unexpected hash: %s", result.SHA256)
	}
	if _, err := os.Stat(checkpointPath); !os.IsNotExist(err) {
		t.Fatalf("checkpoint was not removed: %v", err)
	}
}
