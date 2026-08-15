package smartdownload

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

const (
	fixA = "0xf43ba0b50028b8873fd4d6daac4bb7c4d5523906"
	fixB = "0x64a319e29d72f15cc4030f927e31540e2cd9bfbf"
	fixC = "0x55d398326f99059ff775485246999027b3197955"
)

func xlsxFromSheets(t *testing.T, sheets map[string][][]any) *bytes.Buffer {
	t.Helper()
	f := excelize.NewFile()
	// 删除默认 Sheet1，全部工作表由调用方命名，避免 SetSheetName 与 NewSheet 的命名歧义。
	if err := f.DeleteSheet(f.GetSheetName(0)); err != nil {
		t.Fatal(err)
	}
	for name, rows := range sheets {
		sheet := name
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatalf("NewSheet(%q): %v", name, err)
		}
		for i, row := range rows {
			for j, cell := range row {
				col, err := excelize.CoordinatesToCellName(j+1, i+1)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue(sheet, col, cell); err != nil {
					t.Fatalf("SetCellValue(%q,%q): %v", sheet, col, err)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestImportXLSXMultiSheetFindsAddressColumn(t *testing.T) {
	// Sheet1 说明页（无地址列），Sheet2 有 address 列：含重复与非法值。
	buf := xlsxFromSheets(t, map[string][][]any{
		"Sheet1": {{"说明页"}, {"本表不包含地址列"}, {fixA}},
		"Sheet2": {{"address"}, {fixA}, {strings.ToUpper(fixA)}, {fixB}, {"not-an-address"}},
	})
	result, err := (&Service{}).ImportAddresses("multi.xlsx", buf)
	if err != nil {
		t.Fatalf("多 Sheet XLSX 应识别 Sheet2 地址列: %v", err)
	}
	if result.SelectedColumn != "address" {
		t.Fatalf("selected_column=%q", result.SelectedColumn)
	}
	// 有效 3（fixA/大写 fixA/fixB 均为规范化前有效行），重复 1（大写 fixA），非法 1。
	if len(result.FinalAddresses) != 2 {
		t.Fatalf("final addresses=%v", result.FinalAddresses)
	}
	if result.Valid != 3 || result.Duplicates != 1 || result.Invalid != 1 {
		t.Fatalf("statistics mismatch: %+v", result)
	}
	for _, a := range result.FinalAddresses {
		if !strings.HasPrefix(a, "0x") || a != strings.ToLower(a) {
			t.Fatalf("address not normalized: %q", a)
		}
	}
}

func TestImportXLSXTwoValidSheetsMerge(t *testing.T) {
	buf := xlsxFromSheets(t, map[string][][]any{
		"Sheet1": {{"address"}, {fixA}, {fixC}},
		"Sheet2": {{"wallet"}, {fixB}, {fixA}}, // fixA 跨 Sheet 重复
	})
	result, err := (&Service{}).ImportAddresses("merge.xlsx", buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FinalAddresses) != 3 {
		t.Fatalf("跨 Sheet 应合并去重为 3 个唯一地址: %v", result.FinalAddresses)
	}
	if result.Duplicates != 1 {
		t.Fatalf("跨 Sheet 重复应计入 duplicates=1: %+v", result)
	}
}

func TestImportXLSXAllSheetsWithoutAddressColumnFails(t *testing.T) {
	buf := xlsxFromSheets(t, map[string][][]any{
		"Sheet1": {{"name"}, {"alpha"}},
		"Sheet2": {{"remark"}, {"beta"}},
	})
	_, err := (&Service{}).ImportAddresses("none.xlsx", buf)
	if err == nil || !strings.Contains(err.Error(), "未识别到地址列") {
		t.Fatalf("全部 Sheet 无地址列应明确失败: %v", err)
	}
}

func TestImportXLSXEmptySheetPlusValidSheet(t *testing.T) {
	buf := xlsxFromSheets(t, map[string][][]any{
		"Empty":   {},
		"Sheet2":  {{"address"}, {fixB}},
	})
	result, err := (&Service{}).ImportAddresses("mixed.xlsx", buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FinalAddresses) != 1 || result.FinalAddresses[0] != fixB {
		t.Fatalf("空 Sheet + 有效 Sheet 应导入有效地址: %+v", result)
	}
}

func TestImportXLSXBrokenFileFails(t *testing.T) {
	_, err := (&Service{}).ImportAddresses("broken.xlsx", bytes.NewBufferString("not a zip"))
	if err == nil || !strings.Contains(err.Error(), "读取 XLSX") {
		t.Fatalf("损坏 XLSX 应明确失败: %v", err)
	}
}
