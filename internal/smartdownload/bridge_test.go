package smartdownload

import (
	"context"
	"testing"

	"github.com/etl/backend/internal/downloadscheduler"
)

func TestLegacyPlanBridge(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, DefaultOptions(), NewJSONLPartWriter(dir))
	bridge := NewLegacyPlanBridge(svc)
	plan := &downloadscheduler.Plan{
		ID: "legacy-plan-1",
		Tasks: []*downloadscheduler.PlanTask{
			{
				ID: "t1",
				Requirement: downloadscheduler.Requirement{
					Dataset:   downloadscheduler.DatasetTransactions,
					ChainKey:  "bsc",
					Addresses: []string{addrA, addrB},
					FromBlock: 100,
					ToBlock:   299,
				},
			},
			{
				ID: "t2",
				Requirement: downloadscheduler.Requirement{
					Dataset:   downloadscheduler.DatasetBalance,
					ChainKey:  "bsc",
					Addresses: []string{addrB, addrC},
				},
			},
		},
	}
	resp, err := bridge.BridgePlan(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Valid != 3 {
		t.Fatalf("桥接地址数 %d，期望 3", resp.Valid)
	}
	if len(resp.Batch.DatasetTypes) != 2 {
		t.Fatalf("桥接数据集 %v，期望 2 类", resp.Batch.DatasetTypes)
	}
	// 地址 B 应同时有 transactions + balances 两个 DatasetJob
	bID := addressID(store, resp.Batch.ID, addrB)
	datasets := store.ListDatasetsByAddress(bID)
	if len(datasets) != 2 {
		t.Fatalf("地址 B 数据集数 %d，期望 2", len(datasets))
	}
	for _, ds := range datasets {
		if len(store.ListRangesByDataset(ds.ID)) == 0 {
			t.Fatalf("数据集 %s 没有 Range", ds.ID)
		}
	}
}
