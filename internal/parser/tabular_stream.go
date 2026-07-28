package parser

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ReadTabularPreviews reads at most maxRows from every sheet without loading
// an entire workbook or delimited file into memory.
func ReadTabularPreviews(path string, maxRows int) (map[string][][]string, error) {
	if maxRows <= 0 {
		maxRows = 40
	}
	if isDelimitedFile(path) {
		rows, err := ReadCSVRowsLimited(path, maxRows)
		if err != nil {
			return nil, err
		}
		return map[string][][]string{"": rows}, nil
	}

	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()
	result := make(map[string][][]string)
	for _, sheet := range f.GetSheetList() {
		iter, err := f.Rows(sheet)
		if err != nil {
			continue
		}
		var rows [][]string
		for iter.Next() && len(rows) < maxRows {
			row, rowErr := iter.Columns()
			if rowErr != nil {
				iter.Close()
				return nil, rowErr
			}
			rows = append(rows, row)
		}
		iter.Close()
		result[sheet] = rows
	}
	return result, nil
}

// StreamTabularFile emits every row while keeping memory bounded.
func StreamTabularFile(path string, emit func(sheet string, rowIndex int, row []string) error) error {
	if isDelimitedFile(path) {
		_, encoding, sep, err := readCSVPreview(path, 40)
		if err != nil {
			return err
		}
		reader, file, err := openCSVReader(path, sep, encoding)
		if err != nil {
			return err
		}
		defer file.Close()
		for rowIndex := 0; ; rowIndex++ {
			row, readErr := reader.Read()
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			if err := emit("", rowIndex, row); err != nil {
				return err
			}
		}
	}

	ext := strings.ToLower(filepath.Ext(path))
	if !ExcelSuffixes[ext] {
		return fmt.Errorf("unsupported tabular file: %s", filepath.Base(path))
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()
	for _, sheet := range f.GetSheetList() {
		iter, err := f.Rows(sheet)
		if err != nil {
			continue
		}
		rowIndex := 0
		for iter.Next() {
			row, rowErr := iter.Columns()
			if rowErr != nil {
				iter.Close()
				return rowErr
			}
			if err := emit(sheet, rowIndex, row); err != nil {
				iter.Close()
				return err
			}
			rowIndex++
		}
		iter.Close()
	}
	return nil
}
