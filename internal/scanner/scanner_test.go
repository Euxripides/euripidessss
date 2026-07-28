package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestScanDirectoryClassifiesAlipayGB18030AccountDetail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "826648753_账户明细d2_20260517102205_part1.csv")
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("交易号,商户订单号,交易创建时间,付款时间,最近修改时间,交易来源地,类型,用户信息,交易对方信息,消费名称,金额（元）,收/支,交易状态,支付方式,清算流水号,备注,对应的协查数据\nT1,O1,2026-06-29 10:00:00,2026-06-29 10:01:00,2026-06-29 10:02:00,广东,转账,用户A,用户B,测试,10.00,支出,成功,余额,,,\n"))
	if err != nil {
		t.Fatalf("encode gb18030 fixture: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	scan, err := ScanDirectory(dir)
	if err != nil {
		t.Fatalf("scan directory: %v", err)
	}
	if len(scan.Transactions) != 1 {
		t.Fatalf("expected 1 transaction candidate, got %d unknown=%d", len(scan.Transactions), len(scan.Unknown))
	}
	got := scan.Transactions[0]
	if got.Provider != "支付宝" {
		t.Fatalf("expected provider 支付宝, got %q", got.Provider)
	}
}

func TestDetectProviderByColumnsPrefersStrongBankSignature(t *testing.T) {
	columns := []string{
		"交易卡号", "交易账号", "交易户名", "交易证件号码", "交易方开户行",
		"交易时间", "交易金额", "交易余额", "收付标志", "交易对手账卡号",
		"对手户名", "对手开户银行", "摘要说明", "交易币种", "交易流水号",
	}
	if got := detectProviderByColumns(columns); got != "银行" {
		t.Fatalf("detectProviderByColumns() = %q, want 银行", got)
	}
}
