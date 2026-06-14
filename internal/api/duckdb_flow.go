package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"

	"github.com/etl/backend/internal/analysis/duckdb"
)

const duckDBTablePrefix = "flow_"
const duckDBLoadTimeout = 10 * time.Minute

// ensureSessionDuckDBTable creates a DuckDB table from session files if DuckDB is available
// and the session doesn't already have an analysis table. Returns the table name or empty string.
func ensureSessionDuckDBTable(sessionID, sessionDir string) string {
	if analysisEngine == nil || !analysisEngine.Available() {
		return ""
	}

	tableName := duckDBTablePrefix + sanitizeTableName(sessionID)

	// Check if table already exists via control store
	if controlStore != nil {
		existing, err := controlStore.GetAnalysisTable(sessionID)
		if err == nil && existing != "" {
			return existing
		}
	}

	// Collect CSV/XLSX files from session dir
	var csvFiles []string
	var xlsxSheets []duckdb.ExcelSheetRead

	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		log.Warn().Err(err).Str("session_id", sessionID).Msg("duckdb_read_session_dir_failed")
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(sessionDir, entry.Name())
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".csv", ".tsv", ".txt":
			csvFiles = append(csvFiles, path)
		case ".xlsx", ".xlsm", ".xls":
			sheets := listExcelSheets(path)
			for _, sheet := range sheets {
				xlsxSheets = append(xlsxSheets, duckdb.ExcelSheetRead{
					Path:   path,
					Sheet:  sheet,
					Header: true,
				})
			}
		}
	}

	if len(csvFiles) == 0 && len(xlsxSheets) == 0 {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), duckDBLoadTimeout)
	defer cancel()

	start := time.Now()
	var loadErr error

	if len(csvFiles) > 0 {
		loadErr = analysisEngine.CreateTableFromCSVFiles(ctx, tableName, csvFiles)
	} else if len(xlsxSheets) > 0 {
		loadErr = analysisEngine.CreateTableFromXLSXFiles(ctx, tableName, xlsxSheets)
	}

	if loadErr != nil {
		log.Warn().Err(loadErr).Str("session_id", sessionID).Str("table", tableName).Msg("duckdb_table_create_failed")
		_ = analysisEngine.DropTable(ctx, tableName)
		return ""
	}

	rowCount, _ := analysisEngine.TableRowCount(ctx, tableName)
	log.Info().
		Str("session_id", sessionID).
		Str("table", tableName).
		Int("rows", rowCount).
		Dur("duration", time.Since(start)).
		Msg("duckdb_table_created")

	// Record in SQLite control store
	if controlStore != nil && rowCount > 0 {
		_ = controlStore.UpsertSession(sessionID, "", rowCount, 0, 0, tableName, "loaded")
	}

	return tableName
}

// listExcelSheets returns all sheet names in an Excel file
func listExcelSheets(path string) []string {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	return f.GetSheetList()
}

func getDuckDBAnalysisTable(sessionID string) string {
	if controlStore == nil {
		return ""
	}
	tableName, err := controlStore.GetAnalysisTable(sessionID)
	if err != nil {
		return ""
	}
	return tableName
}

// sanitizeTableName removes characters that are unsafe for DuckDB table names
func sanitizeTableName(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, fmt.Sprintf("%s_%s", duckDBTablePrefix, s))
}

// cleanupOldDuckDBTable removes a DuckDB table for a given session
func cleanupOldDuckDBTable(sessionID string) {
	if analysisEngine == nil || !analysisEngine.Available() {
		return
	}
	tableName := getDuckDBAnalysisTable(sessionID)
	if tableName == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := analysisEngine.DropTable(ctx, tableName); err != nil {
		log.Warn().Err(err).Str("table", tableName).Msg("duckdb_cleanup_drop_failed")
	}
	if controlStore != nil {
		_ = controlStore.DeleteSession(sessionID)
	}
}
