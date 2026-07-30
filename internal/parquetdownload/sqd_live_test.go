package parquetdownload

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

func TestLiveSQDOneFinalizedBlockToParquet(t *testing.T) {
	if os.Getenv("SQD_LIVE_TEST") != "1" {
		t.Skip("set SQD_LIVE_TEST=1 for bounded real-network validation")
	}
	dataRoot := os.Getenv("SQD_LIVE_DATA_ROOT")
	if dataRoot == "" {
		t.Fatal("SQD_LIVE_DATA_ROOT is required")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	engine := duckdb.Open(repoRoot, dataRoot, duckdb.AnalyticsConfig{})
	if !engine.Available() {
		t.Fatalf("DuckDB unavailable: %+v", engine.Status())
	}
	settings := defaultSettings(repoRoot)
	settings.DataRoot = dataRoot
	jobID := "sqd-live-" + time.Now().Format("20060102-150405")
	addresses := []string{
		"0x916f992df86795f24de6c268cfb9031fbb1155da",
		"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d",
	}
	manager := &Manager{
		settings:    settings,
		jobs:        map[string]*Job{jobID: {ID: jobID, Status: StatusRunning, Stages: defaultStages()}},
		lastPersist: map[string]time.Time{},
		sqd:         sqd.New(nil),
		engine:      engine,
	}
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.ingestSQD(
		context.Background(),
		jobID,
		settings,
		network,
		SQDBlockRange{From: 112932400, To: 112932400},
		addresses,
		[]string{"logs", "traces"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.LogRows == 0 || result.TokenTransferRows == 0 || result.TraceRows == 0 {
		t.Fatalf("real SQD rows incomplete: %+v", result)
	}
	var transactionRows int
	err = manager.sqd.StreamTransactions(
		context.Background(),
		network,
		sqd.BlockRange{From: 112932400, To: 112932400},
		[]string{"0x4848489f0b2bedd788c696e2d79b6b69d7484848"},
		func(block sqd.Block) error {
			transactionRows += len(block.Transactions)
			return nil
		},
	)
	if err != nil || transactionRows == 0 {
		t.Fatalf("SQD transaction adapter failed: rows=%d err=%v", transactionRows, err)
	}
	var activityPath string
	for _, output := range result.Outputs {
		if err := verifyParquetFile(output); err != nil {
			t.Fatalf("invalid output %s: %v", output, err)
		}
		if strings.Contains(filepath.ToSlash(output), "/address_activity/") {
			activityPath = output
		}
	}
	summaryTemp := filepath.Join(dataRoot, "tmp", jobID, "summary")
	if err := os.MkdirAll(summaryTemp, 0755); err != nil {
		t.Fatal(err)
	}
	summaryPath, summaryRows, err := manager.writeAddressSummary(
		context.Background(), jobID, settings, network, summaryTemp, []string{activityPath}, addresses,
	)
	if err != nil || summaryRows != int64(len(addresses)) {
		t.Fatalf("address summary failed: rows=%d err=%v", summaryRows, err)
	}
	if err := verifyParquetFile(summaryPath); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{manager: manager, mux: http.NewServeMux()}
	handler.register()
	request := httptest.NewRequest(http.MethodGet, "/address/0x916f992df86795f24de6c268cfb9031fbb1155da/summary?chain_key=bsc", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("address summary API HTTP %d: %s", response.Code, response.Body.String())
	}
	var summary map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil || summary["address_type"] == nil {
		t.Fatalf("invalid address summary API: %s err=%v", response.Body.String(), err)
	}
	sections := []string{"summary", "activity", "tokens", "nfts", "counterparties"}
	var wait sync.WaitGroup
	failures := make(chan string, len(sections))
	for _, section := range sections {
		wait.Add(1)
		go func(section string) {
			defer wait.Done()
			url := "/address/" + addresses[0] + "/" + section + "?chain_key=bsc"
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
			if recorder.Code != http.StatusOK {
				failures <- section + ": " + recorder.Body.String()
			}
		}(section)
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Errorf("concurrent address API failed: %s", failure)
	}
	for _, check := range []struct {
		section string
		fields  []string
	}{
		{section: "activity", fields: []string{"tx_hash", "activity_type", "direction", "asset_type", "amount_raw", "status"}},
		{section: "counterparties", fields: []string{"direction", "native_in_count", "native_out_count", "token_in_count", "token_out_count"}},
	} {
		recorder := httptest.NewRecorder()
		url := "/address/" + addresses[0] + "/" + check.section + "?chain_key=bsc"
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, url, nil))
		var page map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode %s response: %v", check.section, err)
		}
		rows, ok := page["rows"].([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("%s response has no rows: %s", check.section, recorder.Body.String())
		}
		first, ok := rows[0].(map[string]any)
		if !ok {
			t.Fatalf("%s row shape invalid: %+v", check.section, rows[0])
		}
		for _, field := range check.fields {
			if _, exists := first[field]; !exists {
				t.Fatalf("%s response missing %s: %+v", check.section, field, first)
			}
		}
	}
	t.Logf("SQD V1.3 real block validated: transactions=%d result=%+v summary=%s", transactionRows, result, summaryPath)
}
