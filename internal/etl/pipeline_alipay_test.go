package etl

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestRunPipelineProcessesGB18030AlipayAccountDetail(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := t.TempDir()
	path := filepath.Join(inputDir, "826648753_账户明细d2_20260517102205_part1.csv")
	csvText := "交易号,商户订单号,交易创建时间,付款时间,最近修改时间,交易来源地,类型,用户信息,交易对方信息,消费名称,金额（元）,收/支,交易状态,支付方式,清算流水号,备注,对应的协查数据\n" +
		"T1,O1,2026-06-29 10:00:00,2026-06-29 10:01:00,2026-06-29 10:02:00,广东,转账,用户A,用户B,测试,10.00,支出,成功,余额,,,\n"
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(csvText))
	if err != nil {
		t.Fatalf("encode gb18030 fixture: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	result, err := RunPipeline(inputDir, outputDir, "gb18030-alipay")
	if err != nil {
		t.Fatalf("run pipeline: %v", err)
	}
	if len(result.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(result.Transactions))
	}
	row := result.Transactions[0]
	if row["交易金额"] != "10.00" || row["收付标志"] != "出" {
		t.Fatalf("unexpected transaction amount=%q direction=%q", row["交易金额"], row["收付标志"])
	}
}
