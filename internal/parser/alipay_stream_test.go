package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStreamAlipayFilesEmitsRowsWithoutUnifiedBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "账户明细.csv")
	content := "交易号,商户订单号,交易创建时间,付款时间,最近修改时间,交易来源地,类型,用户信息,交易对方信息,消费名称,金额（元）,收/支,交易状态,备注,对应的协查数据\n" +
		"T1,O1,2024-01-01 10:00:00,,,广东,转账,用户A,用户B,测试,10.50,支出,成功,,\n" +
		"T2,O2,2024-01-02 10:00:00,,,广东,转账,用户A,用户C,测试,20.00,收入,成功,,\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var emitted [][]string
	sources, tableRows, unifiedRows, err := StreamAlipayFiles([]string{path}, "strict", func(row []string) error {
		emitted = append(emitted, append([]string(nil), row...))
		return nil
	})
	if err != nil {
		t.Fatalf("stream alipay: %v", err)
	}
	if len(sources) != 1 || sources[0].Rows != 2 || tableRows["账户明细"] != 2 || unifiedRows != 2 {
		t.Fatalf("unexpected stream metadata: sources=%#v tableRows=%#v unifiedRows=%d", sources, tableRows, unifiedRows)
	}
	if len(emitted) != 2 || emitted[0][colIdx("交易金额")] != "10.50" || emitted[1][colIdx("收付标志")] != "进" {
		t.Fatalf("unexpected emitted rows: %#v", emitted)
	}
	if emitted[0][colIdx("来源表")] == "" {
		t.Fatal("expected source location")
	}
}
