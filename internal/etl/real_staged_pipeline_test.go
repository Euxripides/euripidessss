package etl

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealStagedPipeline is opt-in because it processes the caller's complete
// forensic dataset. It provides a repeatable validation entrypoint without
// embedding any evidence path in source control.
func TestRealStagedPipeline(t *testing.T) {
	inputDir := os.Getenv("ETL_REAL_INPUT_DIR")
	outputDir := os.Getenv("ETL_REAL_OUTPUT_DIR")
	if inputDir == "" || outputDir == "" {
		t.Skip("set ETL_REAL_INPUT_DIR and ETL_REAL_OUTPUT_DIR to run")
	}
	var events []ProgressEvent
	result, err := RunPipelineWithOptions(inputDir, outputDir, "real-staged-validation", PipelineOptions{
		UnifySources: true,
		Progress: func(event ProgressEvent) {
			events = append(events, event)
			if event.Status == "running" && event.Current > 0 && event.Current%100000 == 0 {
				t.Logf("%s: %d/%d %s", event.Name, event.Current, event.Total, event.Unit)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.RowsOut == 0 {
		t.Fatal("real staged pipeline produced no rows")
	}
	for _, artifact := range result.Artifacts {
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("artifact missing: %s: %v", artifact.Path, err)
		}
	}
	for _, required := range []string{"raw-alipay", "raw-wechat", "raw-bank", "unified-alipay", "unified-wechat", "unified-bank", "final-csv", "final-xlsx"} {
		if findArtifact(result.Artifacts, required) == nil {
			t.Fatalf("missing required artifact %s", required)
		}
	}
	if filepath.Ext(result.OutputPath) != ".xlsx" {
		t.Fatalf("expected compatibility xlsx output, got %s", result.OutputPath)
	}
	t.Logf("rows_in=%d rows_out=%d duplicates=%d artifacts=%d output=%s events=%d",
		result.Report.RowsIn, result.Report.RowsOut, result.Report.RemovedDuplicates,
		len(result.Artifacts), result.OutputPath, len(events))
}
