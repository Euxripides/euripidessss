package parquetdownload

import (
	"encoding/csv"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

var evmAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

func normalizeAddresses(raw string) AddressSummary {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",，;；|", r)
	})
	summary := AddressSummary{Input: len(fields)}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		value := strings.ToLower(strings.TrimSpace(field))
		if !evmAddressPattern.MatchString(value) {
			if value != "" {
				summary.InvalidItems = append(summary.InvalidItems, value)
			}
			continue
		}
		if _, exists := seen[value]; exists {
			summary.Duplicates++
			continue
		}
		seen[value] = struct{}{}
		summary.Addresses = append(summary.Addresses, value)
	}
	sort.Strings(summary.Addresses)
	summary.Valid = len(summary.Addresses)
	summary.Invalid = len(summary.InvalidItems)
	return summary
}

func parseAddressUpload(header *multipart.FileHeader) (UploadAddressResponse, error) {
	file, err := header.Open()
	if err != nil {
		return UploadAddressResponse{}, err
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	var values []string
	switch ext {
	case ".txt":
		content, err := io.ReadAll(io.LimitReader(file, 16<<20))
		if err != nil {
			return UploadAddressResponse{}, err
		}
		values = append(values, string(content))
	case ".csv":
		reader := csv.NewReader(file)
		reader.FieldsPerRecord = -1
		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return UploadAddressResponse{}, fmt.Errorf("读取 CSV: %w", err)
			}
			values = append(values, row...)
		}
	case ".xlsx", ".xlsm":
		workbook, err := excelize.OpenReader(file)
		if err != nil {
			return UploadAddressResponse{}, fmt.Errorf("读取 Excel: %w", err)
		}
		defer workbook.Close()
		for _, sheet := range workbook.GetSheetList() {
			rows, err := workbook.Rows(sheet)
			if err != nil {
				return UploadAddressResponse{}, err
			}
			for rows.Next() {
				row, err := rows.Columns()
				if err != nil {
					rows.Close()
					return UploadAddressResponse{}, err
				}
				values = append(values, row...)
			}
			rows.Close()
		}
	default:
		return UploadAddressResponse{}, errorsUnsupportedAddressFile(ext)
	}
	raw := strings.Join(values, "\n")
	summary := normalizeAddresses(raw)
	return UploadAddressResponse{
		Raw:     strings.Join(summary.Addresses, "\n"),
		Summary: summary,
	}, nil
}

func errorsUnsupportedAddressFile(ext string) error {
	if ext == "" {
		return fmt.Errorf("地址文件缺少扩展名，仅支持 XLSX、CSV、TXT")
	}
	return fmt.Errorf("不支持的地址文件格式 %s，仅支持 XLSX、CSV、TXT", ext)
}
