package api

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/rules"
)

const maxCacheRowsPerSession = 5000000

type cachedFile struct {
	Headers []string
	Rows    [][]string
}

type sessionRowCache struct {
	mu          sync.RWMutex
	files       []cachedFile
	totalRows   int
	ColumnOrder []string
}

var (
	rowCacheMu sync.RWMutex
	rowCache   = make(map[string]*sessionRowCache)
)

// readSessionDataWithCache reads session files once, builds both TransactionRows
// and the edge detail cache simultaneously, eliminating double I/O.
func readSessionDataWithCache(sessionDir, sessionID string, mapping flowColumnMapping, dirMap map[string]string) []model.TransactionRow {
	rowCacheMu.Lock()
	if _, ok := rowCache[sessionID]; ok {
		rowCacheMu.Unlock()
		return readSessionData(sessionDir, mapping, dirMap)
	}

	normalizedDirMap := make(map[string]string, len(dirMap))
	for k, v := range rules.LoadDirectionAliases() {
		normalizedDirMap[strings.TrimSpace(k)] = v
		normalizedDirMap[parser.NormalizeHeader(k)] = v
	}
	for k, v := range dirMap {
		normalizedDirMap[strings.TrimSpace(k)] = v
		normalizedDirMap[parser.NormalizeHeader(k)] = v
	}
	mapping = normalizeFlowColumnMapping(mapping)

	cache := &sessionRowCache{}
	var totalRows int
	var txns []model.TransactionRow

	filepath.Walk(sessionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !parser.SupportedSuffixes[ext] {
			return nil
		}

		var rows [][]string
		if parser.ExcelSuffixes[ext] {
			sheets, err := parser.ReadExcelFile(path)
			if err != nil {
				return nil
			}
			for _, s := range sheets {
				rows = append(rows, s...)
			}
		} else {
			var readErr error
			rows, readErr = parser.ReadCSVFile(path)
			if readErr != nil {
				return nil
			}
		}

		if len(rows) < 2 {
			return nil
		}

		headers := rows[0]
		colIdx := make(map[string]int)
		for i, h := range headers {
			colIdx[parser.NormalizeHeader(h)] = i
		}
		dataRows := rows[1:]

		for _, row := range dataRows {
			txns = append(txns, transactionFromMappedRow(row, colIdx, mapping, normalizedDirMap))
		}

		if totalRows+len(dataRows) <= maxCacheRowsPerSession {
			cache.files = append(cache.files, cachedFile{Headers: headers, Rows: dataRows})
			if len(cache.ColumnOrder) == 0 {
				normHeaders := make([]string, len(headers))
				for i, h := range headers {
					normHeaders[i] = parser.NormalizeHeader(h)
				}
				cache.ColumnOrder = normHeaders
			}
			totalRows += len(dataRows)
		}

		return nil
	})

	cache.totalRows = totalRows
	if cache.totalRows > 0 {
		rowCache[sessionID] = cache
	}
	rowCacheMu.Unlock()

	return txns
}

func getCachedFiles(sessionID string) *sessionRowCache {
	rowCacheMu.RLock()
	defer rowCacheMu.RUnlock()
	return rowCache[sessionID]
}

func getCachedColumnOrder(sessionID string) []string {
	rowCacheMu.RLock()
	defer rowCacheMu.RUnlock()
	if cache, ok := rowCache[sessionID]; ok {
		return cache.ColumnOrder
	}
	return nil
}

func processCachedRows(cache *sessionRowCache, p EdgeDetailPayload) []map[string]interface{} {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	mapping := normalizeFlowColumnMapping(flowColumnMapping{
		SourceCol:     p.SourceColumn,
		SourceAccount: p.SourceAccountColumn,
		SourceName:    p.SourceNameColumn,
		SourceID:      p.SourceIDColumn,
		SourceLabel:   p.SourceLabelColumn,
		TargetCol:     p.TargetColumn,
		TargetCard:    p.TargetCardColumn,
		TargetName:    p.TargetNameColumn,
		TargetID:      p.TargetIDColumn,
		TargetLabel:   p.TargetLabelColumn,
		Amount:        p.AmountColumn,
		Time:          p.TimeColumn,
		Direction:     p.DirectionColumn,
		Serial:        p.SerialColumn,
		Summary:       p.SummaryColumn,
		Remark:        p.RemarkColumn,
	})
	filterPayload := edgeDetailFilterPayload(p)
	dirMap := make(map[string]string)
	for k, v := range rules.LoadDirectionAliases() {
		dirMap[strings.TrimSpace(k)] = v
		dirMap[parser.NormalizeHeader(k)] = v
	}

	var result []map[string]interface{}
	for _, cf := range cache.files {
		colIdx := make(map[string]int)
		for i, h := range cf.Headers {
			colIdx[parser.NormalizeHeader(h)] = i
		}
		for _, row := range cf.Rows {
			txn := transactionFromMappedRow(row, colIdx, mapping, dirMap)
			if !transactionMatchesFilters(txn, filterPayload) {
				continue
			}
			source, target := flowEndpointsForTransaction(txn)
			if source != p.Source || target != p.Target {
				continue
			}
			m := make(map[string]interface{})
			for j, h := range cf.Headers {
				if j < len(row) {
					m[parser.NormalizeHeader(h)] = row[j]
				}
			}
			m["流向源"] = source
			m["流向目标"] = target
			result = append(result, m)
		}
	}
	return result
}
