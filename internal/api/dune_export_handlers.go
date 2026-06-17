package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

type duneExportRequest struct {
	ExecutionID         string `json:"execution_id"`
	APIKey              string `json:"api_key"`
	Cookie              string `json:"cookie"`
	QueryID             int64  `json:"query_id"`
	Scope               string `json:"scope"`
	Offset              int    `json:"offset"`
	Limit               int    `json:"limit"`
	AllowPartialResults bool   `json:"allow_partial_results"`
}

func HandleDuneExportExcel(c *gin.Context) {
	var payload duneExportRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request: " + err.Error()})
		return
	}
	apiKey, _ := resolveDuneAPIKey(payload.APIKey)
	workbook, rowCount, err := buildDuneExcel(c.Request.Context(), apiKey, payload)
	if err != nil {
		writeDuneAPIError(c, err)
		return
	}
	defer workbook.Close()
	var buf bytes.Buffer
	if err := workbook.Write(&buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	filename := fmt.Sprintf("dune_%s_%s.xlsx", safeName(payload.ExecutionID), normalizeExportScope(payload.Scope))
	c.Header("X-Dune-Export-Rows", fmt.Sprintf("%d", rowCount))
	c.DataFromReader(http.StatusOK, int64(buf.Len()), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", bytes.NewReader(buf.Bytes()), map[string]string{
		"Content-Disposition": fmt.Sprintf(`attachment; filename="%s"`, filename),
	})
}

func buildDuneExcel(ctx context.Context, apiKey string, payload duneExportRequest) (*excelize.File, int, error) {
	pageSize := normalizeDuneLimit(payload.Limit)
	if normalizeExportScope(payload.Scope) == "all" {
		pageSize = 1000
	}
	first, err := fetchDunePreviewPage(ctx, apiKey, payload.Cookie, payload.ExecutionID, payload.QueryID, maxInt(payload.Offset, 0), pageSize, payload.AllowPartialResults)
	if err != nil {
		return nil, 0, err
	}
	columns := first.Result.Metadata.ColumnNames
	labels := localizeDuneColumns(ctx, columns)
	file, writer, err := newDuneWorkbook(columns, labels)
	if err != nil {
		return nil, 0, err
	}
	written, err := appendDuneRows(writer, columns, 2, first.Result.Rows)
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if normalizeExportScope(payload.Scope) == "all" {
		next := payload.Offset + written
		total := first.Result.Metadata.TotalRows
		for written < total {
			page, err := fetchDunePreviewPage(ctx, apiKey, payload.Cookie, payload.ExecutionID, payload.QueryID, next, pageSize, payload.AllowPartialResults)
			if err != nil {
				_ = file.Close()
				return nil, 0, err
			}
			count, err := appendDuneRows(writer, columns, written+2, page.Result.Rows)
			if err != nil {
				_ = file.Close()
				return nil, 0, err
			}
			if count == 0 {
				break
			}
			written += count
			next += count
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, written, nil
}

func newDuneWorkbook(columns []string, labels map[string]string) (*excelize.File, *excelize.StreamWriter, error) {
	file := excelize.NewFile()
	sheet := "Dune"
	file.SetSheetName("Sheet1", sheet)
	writer, err := file.NewStreamWriter(sheet)
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	header := make([]interface{}, len(columns))
	for i, column := range columns {
		label := strings.TrimSpace(labels[column])
		if label == "" {
			label = column
		}
		header[i] = label
	}
	if err := writer.SetRow("A1", header); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, writer, nil
}

func appendDuneRows(writer *excelize.StreamWriter, columns []string, startRow int, rows []map[string]interface{}) (int, error) {
	for rowIndex, row := range rows {
		values := make([]interface{}, len(columns))
		for columnIndex, column := range columns {
			values[columnIndex] = row[column]
		}
		cell, err := excelize.CoordinatesToCellName(1, startRow+rowIndex)
		if err != nil {
			return rowIndex, err
		}
		if err := writer.SetRow(cell, values); err != nil {
			return rowIndex, err
		}
	}
	return len(rows), nil
}

func normalizeExportScope(scope string) string {
	if strings.EqualFold(strings.TrimSpace(scope), "all") {
		return "all"
	}
	return "page"
}
