package etl

import (
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/model"
)

func TestDedupIdentityPreservesLegitimateSameSecondTransactions(t *testing.T) {
	base := model.TransactionRow{
		"来源类型": "微信", "交易时间": "2026-07-29 10:00:00", "交易金额": "100.00",
		"收付标志": "出", "交易账号": "A", "交易对手账卡号": "B",
	}
	first := cloneTransaction(base)
	first["来源记录ID"] = "source-row-1"
	second := cloneTransaction(base)
	second["来源记录ID"] = "source-row-2"
	_, firstKey := buildDedupIdentity(first)
	_, secondKey := buildDedupIdentity(second)
	if firstKey == secondKey {
		t.Fatal("transactions without a serial number must remain distinct by source record")
	}

	first["交易流水号"] = "WX-1"
	second["交易流水号"] = "WX-1"
	_, firstKey = buildDedupIdentity(first)
	_, secondKey = buildDedupIdentity(second)
	if firstKey != secondKey {
		t.Fatal("identical serial-number transactions must deduplicate across source records")
	}
	second["摘要说明"] = "different business record"
	_, secondKey = buildDedupIdentity(second)
	if firstKey == secondKey {
		t.Fatal("serial collisions with different business content must not be deleted")
	}
}

func TestUnifiedStoreRecordsDuplicateAndRejectedAudits(t *testing.T) {
	store, err := newUnifiedStreamStore(filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	valid := model.TransactionRow{
		"来源类型": "微信", "来源记录ID": "row-1", "来源文件": "wechat.csv", "来源Sheet": "Sheet1",
		"原始行号": "2", "交易时间": "2026-07-29 10:00:00", "交易金额": "100.00",
		"收付标志": "出", "交易账号": "A", "交易对手账卡号": "B", "交易流水号": "WX-1",
	}
	if err := store.Add(cloneTransaction(valid)); err != nil {
		t.Fatal(err)
	}
	duplicate := cloneTransaction(valid)
	duplicate["来源记录ID"] = "row-2"
	if err := store.Add(duplicate); err != nil {
		t.Fatal(err)
	}
	rejected := cloneTransaction(valid)
	rejected["来源记录ID"] = "row-3"
	rejected["收付标志"] = ""
	rejected["主体判定状态"] = "无法判定"
	rejected["主体判定依据"] = "未匹配调查主体"
	if err := store.Add(rejected); err != nil {
		t.Fatal(err)
	}
	if err := store.Commit(); err != nil {
		t.Fatal(err)
	}
	var duplicates, rejectedRows int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM duplicates").Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM rejected").Scan(&rejectedRows); err != nil {
		t.Fatal(err)
	}
	if duplicates != 1 || rejectedRows != 1 || store.rowsOut != 1 {
		t.Fatalf("unexpected audit counts: output=%d duplicates=%d rejected=%d", store.rowsOut, duplicates, rejectedRows)
	}
}

func cloneTransaction(input model.TransactionRow) model.TransactionRow {
	output := make(model.TransactionRow, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
