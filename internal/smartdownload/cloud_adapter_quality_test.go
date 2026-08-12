package smartdownload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/cloudruntime"
)

type materializedCloudRuntime struct {
	job cloudruntime.Job
}

func (r *materializedCloudRuntime) SubmitJob(_ context.Context, job cloudruntime.Job) (string, error) {
	r.job.ID = job.ID
	return job.ID, nil
}
func (r *materializedCloudRuntime) JobStatus(string) (cloudruntime.Job, error) { return r.job, nil }
func (r *materializedCloudRuntime) CancelJob(context.Context, string) error    { return nil }
func (r *materializedCloudRuntime) Status() cloudruntime.Status {
	return cloudruntime.Status{State: cloudruntime.WorkerReady, Available: true}
}

func TestCloudAdapterReconcilesManifestAndParquetRows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "part-0.parquet"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := Record{ChainID: 56, BlockNumber: 5, TransactionHash: "0x" + strings.Repeat("a", 64), Dataset: DatasetTokenTransfers, Address: addrA}
	for _, tc := range []struct {
		name     string
		rows     int64
		wantErr  bool
		wantRows int
	}{
		{name: "exact", rows: 1, wantRows: 1},
		{name: "manifest_larger_than_artifact", rows: 2, wantErr: true},
		{name: "manifest_zero_but_artifact_nonempty", rows: 0, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := &materializedCloudRuntime{job: cloudruntime.Job{State: "done", Rows: tc.rows, OutputDir: dir}}
			adapter := NewSQDCloudAdapter(runtime)
			adapter.pollInterval = time.Millisecond
			adapter.SetResultReader(func(context.Context, string) ([]Record, error) { return []Record{record}, nil })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := adapter.ExecuteRange(ctx, RangeRequest{
				DatasetJobID: "dataset", Address: addrA, Dataset: DatasetTokenTransfers,
				ChainKey: "bsc", ChainID: 56, FromBlock: 1, ToBlock: 10,
			})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "\u884c\u6570\u5bf9\u8d26\u5931\u8d25") {
					t.Fatalf("err=%v, want row reconciliation failure", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Records) != tc.wantRows || result.CompletedTo != 10 {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
