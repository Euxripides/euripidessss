package smartdownload

import (
	"context"
	"errors"
	"testing"
)

type schedulerMatrixAdapter struct {
	name      string
	rows      uint64
	available bool
	modeOK    map[DownloadMode]bool
	datasets  map[string]bool
}

func (a *schedulerMatrixAdapter) Name() string { return a.name }
func (a *schedulerMatrixAdapter) Supports(d string) bool {
	if a.datasets != nil {
		return a.datasets[d]
	}
	return d == DatasetTokenTransfers
}
func (a *schedulerMatrixAdapter) Available() bool { return a.available }
func (a *schedulerMatrixAdapter) AvailableForMode(chainKey string, mode DownloadMode) bool {
	return chainKey == "bsc" && a.available && a.modeOK[mode]
}
func (a *schedulerMatrixAdapter) Probe(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{EstimatedRows: a.rows, EstimatedBytes: a.rows * 160, Confidence: .9}, nil
}
func (a *schedulerMatrixAdapter) ExecuteRange(context.Context, RangeRequest) (*ProviderResult, error) {
	return &ProviderResult{}, nil
}

func TestSchedulerReordersAfterLargeDatasetProbe(t *testing.T) {
	s := NewSmartScheduler()
	for _, name := range []string{"csv", "sqd", "rpc", "sqd_cloud"} {
		s.Register(&schedulerMatrixAdapter{name: name, rows: 1_000_000, available: true,
			modeOK: map[DownloadMode]bool{DownloadModeAuto: true}})
	}
	plan, err := s.PlanDataset(context.Background(), ProbeRequest{
		Dataset: DatasetTokenTransfers, ChainKey: "bsc", ChainID: 56,
		Address: addrA, FromBlock: 1, ToBlock: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SizeClass != SizeClassL || plan.PreferredProvider != "sqd" {
		t.Fatalf("large plan=%+v, want size=L preferred=sqd", plan)
	}
	if len(plan.Candidates) == 0 || plan.Candidates[0].Name != "sqd" || plan.Candidates[0].Score != 88 {
		t.Fatalf("effective scores were not re-sorted: %+v", plan.Candidates)
	}
}

func TestSchedulerSelectionMatrixHonorsChainModeFailureAndCircuit(t *testing.T) {
	s := NewSmartScheduler()
	rpc := &schedulerMatrixAdapter{name: "rpc", available: true, modeOK: map[DownloadMode]bool{
		DownloadModeAuto: false, DownloadModeTurbo: true, DownloadModeEmergency: true,
	}}
	cloud := &schedulerMatrixAdapter{name: "sqd_cloud", available: true, modeOK: map[DownloadMode]bool{
		DownloadModeAuto: true, DownloadModeTurbo: true, DownloadModeEmergency: true,
	}}
	s.Register(rpc)
	s.Register(cloud)

	if got, ok := s.SelectProviderFor(DatasetTokenTransfers, "bsc", DownloadModeAuto, nil); !ok || got != "sqd_cloud" {
		t.Fatalf("AUTO selected %q ok=%v, want scoped Cloud fallback", got, ok)
	}
	if got, ok := s.SelectProviderFor(DatasetTokenTransfers, "bsc", DownloadModeTurbo, nil); !ok || got != "rpc" {
		t.Fatalf("TURBO selected %q ok=%v, want RPC", got, ok)
	}
	if got, ok := s.SelectProviderFor(DatasetTokenTransfers, "bsc", DownloadModeEmergency, []string{"rpc"}); !ok || got != "sqd_cloud" {
		t.Fatalf("EMERGENCY failover selected %q ok=%v, want Cloud", got, ok)
	}
	s.Health().RecordFailure("sqd_cloud", errors.New("cloud failed"))
	s.Health().RecordFailure("sqd_cloud", errors.New("cloud failed again"))
	if got, ok := s.SelectProviderFor(DatasetTokenTransfers, "bsc", DownloadModeAuto, nil); ok {
		t.Fatalf("circuit-open Cloud was incorrectly selected: %q", got)
	}
}
