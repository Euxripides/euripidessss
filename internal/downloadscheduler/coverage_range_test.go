package downloadscheduler

import (
	"context"
	"testing"
)

type rangeCoverageFixture struct {
	covered bool
	rows    int64
}

func (f rangeCoverageFixture) AddressTxCount(context.Context, string) (int64, error) {
	return 999, nil // 其他历史区间有数据，不能代表本次范围已覆盖。
}

func (f rangeCoverageFixture) AddressRangeCovered(context.Context, string, string, Dataset, uint64, uint64) (bool, int64, error) {
	return f.covered, f.rows, nil
}

func TestRequirementCoverageDoesNotTreatUnrelatedAddressHistoryAsFull(t *testing.T) {
	resolver := NewCoverageResolver(rangeCoverageFixture{covered: false, rows: 999})
	result, err := resolver.CheckRequirement(context.Background(), Requirement{
		ChainKey: "bsc", Dataset: DatasetTokenTransfer,
		Addresses: []string{"0x1111111111111111111111111111111111111111"},
		FromBlock: 100, ToBlock: 110,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Have {
		t.Fatalf("uncovered request was incorrectly treated as full: %+v", result.Items)
	}
}

func TestRequirementCoverageAcceptsExactCertifiedRange(t *testing.T) {
	resolver := NewCoverageResolver(rangeCoverageFixture{covered: true, rows: 12})
	result, err := resolver.CheckRequirement(context.Background(), Requirement{
		ChainKey: "bsc", Dataset: DatasetTokenTransfer,
		Addresses: []string{"0x1111111111111111111111111111111111111111"},
		FromBlock: 100, ToBlock: 110,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || !result.Items[0].Have || result.Items[0].TxCount != 12 {
		t.Fatalf("exact covered request not recognized: %+v", result.Items)
	}
}
