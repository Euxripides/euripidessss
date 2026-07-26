package parser

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestReadCSVRowsLimitedDecodesGB18030(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "账户明细.csv")
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("交易号,商户订单号,交易创建时间,金额（元）,收/支\nT1,O1,2026-06-29,10.00,收入\n"))
	if err != nil {
		t.Fatalf("encode gb18030 fixture: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	rows, err := ReadCSVRowsLimited(path, 2)
	if err != nil {
		t.Fatalf("read gb18030 csv: %v", err)
	}
	if got := rows[0][0]; got != "交易号" {
		t.Fatalf("expected GB18030 header 交易号, got %q", got)
	}
	if got := rows[0][1]; got != "商户订单号" {
		t.Fatalf("expected GB18030 header 商户订单号, got %q", got)
	}
}
