package parquetdownload

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	datasetwriter "github.com/etl/backend/internal/writer"
)

func TestFinalizerKeepsAPIAndManifestConsistent(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "warehouse", "result.parquet")
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("PAR1stablePAR1"), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	job := &Job{
		SchemaVersion:   datasetwriter.ManifestSchemaVersion,
		ID:              "finalizer-test",
		ChainKey:        "bsc",
		ChainID:         56,
		Status:          StatusRunning,
		Stage:           "output",
		Progress:        95,
		SelectedSources: []string{"transactions", "logs", "traces"},
		Outputs:         []string{output},
		Stages:          defaultStages(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	for index := range job.Stages {
		job.Stages[index].Status = StatusDone
		job.Stages[index].Progress = 100
	}
	manager := testManager(root, job)
	manager.finishJob(job.ID, StatusDone, nil)

	apiJob, err := manager.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if apiJob.Status != StatusDone || apiJob.Stage != "done" || !apiJob.Manifest.Consistent {
		t.Fatalf("unexpected API job: %+v", apiJob)
	}
	if len(apiJob.Checksums) != 1 || apiJob.Checksums[0].Path != output {
		t.Fatalf("checksums missing: %+v", apiJob.Checksums)
	}
	content, err := os.ReadFile(apiJob.Manifest.Path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Job
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != apiJob.Status || manifest.Stage != apiJob.Stage ||
		manifest.Progress != apiJob.Progress || manifest.FinishedAt == nil ||
		manifest.SchemaVersion != datasetwriter.ManifestSchemaVersion {
		t.Fatalf("API/manifest mismatch: api=%+v manifest=%+v", apiJob, manifest)
	}
}

func TestCancelSettlesStagesAndWritesFinalManifest(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	job := &Job{
		SchemaVersion:   datasetwriter.ManifestSchemaVersion,
		ID:              "cancel-test",
		ChainKey:        "bsc",
		ChainID:         56,
		Status:          StatusRunning,
		Stage:           "download",
		Progress:        42,
		SelectedSources: []string{"transactions"},
		Stages:          defaultStages(),
		Files: []*FileTask{{
			Status: "downloading",
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	setStage(job, "download", StatusRunning, 42, "下载中")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := testManager(root, job)
	manager.cancels[job.ID] = cancel
	if _, err := manager.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("worker context was not canceled")
	}
	canceling, _ := manager.Get(job.ID)
	if canceling.Status != StatusCanceling || !canceling.CancellationRequested {
		t.Fatalf("cancel request did not enter canceling state: %+v", canceling)
	}
	manager.finishJob(job.ID, StatusCanceled, errors.New("任务已取消"))
	final, _ := manager.Get(job.ID)
	if final.Status != StatusCanceled || final.Stage != "canceled" || !final.Manifest.Consistent {
		t.Fatalf("cancel did not finalize: %+v", final)
	}
	for _, stage := range final.Stages {
		if stage.Status == StatusRunning || stage.Status == StatusQueued || stage.Status == StatusCanceling {
			t.Fatalf("terminal job contains active stage: %+v", stage)
		}
	}
	if final.Files[0].Status != StatusCanceled {
		t.Fatalf("file task did not settle: %+v", final.Files[0])
	}
	var manifest Job
	content, err := os.ReadFile(final.Manifest.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != final.Status || manifest.Stages[2].Status == StatusRunning {
		t.Fatalf("canceled manifest mismatch: %+v", manifest)
	}
}

func TestSQDParquetOutputsExportCSV(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("bundled DuckDB executable is Windows-only")
	}
	root := repositoryRoot(t)
	exe := filepath.Join(root, "tools", "duckdb", "duckdb.exe")
	if _, err := os.Stat(exe); err != nil {
		t.Skip("bundled DuckDB executable not available")
	}
	dataRoot := t.TempDir()
	engine := duckdb.Open(root, dataRoot, duckdb.AnalyticsConfig{
		DuckDBPath:     exe,
		DuckDBDatabase: filepath.Join(dataRoot, "test.duckdb"),
	})
	parquetPath := filepath.Join(dataRoot, "warehouse", "traces", "trace.parquet")
	if err := os.MkdirAll(filepath.Dir(parquetPath), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecSQL(context.Background(),
		"COPY (SELECT 1 AS id, 'trace' AS source) TO "+sqlString(parquetPath)+" (FORMAT PARQUET)"); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{engine: engine}
	outputs, err := manager.exportDatasetCSVs(context.Background(), "csv-test", Settings{DataRoot: dataRoot}, []string{parquetPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 {
		t.Fatalf("unexpected CSV outputs: %v", outputs)
	}
	content, err := os.ReadFile(outputs[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "id,source\n1,trace\n" {
		t.Fatalf("unexpected CSV: %q", content)
	}
}

func TestCoverageDoesNotTreatSkippedSelectedSourceAsComplete(t *testing.T) {
	job := &Job{
		ID:              "coverage-test",
		ChainID:         56,
		Status:          StatusFailed,
		SelectedSources: []string{"logs", "traces"},
		Stages:          defaultStages(),
	}
	setStage(job, "logs", StatusDone, 100, "")
	setStage(job, "traces", StatusSkipped, 0, "前序阶段失败")
	updateCoverage(job)
	if job.Coverage.LogsStatus != CoverageComplete ||
		job.Coverage.TraceStatus != CoveragePartial ||
		job.Coverage.TransactionsStatus != CoverageNotSelected ||
		job.Coverage.CoveragePercent != 33.33 {
		t.Fatalf("unexpected coverage: %+v", job.Coverage)
	}
}

func testManager(root string, job *Job) *Manager {
	settings := Settings{DataRoot: root}
	updateCoverage(job)
	return &Manager{
		settings:    settings,
		jobs:        map[string]*Job{job.ID: job},
		cancels:     map[string]context.CancelFunc{},
		lastPersist: map[string]time.Time{},
	}
}
