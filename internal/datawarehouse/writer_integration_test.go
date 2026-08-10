package datawarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
	"github.com/etl/backend/internal/explorer"
	"github.com/etl/backend/internal/smartdownload"
)

func TestClickHouseIntegrationWriterIdempotencyAndExplorer(t *testing.T) {
	if os.Getenv("CLICKHOUSE_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_INTEGRATION=1 to run against the deployed ClickHouse")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	jobID := fmt.Sprintf("integration_%d", time.Now().UnixNano())
	tokenJobID := jobID + "_token"
	contractJobID := jobID + "_contract"
	contractTxJobID := jobID + "_contract_tx"
	logJobID := jobID + "_log"
	failedJobID := jobID + "_failed"
	from := "0x1111111111111111111111111111111111111111"
	to := "0x2222222222222222222222222222222222222222"
	txHash := "0x" + strings.Repeat("a", 64)
	tokenAddress := "0x3333333333333333333333333333333333333333"
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		for _, target := range [][2]string{{"chain_transactions", jobID}, {"chain_transactions", failedJobID}, {"chain_transactions", contractTxJobID}, {"token_transfers", tokenJobID}, {"contract_creations", contractJobID}, {"contracts", contractJobID}, {"parsed_events", logJobID}, {"address_activity", jobID}, {"address_activity", failedJobID}, {"address_activity", contractTxJobID}, {"address_activity", tokenJobID}, {"address_activity", contractJobID}} {
			_ = client.Exec(cleanup, fmt.Sprintf("ALTER TABLE onchain.%s DELETE WHERE ingest_job_id='%s' SETTINGS mutations_sync=2", target[0], target[1]))
		}
	}()

	csvData := "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,method_id,status,gas_used,gas_price,source_provider\n" +
		fmt.Sprintf("56,999999999,1700000000,%s,7,%s,%s,0x10,,0xa9059cbb,1,21000,5,integration_test\n", txHash, from, to)
	writer := NewWriter(client, fakeDuckDB{csv: csvData})
	writer.SetAnalyticsRefresher(explorer.NewRepository(client))
	req := smartdownload.IndexedWriteRequest{
		DatasetJobID: jobID, ChainKey: "bsc", ChainID: 56,
		Dataset: smartdownload.DatasetTransactions, Address: from,
		FromBlock: 999999999, ToBlock: 999999999, RowCount: 1,
		MergedParquet: eDriveParquet(t), SourceProvider: "integration_test",
	}
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := writer.WriteIndexed(ctx, req)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if result.InputRows != 1 || result.InsertedRows != 1 || result.RejectedRows != 0 || result.ActivityRows != 2 || result.VerifiedRows != 1 {
			t.Fatalf("attempt %d reconciliation: %+v", attempt, result)
		}
	}

	contractHash := "0x" + strings.Repeat("c", 64)
	contractAddress := "0x4444444444444444444444444444444444444444"
	contractCSV := "chain_id,block_number,block_time,transaction_hash,creator_address,contract_address,creation_type,source_provider\n" +
		fmt.Sprintf("56,999999999,1700000002,%s,%s,%s,CREATE,integration_test\n", contractHash, from, contractAddress)
	contractReq := req
	contractReq.DatasetJobID, contractReq.Dataset = contractJobID, smartdownload.DatasetContractCreations
	contractResult, err := NewWriter(client, fakeDuckDB{csv: contractCSV}).WriteIndexed(ctx, contractReq)
	if err != nil || contractResult.VerifiedRows != 1 || contractResult.ActivityRows != 2 {
		t.Fatalf("contract reconciliation: result=%+v err=%v", contractResult, err)
	}
	contractTxCSV := "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,status,is_contract_creation,created_contract_address,source_provider\n" +
		fmt.Sprintf("56,999999999,1700000002,%s,10,%s,,0,1,true,%s,integration_test\n", contractHash, from, contractAddress)
	contractTxReq := req
	contractTxReq.DatasetJobID = contractTxJobID
	contractTxResult, err := NewWriter(client, fakeDuckDB{csv: contractTxCSV}).WriteIndexed(ctx, contractTxReq)
	if err != nil || contractTxResult.VerifiedRows != 1 {
		t.Fatalf("contract transaction reconciliation: result=%+v err=%v", contractTxResult, err)
	}

	topic0 := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	fromTopic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(from, "0x")
	toTopic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(to, "0x")
	topics := fmt.Sprintf(`["%s","%s","%s"]`, topic0, fromTopic, toTopic)
	logCSV := "chain_id,block_number,block_time,transaction_hash,log_index,contract_address,topics,data,source_provider\n" +
		fmt.Sprintf("56,999999999,1700000000,%s,9,%s,\"%s\",0x%s,integration_test\n", txHash, tokenAddress, strings.ReplaceAll(topics, `"`, `""`), strings.Repeat("0", 63)+"1")
	logReq := req
	logReq.DatasetJobID, logReq.Dataset = logJobID, smartdownload.DatasetLogs
	logResult, err := NewWriter(client, fakeDuckDB{csv: logCSV}).WriteIndexed(ctx, logReq)
	if err != nil || logResult.VerifiedRows != 1 || logResult.ActivityRows != 0 {
		t.Fatalf("log reconciliation: result=%+v err=%v", logResult, err)
	}

	failedHash := "0x" + strings.Repeat("d", 64)
	failedCSV := "chain_id,block_number,block_time,transaction_hash,transaction_index,from_address,to_address,value_raw,input,status,source_provider\n" +
		fmt.Sprintf("56,999999999,1735689600,%s,8,%s,%s,0,0x095ea7b3,0,integration_test\n", failedHash, from, to)
	failedReq := req
	failedReq.DatasetJobID = failedJobID
	failedResult, err := NewWriter(client, fakeDuckDB{csv: failedCSV}).WriteIndexed(ctx, failedReq)
	if err != nil || failedResult.VerifiedRows != 1 {
		t.Fatalf("failed transaction reconciliation: result=%+v err=%v", failedResult, err)
	}

	page, err := explorer.NewRepository(client).ListActivity(ctx, explorer.ActivityQuery{
		ChainID: 56, Address: from, Activity: explorer.ActivityTransactions, PageSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.TransactionHash == txHash && item.Direction == "OUT" && item.Amount == "16" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ClickHouse Explorer did not return the idempotently written transaction: %+v", page.Items)
	}

	tokenHash := txHash
	tokenCSV := "chain_id,block_number,block_time,transaction_hash,log_index,token_address,token_standard,from_address,to_address,value_raw,source_provider\n" +
		fmt.Sprintf("56,999999999,1735689600,%s,9,0x55d398326f99059ff775485246999027b3197955,ERC20,%s,%s,100,integration_test\n", tokenHash, from, to)
	tokenWriter := NewWriter(client, fakeDuckDB{csv: tokenCSV})
	tokenReq := req
	tokenReq.DatasetJobID = tokenJobID
	tokenReq.Dataset = smartdownload.DatasetTokenTransfers
	for attempt := 1; attempt <= 2; attempt++ {
		result, err := tokenWriter.WriteIndexed(ctx, tokenReq)
		if err != nil || result.VerifiedRows != 1 || result.ActivityRows != 2 {
			t.Fatalf("token attempt %d: result=%+v err=%v", attempt, result, err)
		}
	}

	if baseURL := strings.TrimRight(os.Getenv("EXPLORER_INTEGRATION_URL"), "/"); baseURL != "" {
		for _, endpoint := range []string{"transactions", "token-transfers", "summary"} {
			response, err := http.Get(fmt.Sprintf("%s/api/v1/explorer/bsc/address/%s/%s", baseURL, from, endpoint))
			if err != nil {
				t.Fatal(err)
			}
			var payload any
			decodeErr := json.NewDecoder(response.Body).Decode(&payload)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK || decodeErr != nil {
				t.Fatalf("Explorer HTTP %s: status=%d payload=%v decode=%v", endpoint, response.StatusCode, payload, decodeErr)
			}
		}
		response, err := http.Get(fmt.Sprintf("%s/api/v2/explorer/bsc/tx/%s", baseURL, txHash))
		if err != nil {
			t.Fatal(err)
		}
		var canonical map[string]any
		decodeErr := json.NewDecoder(response.Body).Decode(&canonical)
		_ = response.Body.Close()
		method, _ := canonical["method"].(map[string]any)
		transfers, _ := canonical["token_transfers"].([]any)
		events, _ := canonical["parsed_events"].([]any)
		var transfer map[string]any
		if len(transfers) == 1 {
			transfer, _ = transfers[0].(map[string]any)
		}
		usd, _ := transfer["usd"].(map[string]any)
		if response.StatusCode != http.StatusOK || decodeErr != nil || canonical["status"] != "SUCCESS" || method["name"] != "transfer" || len(events) != 1 || len(transfers) != 1 || usd["usd_value"] != "100" || usd["price_source"] != "TETHER_USD_PEG_FALLBACK" {
			t.Fatalf("Canonical V2 HTTP: status=%d payload=%v decode=%v", response.StatusCode, canonical, decodeErr)
		}
		response, err = http.Get(fmt.Sprintf("%s/api/v2/explorer/bsc/tx/%s", baseURL, failedHash))
		if err != nil {
			t.Fatal(err)
		}
		canonical = nil
		decodeErr = json.NewDecoder(response.Body).Decode(&canonical)
		_ = response.Body.Close()
		failedTransfers, _ := canonical["token_transfers"].([]any)
		if response.StatusCode != http.StatusOK || decodeErr != nil || canonical["status"] != "FAILED" || len(failedTransfers) != 0 {
			t.Fatalf("Canonical failed transaction: status=%d payload=%v decode=%v", response.StatusCode, canonical, decodeErr)
		}
		response, err = http.Get(fmt.Sprintf("%s/api/v2/explorer/bsc/tx/%s", baseURL, contractHash))
		if err != nil {
			t.Fatal(err)
		}
		canonical = nil
		decodeErr = json.NewDecoder(response.Body).Decode(&canonical)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || canonical["is_contract_creation"] != true || canonical["contract_creation"] == nil {
			t.Fatalf("Canonical contract transaction: status=%d payload=%v decode=%v", response.StatusCode, canonical, decodeErr)
		}
		response, err = http.Get(fmt.Sprintf("%s/api/v2/contracts/bsc/%s", baseURL, contractAddress))
		if err != nil {
			t.Fatal(err)
		}
		var contract map[string]any
		decodeErr = json.NewDecoder(response.Body).Decode(&contract)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || contract["creation_tx"] != contractHash || contract["creator_address"] != from {
			t.Fatalf("Canonical contract asset: status=%d payload=%v decode=%v", response.StatusCode, contract, decodeErr)
		}
	}
}
