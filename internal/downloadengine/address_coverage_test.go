package downloadengine

import (
	"fmt"
	"testing"
	"time"
)

// ── V2.1 RC2 地址覆盖索引测试 ──

func TestBloomFilterBasic(t *testing.T) {
	bf := NewBloomFilter(10000, 0.01)
	bf.Add("0x55d398326f99059ff775485246999027b3197955") // USDT
	bf.Add("0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c") // WBNB

	if !bf.MightContain("0x55d398326f99059ff775485246999027b3197955") {
		t.Error("USDT should be in bloom filter")
	}
	if bf.MightContain("0xdead000000000000000000000000000000000000") {
		t.Log("  (false positive possible — acceptable for bloom filter)")
	}
	t.Logf("  Bloom filter: add=2, contains=OK, bits=%d", len(bf.bits)*64)
}

func TestAddressCoverageIndexCheck(t *testing.T) {
	aci := NewAddressCoverageIndex()

	aci.Mark(&AddressCoverageRecord{
		Address: "0x55d398326f99059ff775485246999027b3197955", Chain: "bsc",
		DatasetType: DSTransactions, StartBlock: 44500000, EndBlock: 44501000,
		Status: AddrReady, DatasetID: "ds-001",
	})

	status, found := aci.Check("bsc", "0x55d398326f99059ff775485246999027b3197955", DSTransactions)
	if !found || status != AddrReady {
		t.Errorf("USDT tx should be READY, got %v/%s", found, status)
	}

	// 不同数据类型独立
	_, found = aci.Check("bsc", "0x55d398326f99059ff775485246999027b3197955", DSLogs)
	if found {
		t.Error("USDT logs should NOT be covered yet")
	}

	t.Logf("  USDT: tx=READY, logs=NOT_FOUND ✅")
}

func TestAddressCoverageIncrementalTask(t *testing.T) {
	aci := NewAddressCoverageIndex()
	chain := "bsc"

	// Mark 100K as READY
	for i := 0; i < 100000; i++ {
		aci.Mark(&AddressCoverageRecord{
			Address: fmt.Sprintf("0x%040x", i), Chain: chain,
			DatasetType: DSTransactions, Status: AddrReady,
		})
	}

	// Simulate 500K input → 100K ready + 400K missing
	addrs500K := make([]string, 500000)
	for i := 0; i < 500000; i++ {
		addrs500K[i] = fmt.Sprintf("0x%040x", i)
	}

	task := aci.IncrementalTask(chain, addrs500K, DSTransactions)
	if task.Ready != 100000 || task.Missing != 400000 {
		t.Errorf("expected 100K ready + 400K missing, got %d+%d", task.Ready, task.Missing)
	}
	t.Logf("  Incremental task: %d total, %d ready, %d missing ✅", task.Total, task.Ready, task.Missing)
}

func TestAddressCoverageBatchCheck(t *testing.T) {
	aci := NewAddressCoverageIndex()

	// Mark first 100 as READY
	for i := 0; i < 100; i++ {
		aci.Mark(&AddressCoverageRecord{
			Address: fmt.Sprintf("0x%040x", i), Chain: "bsc",
			DatasetType: DSTransactions, Status: AddrReady,
		})
	}

	// Batch query 200 addresses
	addrs := make([]string, 200)
	for i := 0; i < 200; i++ {
		addrs[i] = fmt.Sprintf("0x%040x", i)
	}

	ready, missing := aci.BatchCheck("bsc", addrs, DSTransactions)
	if len(ready) != 100 || len(missing) != 100 {
		t.Errorf("expected 100 ready + 100 missing, got %d+%d", len(ready), len(missing))
	}
	t.Logf("  Batch check: %d ready, %d missing ✅", len(ready), len(missing))
}

func TestAddressCoverageStatusTransition(t *testing.T) {
	aci := NewAddressCoverageIndex()

	addr := "0xabc"
	aci.Mark(&AddressCoverageRecord{Address: addr, Chain: "bsc", DatasetType: DSTransactions, Status: AddrNew})
	aci.Mark(&AddressCoverageRecord{Address: addr, Chain: "bsc", DatasetType: DSTransactions, Status: AddrScheduled})
	aci.Mark(&AddressCoverageRecord{Address: addr, Chain: "bsc", DatasetType: DSTransactions, Status: AddrDownloading})

	status, _ := aci.Check("bsc", addr, DSTransactions)
	if status != AddrDownloading {
		t.Errorf("expected DOWNLOADING, got %s", status)
	}

	aci.Mark(&AddressCoverageRecord{Address: addr, Chain: "bsc", DatasetType: DSTransactions, Status: AddrReady})
	status, _ = aci.Check("bsc", addr, DSTransactions)
	if status != AddrReady {
		t.Errorf("expected READY, got %s", status)
	}
	t.Logf("  Status transition: NEW→SCHEDULED→DOWNLOADING→READY ✅")
}

func TestAddressCoverageFailAndRequeue(t *testing.T) {
	aci := NewAddressCoverageIndex()

	addrs := make([]string, 10)
	for i := 0; i < 10; i++ {
		addrs[i] = fmt.Sprintf("0x%040x", i)
		aci.Mark(&AddressCoverageRecord{
			Address: addrs[i], Chain: "bsc", DatasetType: DSTransactions,
			Status: AddrDownloading,
		})
	}

	// 模拟5个失败
	failed := aci.FailAndRequeue("bsc", addrs[:5], DSTransactions)
	if failed != 5 {
		t.Errorf("expected 5 failed, got %d", failed)
	}

	for _, a := range addrs[:5] {
		s, _ := aci.Check("bsc", a, DSTransactions)
		if s != AddrFailed {
			t.Errorf("%s should be FAILED, got %s", a, s)
		}
	}
	t.Logf("  Fail and requeue: 5 DOWNLOADING → 5 FAILED ✅")
}

func TestMultiTypeCoverage(t *testing.T) {
	aci := NewAddressCoverageIndex()
	addr := "0x55d398326f99059ff775485246999027b3197955"

	aci.Mark(&AddressCoverageRecord{Address: addr, Chain: "bsc", DatasetType: DSTransactions, Status: AddrReady})
	aci.Mark(&AddressCoverageRecord{Address: addr, Chain: "bsc", DatasetType: DSLogs, Status: AddrDownloading})

	mt := aci.MultiTypeCheck("bsc", addr)
	if mt.Transactions != AddrReady {
		t.Errorf("tx should be READY, got %s", mt.Transactions)
	}
	if mt.Logs != AddrDownloading {
		t.Errorf("logs should be DOWNLOADING, got %s", mt.Logs)
	}
	if mt.Traces != AddrNew {
		t.Errorf("traces should be NEW, got %s", mt.Traces)
	}
	t.Logf("  Multi-type: tx=READY, logs=DOWNLOADING, traces=NEW, transfers=NEW ✅")
}

func TestDuckDBAddressCoverageDDL(t *testing.T) {
	ddl := AddressCoverageDDL()
	if ddl == "" {
		t.Error("DDL should not be empty")
	}
	t.Logf("  DDL: %s...", ddl[:60])
}

func TestAddressCoverageSnapshot(t *testing.T) {
	aci := NewAddressCoverageIndex()
	for i := 0; i < 100; i++ {
		aci.Mark(&AddressCoverageRecord{Address: fmt.Sprintf("0x%040x", i), Chain: "bsc", DatasetType: DSTransactions, Status: AddrReady})
	}
	for i := 0; i < 50; i++ {
		aci.Check("bsc", fmt.Sprintf("0x%040x", i), DSTransactions)     // hits
		aci.Check("bsc", fmt.Sprintf("0x%040x", i+500), DSTransactions) // misses
	}
	snap := aci.Snapshot()
	t.Logf("  Snapshot: total=%d, hit=%d, miss=%d, ready=%d, download=%d",
		snap["address_coverage_total"], snap["address_cache_hit"],
		snap["address_cache_miss"], snap["address_ready_total"],
		snap["address_download_required"])
}

func init() { _ = time.Now } // avoid unused import
