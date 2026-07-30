package writer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SQLExecutor interface {
	ExecSQLJSON(context.Context, string) ([]map[string]any, error)
}

func ExportParquetCSV(ctx context.Context, executor SQLExecutor, parquetPath, csvPath string) error {
	if executor == nil {
		return fmt.Errorf("CSV 导出引擎不可用")
	}
	if err := os.MkdirAll(filepath.Dir(csvPath), 0755); err != nil {
		return err
	}
	if err := os.Remove(csvPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	query := "COPY (SELECT * FROM read_parquet(" + sqlString(parquetPath) + ")) TO " +
		sqlString(csvPath) + " (FORMAT CSV, HEADER true)"
	if _, err := executor.ExecSQLJSON(ctx, query); err != nil {
		return fmt.Errorf("导出 CSV %s: %w", filepath.Base(csvPath), err)
	}
	return nil
}

func CSVOutputPath(dataRoot, jobID, parquetPath string) string {
	name := strings.TrimSuffix(filepath.Base(parquetPath), filepath.Ext(parquetPath)) + ".csv"
	return filepath.Join(dataRoot, "exports", "job="+jobID, name)
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(value), "'", "''") + "'"
}
