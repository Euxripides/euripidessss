package parser

import (
	"testing"
)

func TestNormalizeHeader(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{"  交易时间 ", "交易时间"},
		{"\ufeff交易金额", "交易金额"},
		{"   ", ""},
		{nil, ""},
	}
	for _, tt := range tests {
		got := NormalizeHeader(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeHeader(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToNumber(t *testing.T) {
	tests := []struct {
		input interface{}
		want  float64
	}{
		{"100.00", 100.00},
		{"￥200.50", 200.50},
		{"¥300", 300},
		{"1,234.56", 1234.56},
		{nil, 0},
		{"abc", 0},
	}
	for _, tt := range tests {
		got := ToNumber(tt.input)
		if got != tt.want {
			t.Errorf("ToNumber(%v) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeDatetime(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{"2024-01-01 12:00:00", "2024-01-01 12:00:00"},
		{"20240101", "2024-01-01 00:00:00"},
		{"20240101101010", "2024-01-01 10:10:10"},
		{"2024/01/02", "2024-01-02 00:00:00"},
		{nil, ""},
	}
	for _, tt := range tests {
		got := NormalizeDatetime(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeDatetime(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeDirection(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{"D", "出"},
		{"借", "出"},
		{"支出", "出"},
		{"C", "进"},
		{"贷", "进"},
		{"收入", "进"},
		{nil, ""},
	}
	for _, tt := range tests {
		got := NormalizeDirection(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeDirection(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSupportedSuffixes(t *testing.T) {
	expected := []string{".csv", ".tsv", ".txt", ".xlsx", ".xlsm", ".xls"}
	for _, ext := range expected {
		if !SupportedSuffixes[ext] {
			t.Errorf("expected %s to be supported", ext)
		}
	}
}

func TestCleanAccountNumber(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{"6222021234567890", "6222021234567890"},
		{"CNY-6222021234567890", "6222021234567890"},
		{"USD-622202", "622202"},
		{nil, ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := CleanAccountNumber(tt.input)
		if got != tt.want {
			t.Errorf("CleanAccountNumber(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFindColumn(t *testing.T) {
	headers := []string{"交易时间", "交易金额", "收付标志"}
	idx := FindColumn(headers, []string{"金额", "交易金额"})
	if idx != 1 {
		t.Errorf("FindColumn should find index 1, got %d", idx)
	}
	idx = FindColumn(headers, []string{"不存在的列"})
	if idx != -1 {
		t.Errorf("FindColumn should return -1 for missing column, got %d", idx)
	}
}

func TestRound2(t *testing.T) {
	if v := Round2(100.123); v != 100.12 {
		t.Errorf("Round2(100.123) = %f, want 100.12", v)
	}
	if v := Round2(100.125); v != 100.13 {
		t.Errorf("Round2(100.125) = %f, want 100.13", v)
	}
}

func TestTrimRows(t *testing.T) {
	rows := [][]string{
		{"a", "b", ""},
		{"c", "", ""},
	}
	result := TrimRows(rows)
	if len(result[0]) != 2 || result[0][0] != "a" {
		t.Errorf("TrimRows failed for row 0")
	}
	if len(result[1]) != 1 || result[1][0] != "c" {
		t.Errorf("TrimRows failed for row 1")
	}
}
