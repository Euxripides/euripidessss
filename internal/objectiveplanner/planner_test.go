package objectiveplanner

import (
	"strings"
	"testing"
)

func TestFundSinkMatrixAndCostGuard(t *testing.T) {
	o := Objective{Type: "fund_sink", Constraints: Constraints{MaxAddresses: 100}}
	p, err := Build(o, "bsc", []string{"0xaaa1", "0xbbb1"}, 114450000, 114499999, 10, 1000, 50)
	if err != nil {
		t.Fatal(err)
	}
	if p.Estimate.Rejected {
		t.Fatalf("small plan should pass guard: %+v", p.Estimate)
	}
	if p.Needs[0].Dataset != "token_transfer" || p.Needs[0].Direction != "in" {
		t.Fatalf("fund_sink first need = %+v", p.Needs[0])
	}
	foundBalance := false
	for _, n := range p.Needs {
		if n.Dataset == "balance" && n.CloudEligible {
			t.Fatal("balance must not be cloud eligible")
		}
		if n.Dataset == "balance" {
			foundBalance = true
		}
	}
	if !foundBalance {
		t.Fatal("fund_sink matrix missing balance")
	}
	// Cost Guard：小上限应拒绝
	big, err := Build(o, "bsc", make([]string, 100), 114450000, 114999999, 1, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !big.Estimate.Rejected || !strings.Contains(big.Estimate.RejectReason, "上限") {
		t.Fatalf("cost guard must reject: %+v", big.Estimate)
	}
}

func TestUnknownObjective(t *testing.T) {
	if _, err := Build(Objective{Type: "nope"}, "bsc", []string{"0xaaa1"}, 1, 2, 100, 1000, 50); err == nil {
		t.Fatal("unknown objective must error")
	}
}
