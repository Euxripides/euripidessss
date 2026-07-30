package parquetdownload

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	datasetwriter "github.com/etl/backend/internal/writer"
)

func (m *Manager) exportDatasetCSVs(
	ctx context.Context,
	jobID string,
	settings Settings,
	parquetPaths []string,
) ([]string, error) {
	if m.engine == nil || !m.engine.Available() {
		return nil, fmt.Errorf("CSV 导出已启用，但 DuckDB 不可用")
	}
	outputs := make([]string, 0, len(parquetPaths))
	seen := map[string]int{}
	for _, parquetPath := range parquetPaths {
		if !strings.EqualFold(filepath.Ext(parquetPath), ".parquet") {
			continue
		}
		csvPath := datasetwriter.CSVOutputPath(settings.DataRoot, jobID, parquetPath)
		key := strings.ToLower(csvPath)
		if count := seen[key]; count > 0 {
			base := strings.TrimSuffix(csvPath, filepath.Ext(csvPath))
			csvPath = fmt.Sprintf("%s-%d.csv", base, count+1)
		}
		seen[key]++
		if err := datasetwriter.ExportParquetCSV(ctx, m.engine, parquetPath, csvPath); err != nil {
			return nil, err
		}
		outputs = append(outputs, csvPath)
	}
	return outputs, nil
}
