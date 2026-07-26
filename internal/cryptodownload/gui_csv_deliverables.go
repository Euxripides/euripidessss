package cryptodownload

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type guiRawCSVDeliverable struct {
	label   string
	headers []string
	records []map[string]string
}

func writeGUIRawCSVDeliverables(cfg Config, data ExportData) ([]string, error) {
	dir := filepath.Dir(absOrClean(cfg.Out))
	address := sanitizeFilePart(strings.ToLower(strings.TrimSpace(cfg.Address)))
	if address == "" {
		address = "address"
	}
	chain := guiRawCSVChain(cfg)
	data = finalizeResumedRawCSV(data, cfg.Address, chain)
	rawItems := []guiRawCSVDeliverable{
		{label: "交易记录", headers: data.RawTxHeaders, records: data.RawTransactions},
		{label: "代币转账", headers: data.RawTokenHeaders, records: data.RawTokenTransfers},
	}
	files := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		if len(item.records) == 0 {
			continue
		}
		headers := item.headers
		if len(headers) == 0 {
			headers = sortedRawCSVHeaders(item.records)
		}
		path := filepath.Join(dir, fmt.Sprintf("%s_%s_%s.csv", chain, item.label, address))
		if err := writeGUIRawCSV(path, headers, item.records); err != nil {
			return nil, fmt.Errorf("write %s csv: %w", item.label, err)
		}
		files = append(files, path)
	}
	if len(data.RawTransactions) == 0 && len(data.Transactions) > 0 {
		path := filepath.Join(dir, fmt.Sprintf("%s_%s_%s.csv", chain, "交易记录", address))
		if err := writeGUIMappedCSV(path, transactionHeaders, data.Transactions); err != nil {
			return nil, fmt.Errorf("write %s csv: %w", "交易记录", err)
		}
		files = append(files, path)
	}
	if len(data.RawTokenTransfers) == 0 && len(data.TokenTransfers) > 0 {
		path := filepath.Join(dir, fmt.Sprintf("%s_%s_%s.csv", chain, "代币转账", address))
		if err := writeGUIMappedCSV(path, transactionHeaders, data.TokenTransfers); err != nil {
			return nil, fmt.Errorf("write %s csv: %w", "代币转账", err)
		}
		files = append(files, path)
	}
	return files, nil
}

func guiRawCSVChain(cfg Config) string {
	if len(cfg.Chains) > 0 {
		if chain := sanitizeFilePart(strings.ToUpper(strings.TrimSpace(cfg.Chains[0]))); chain != "" {
			return chain
		}
	}
	return "CSV"
}

func sortedRawCSVHeaders(records []map[string]string) []string {
	seen := map[string]bool{}
	for _, record := range records {
		for key := range record {
			seen[key] = true
		}
	}
	headers := make([]string, 0, len(seen))
	for key := range seen {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	return headers
}

func writeGUIRawCSV(path string, headers []string, records []map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(absOrClean(path)), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, record := range records {
		row := make([]string, 0, len(headers))
		for _, header := range headers {
			row = append(row, sanitizeExcelString(strings.TrimSpace(record[header])))
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func writeGUIMappedCSV(path string, columns []Column, records []map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(absOrClean(path)), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	headers := make([]string, 0, len(columns))
	for _, column := range columns {
		headers = append(headers, column.Title)
	}
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, record := range records {
		row := make([]string, 0, len(columns))
		for _, column := range columns {
			row = append(row, sanitizeExcelString(strings.TrimSpace(toString(record[column.Key]))))
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}
