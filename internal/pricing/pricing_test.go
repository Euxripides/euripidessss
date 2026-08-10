package pricing

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

const (
	bscUSDT   = "0x55d398326f99059ff775485246999027b3197955"
	testToken = "0x1111111111111111111111111111111111111111"
)

type fakeRepository struct {
	prices  []HistoricalPrice
	buckets []time.Time
	written []HistoricalPrice
}

func (f *fakeRepository) Candidates(context.Context, uint32, string, time.Time, time.Duration) ([]HistoricalPrice, error) {
	return append([]HistoricalPrice(nil), f.prices...), nil
}
func (f *fakeRepository) Buckets(context.Context, uint32, string, Resolution, time.Time, time.Time) ([]time.Time, error) {
	return append([]time.Time(nil), f.buckets...), nil
}
func (f *fakeRepository) PutPrices(_ context.Context, prices []HistoricalPrice) error {
	f.written = append(f.written, prices...)
	return nil
}

func TestResolverHistoricalPriceBeatsCurrentAndStablecoinFallback(t *testing.T) {
	at := time.Date(2025, 1, 1, 12, 1, 37, 0, time.UTC)
	repo := &fakeRepository{prices: []HistoricalPrice{{ChainID: 56, TokenID: bscUSDT, PriceTime: at.Add(-37 * time.Second), PriceUSD: "0.98", Source: "COINGECKO_HISTORICAL", Confidence: "HIGH", Resolution: Resolution1Minute, SourcePriority: 10}}}
	resolver := NewResolver(repo, ResolverOptions{Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }})
	price, err := resolver.ResolvePrice(context.Background(), 56, bscUSDT, at)
	if err != nil {
		t.Fatal(err)
	}
	if price.PriceUSD != "0.98" || price.IsFallback {
		t.Fatalf("historical depeg not selected: %+v", price)
	}
}

func TestResolverPriorityToleranceFallbackAndMissing(t *testing.T) {
	at := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeRepository{prices: []HistoricalPrice{
		{PriceTime: at.Add(time.Minute), PriceUSD: "2", Source: "AGGREGATOR", Confidence: "HIGH", Resolution: Resolution1Minute, SourcePriority: 30},
		{PriceTime: at.Add(-90 * time.Second), PriceUSD: "1.9", Source: "LOCAL_VERIFIED", Confidence: "HIGH", Resolution: Resolution1Minute, IsVerified: true, SourcePriority: 100},
	}}
	resolver := NewResolver(repo, ResolverOptions{Now: func() time.Time { return at.AddDate(1, 0, 0) }})
	price, err := resolver.ResolvePrice(context.Background(), 56, testToken, at)
	if err != nil || price.PriceUSD != "1.9" {
		t.Fatalf("source priority failed: %+v %v", price, err)
	}
	repo.prices = []HistoricalPrice{{PriceTime: at.Add(3 * time.Minute), PriceUSD: "0.9", Source: "LOCAL_VERIFIED", Resolution: Resolution1Minute, IsVerified: true, SourcePriority: 0}}
	price, err = resolver.ResolvePrice(context.Background(), 56, bscUSDT, at)
	if err != nil || !price.IsFallback || price.PriceUSD != "1" {
		t.Fatalf("stable fallback failed: %+v %v", price, err)
	}
	if _, err = resolver.ResolvePrice(context.Background(), 56, testToken, at); !errors.Is(err, ErrPriceMissing) {
		t.Fatalf("missing price returned %v", err)
	}
}

func TestCanonicalNativeAssetIDs(t *testing.T) {
	for _, test := range []struct {
		chain       uint64
		input, want string
	}{{56, "BNB", "native:56"}, {1, "native", "native:1"}, {42161, "ETH", "native:42161"}} {
		got, err := CanonicalTokenID(test.chain, test.input)
		if err != nil || got != test.want {
			t.Fatalf("CanonicalTokenID(%d,%q)=%q,%v", test.chain, test.input, got, err)
		}
	}
	if _, err := CanonicalTokenID(56, "USDT"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("symbol collision accepted: %v", err)
	}
}

func TestGapDetectorCompactsMissingBuckets(t *testing.T) {
	from := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepository{buckets: []time.Time{from, from.Add(3 * time.Hour)}}
	gaps, err := NewGapDetector(repo).Detect(context.Background(), GapRequest{ChainID: 56, Token: testToken, From: from, To: from.Add(3 * time.Hour), Resolution: Resolution1Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].MissingBuckets != 2 || !gaps[0].Start.Equal(from.Add(time.Hour)) || !gaps[0].End.Equal(from.Add(2*time.Hour)) {
		t.Fatalf("unexpected gaps: %+v", gaps)
	}
}

type fakeSource struct {
	called int
	prices []HistoricalPrice
}

func (f *fakeSource) Name() string { return "LOCAL_IMPORT" }
func (f *fakeSource) Fetch(context.Context, BackfillRequest) ([]HistoricalPrice, error) {
	f.called++
	return f.prices, nil
}

type fakeJobStore struct {
	statuses []string
	gaps     []PriceGap
}

func (f *fakeJobStore) SaveJob(_ context.Context, job BackfillJob) error {
	f.statuses = append(f.statuses, job.Status)
	return nil
}
func (f *fakeJobStore) SaveGaps(_ context.Context, _ string, gaps []PriceGap) error {
	f.gaps = append(f.gaps, gaps...)
	return nil
}

func TestBackfillSourcesOnlyRunInWorkerService(t *testing.T) {
	from := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	repo := &fakeRepository{buckets: []time.Time{from}}
	source := &fakeSource{prices: []HistoricalPrice{{PriceTime: from, PriceUSD: "2", Confidence: "HIGH", Resolution: Resolution1Hour}}}
	jobs := &fakeJobStore{}
	resolver := NewResolver(repo, ResolverOptions{Now: func() time.Time { return from.AddDate(2, 0, 0) }})
	_, _ = resolver.ResolvePrice(context.Background(), 56, testToken, from)
	if source.called != 0 {
		t.Fatal("Explorer resolver invoked external backfill source")
	}
	service := NewBackfillService(repo, jobs, []BackfillSource{source})
	service.now = func() time.Time { return from }
	job, err := service.Run(context.Background(), BackfillRequest{JobID: "123e4567-e89b-12d3-a456-426614174000", ChainID: 56, Token: testToken, From: from, To: from, Resolution: Resolution1Hour, SourcePriority: []string{"LOCAL_IMPORT"}})
	if err != nil {
		t.Fatal(err)
	}
	if source.called != 1 || job.WrittenRows != 1 || len(repo.written) != 1 || strings.Join(jobs.statuses, ",") != "RUNNING,SUCCEEDED" {
		t.Fatalf("job=%+v calls=%d statuses=%v", job, source.called, jobs.statuses)
	}
}

type fakeClient struct {
	rows    []map[string]any
	table   string
	columns []string
	body    string
}

func (f *fakeClient) QueryJSON(context.Context, string) ([]map[string]any, error) { return f.rows, nil }
func (f *fakeClient) InsertCSV(_ context.Context, table string, columns []string, rows io.Reader) error {
	body, _ := io.ReadAll(rows)
	f.table, f.columns, f.body = table, columns, string(body)
	return nil
}

func TestRepositoryWritesFullPriceProvenance(t *testing.T) {
	client := &fakeClient{}
	repo := NewRepository(client)
	err := repo.PutPrices(context.Background(), []HistoricalPrice{{ChainID: 56, TokenID: testToken, PriceTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), PriceUSD: "2", Source: "LOCAL_VERIFIED", Confidence: "HIGH", Resolution: Resolution1Minute, IsVerified: true, PriceVersion: "p2", SourceVersion: "s4"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.table != "onchain.token_prices" || !strings.Contains(client.body, "LOCAL_VERIFIED") || !strings.Contains(client.body, "p2") || len(client.columns) != 19 {
		t.Fatalf("incomplete insert: %s %v", client.body, client.columns)
	}
}
