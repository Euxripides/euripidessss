package ledgerimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDecimalScientific(t *testing.T) {
	cases := map[string]string{
		"1.0E-9":    "0.000000001",
		"2.31E-6":   "0.00000231",
		"0.543696":  "0.543696",
		"88.000000": "88",
		"0.0":       "0",
	}
	for in, want := range cases {
		got, ok := normalizeDecimal(in)
		if !ok || got != want {
			t.Errorf("normalizeDecimal(%q) = %q,%v want %q,true", in, got, ok, want)
		}
	}
	if _, ok := normalizeDecimal("abc"); ok {
		t.Errorf("normalizeDecimal(abc) should fail")
	}
}

func TestRawFromDecimal(t *testing.T) {
	cases := []struct {
		value    string
		decimals uint8
		want     string
	}{
		{"0.543696", 6, "543696"},
		{"177.152905", 6, "177152905"},
		{"88.000000", 18, "88000000000000000000"},
		{"0.07257460459383883", 18, "72574604593838830"},
		{"206.68501288686113", 18, "206685012886861130000"},
		{"0.000000001", 18, "1000000000"},
	}
	for _, c := range cases {
		if got := rawFromDecimal(c.value, c.decimals); got != c.want {
			t.Errorf("rawFromDecimal(%q,%d) = %q want %q", c.value, c.decimals, got, c.want)
		}
	}
}

func TestStatusAndMethod(t *testing.T) {
	if statusFromReceipt("0x1") != "SUCCESS" || statusFromReceipt("0x0") != "FAILED" || statusFromReceipt("transfer") != "UNKNOWN" {
		t.Errorf("statusFromReceipt mapping wrong")
	}
	id, name := methodParts("0x76911b5d")
	if id != "0x76911b5d" || name != "" {
		t.Errorf("methodParts id = %q,%q", id, name)
	}
	id, name = methodParts("approve")
	if id != "" || name != "approve" {
		t.Errorf("methodParts name = %q,%q", id, name)
	}
	id, name = methodParts("-")
	if id != "" || name != "" {
		t.Errorf("methodParts dash = %q,%q", id, name)
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseLedger10(t *testing.T) {
	content := "交易哈希,区块高度,本地时间(UTC+8),UTC时间,发送方,接收方,数量,代币符号,代币地址,logIndex\n" +
		"0xABC,57700080,2025/08/15 23:53:00,2025/08/15 15:53:00,0x8BFCc1513C04f1e44c85eBb94aBbB2ef4c20DDcb,0x2da39e15f8b505da62a79eeb1071665445433652,0.543696,FIST,0xc9882def23bc42d53895b8361d0b1edc7570bc6a,590\n"
	path := writeTemp(t, "ledger.csv", content)
	src := Source{Kind: KindLedger10, Path: path, Provider: "SQD_FINALIZED", RangeID: "fist-ledger", Priority: 3}
	var got []TransferRow
	stats, err := parseLedger10(path, "job-test", src, "2026-08-10 00:00:00.000", func(row TransferRow) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.ParsedRows != 1 || stats.Rejected != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	row := got[0]
	if row.TxHash != "0xabc" || row.BlockNumber != 57700080 || row.LogIndex == nil || *row.LogIndex != 590 {
		t.Fatalf("row = %+v", row)
	}
	if row.FromAddress != "0x8bfcc1513c04f1e44c85ebb94abbb2ef4c20ddcb" {
		t.Fatalf("from = %q", row.FromAddress)
	}
	if row.RawValue != "543696" || row.ValueDecimal != "0.543696" || row.TokenDecimals != 6 {
		t.Fatalf("amount fields = %+v", row)
	}
	if row.BlockTime != "2025-08-15 15:53:00.000" {
		t.Fatalf("block time = %q", row.BlockTime)
	}
	if stats.SHA256 == "" {
		t.Fatalf("sha256 missing")
	}
}

func TestParseTransfer9AndSynthetic(t *testing.T) {
	content := "交易哈希,区块高度,本地时间(UTC+8),UTC时间,发送方,接收方,数量,代币符号,代币地址,_extra_1\n" +
		"0x7858b834d06420d186a742128041878b51561180ef18f20a684e25d6b6f95599,113610339,2026/08/02 23:33:06,2026/08/02 15:33:06,0x73551c9cd2400f2c7d3389ec914154d489dc1854,0x193d5f17b9c370f494cb320c2d399e92d6f1602a,0.07257460459383883,FXH,0xa1210bd712717baa2a04f95fb8eaee36a65d97f3,\n" +
		"0x7858b834d06420d186a742128041878b51561180ef18f20a684e25d6b6f95599,113610339,2026/08/02 23:33:06,2026/08/02 15:33:06,0x73551c9cd2400f2c7d3389ec914154d489dc1854,0x193d5f17b9c370f494cb320c2d399e92d6f1602a,0.02902984183753553,FXH,0xa1210bd712717baa2a04f95fb8eaee36a65d97f3,\n"
	path := writeTemp(t, "transfers.csv", content)
	src := Source{Kind: KindTransfer9, Path: path, Provider: "ADDRESS_CSV_EXPORT", RangeID: "address-transfer-csv", Priority: 2}
	var got []TransferRow
	stats, err := parseTransfer9(path, "job-test", src, "2026-08-10 00:00:00.000", func(row TransferRow) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.ParsedRows != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if got[0].LogIndex != nil {
		t.Fatalf("log index should be nil before synthetic assignment")
	}
	assignSyntheticLogIndices(got)
	if got[0].LogIndex == nil || got[1].LogIndex == nil {
		t.Fatalf("synthetic indices not assigned")
	}
	if *got[0].LogIndex == *got[1].LogIndex {
		t.Fatalf("synthetic indices collide: %d", *got[0].LogIndex)
	}
	if got[0].TokenAddress != "0xa1210bd712717baa2a04f95fb8eaee36a65d97f3" {
		t.Fatalf("token = %q", got[0].TokenAddress)
	}
	if got[0].RawValue != "" || got[0].TokenDecimals != 0 {
		t.Fatalf("unknown token should keep empty raw/0 decimals")
	}
}

func TestParseTx11(t *testing.T) {
	content := "交易哈希,区块高度,本地时间(UTC+8),UTC时间,发送方,接收方,数量,手续费,交易状态,_extra_1,_extra_2\n" +
		"0xa2656b48733fde79a0047218d06b8ac170afda44323825ba26100e8216a98f47,112357031,2026/07/27 10:48:01,2026/07/27 02:48:01,0x193d5f17b9c370f494cb320c2d399e92d6f1602a,0xf350972357cfd2e53580e1e3a8cf4d043e921900,0.0,0.00001918732855,0x7bf689f4,SUCCESS,\n"
	path := writeTemp(t, "tx.csv", content)
	src := Source{Kind: KindTx11, Path: path, Provider: "ADDRESS_CSV_EXPORT", RangeID: "address-tx-csv", Priority: 2}
	var got []TxRow
	stats, err := parseTx11(path, "job-test", src, "2026-08-10 00:00:00.000", func(row TxRow) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.ParsedRows != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	row := got[0]
	if row.MethodID != "0x7bf689f4" || row.Status != "SUCCESS" || row.StatusSource != "RECEIPT" {
		t.Fatalf("row = %+v", row)
	}
	if row.FeeNative != "0.00001918732855" {
		t.Fatalf("fee = %q", row.FeeNative)
	}
}

func TestDiscoverSourcesSkipsWalletDuplicates(t *testing.T) {
	root := t.TempDir()
	flows := filepath.Join(root, "flows")
	ledger := filepath.Join(root, "ledger")
	for _, d := range []string{flows, ledger} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p1 := filepath.Join(flows, "p1+p0", "0xabc")
	wallet := filepath.Join(flows, "wallet_export_x", "0xabc")
	for _, d := range []string{p1, wallet} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTempDir := func(dir, name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("a,b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	writeTempDir(p1, "BSC_代币转账_0xabc.csv")
	writeTempDir(p1, "BSC_交易记录_0xabc.csv")
	writeTempDir(wallet, "BSC_代币转账_0xabc.csv")
	writeTempDir(wallet, "BSC_交易记录_0xabc.csv")
	writeTempDir(flows, "0xabc.xlsx")
	writeTempDir(flows, "下载情况.xlsx")

	sources, err := DiscoverSources(Config{LedgerRoot: ledger, FlowsRoot: flows})
	if err != nil {
		t.Fatal(err)
	}
	var transfer9, transferOK, tx11, txOK, tx9 int
	for _, s := range sources {
		switch s.Kind {
		case KindTransfer9:
			transfer9++
		case KindTransferOK:
			transferOK++
		case KindTx11:
			tx11++
		case KindTxOK:
			txOK++
		case KindTx9:
			tx9++
		}
	}
	if transfer9 != 1 || transferOK != 1 || tx11 != 1 || txOK != 1 || tx9 != 1 {
		t.Fatalf("source counts: transfer9=%d transferOK=%d tx11=%d txOK=%d tx9=%d", transfer9, transferOK, tx11, txOK, tx9)
	}
}
