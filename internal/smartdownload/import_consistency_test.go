package smartdownload

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestAddressImportFormatsHaveIdenticalStatistics(t *testing.T) {
	const (
		first  = "0x1111111111111111111111111111111111111111"
		second = "0x2222222222222222222222222222222222222222"
	)
	rows := []string{first, second, first, "not-an-address"}
	txt := "address\n" + strings.Join(rows, "\n") + "\n\n"
	csv := "address\n" + strings.Join(rows, "\n") + "\n"

	book := excelize.NewFile()
	sheet := book.GetSheetName(0)
	if err := book.SetCellValue(sheet, "A1", "address"); err != nil {
		t.Fatal(err)
	}
	for index, value := range rows {
		if err := book.SetCellValue(sheet, fmt.Sprintf("A%d", index+2), value); err != nil {
			t.Fatal(err)
		}
	}
	var xlsx bytes.Buffer
	if err := book.Write(&xlsx); err != nil {
		t.Fatal(err)
	}
	if err := book.Close(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		filename string
		payload  []byte
	}{
		{name: "txt", filename: "addresses.txt", payload: []byte(txt)},
		{name: "csv", filename: "addresses.csv", payload: []byte(csv)},
		{name: "xlsx", filename: "addresses.xlsx", payload: xlsx.Bytes()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := (&Service{}).ImportAddresses(tc.filename, bytes.NewReader(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != 4 || result.Valid != 3 || result.Duplicates != 1 || result.Invalid != 1 {
				t.Fatalf("统计不一致: rows=%d valid=%d duplicates=%d invalid=%d", result.Rows, result.Valid, result.Duplicates, result.Invalid)
			}
			if result.SelectedColumn != "address" || len(result.DetectedColumns) != 1 {
				t.Fatalf("地址列识别不一致: selected=%q columns=%+v", result.SelectedColumn, result.DetectedColumns)
			}
			column := result.DetectedColumns[0]
			if column.Valid != 3 || column.NonEmpty != 4 || column.Confidence != 0.75 {
				t.Fatalf("列统计不一致: %+v", column)
			}
			if len(result.FinalAddresses) != 2 || result.FinalAddresses[0] != first || result.FinalAddresses[1] != second {
				t.Fatalf("最终地址集合不一致: %v", result.FinalAddresses)
			}
		})
	}
}
