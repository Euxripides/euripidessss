package parquetdownload

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/etl/backend/internal/chain"
	rpcsource "github.com/etl/backend/internal/datasource/rpc"
	"github.com/etl/backend/internal/normalize"
)

type analyticsOutcome struct {
	ReceiptRows       int64
	ContractCreations int64
	ActivityRows      int64
	Outputs           []string
}

func (m *Manager) buildAnalytics(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	targetPath string,
) (analyticsOutcome, error) {
	job, err := m.Get(jobID)
	if err != nil {
		return analyticsOutcome{}, err
	}
	var transactionPaths []string
	for _, file := range job.Files {
		if file.Status == StatusDone && file.OutputPath != "" {
			transactionPaths = append(transactionPaths, file.OutputPath)
		}
	}
	if len(transactionPaths) == 0 {
		return analyticsOutcome{}, errors.New("没有可用于标准化分析的 transactions Parquet")
	}

	m.mutate(jobID, func(item *Job) {
		item.Stage = "receipts"
		if item.IncludeReceipts {
			setStage(item, "receipts", StatusRunning, 0, "探测 RPC 与 Receipt Schema")
		} else {
			setStage(item, "receipts", StatusDone, 100, "未启用；不将合约创建候选视为确认结果")
		}
	})

	result := analyticsOutcome{}
	receiptPath := ""
	if job.IncludeReceipts {
		receiptPath, result.ReceiptRows, err = m.fetchReceipts(ctx, jobID, settings, network, transactionPaths)
		if err != nil {
			return analyticsOutcome{}, err
		}
		result.Outputs = append(result.Outputs, receiptPath)
		m.mutate(jobID, func(item *Job) {
			setStage(item, "receipts", StatusDone, 100, fmt.Sprintf("已标准化 %d 条回执", result.ReceiptRows))
		})
	}

	m.mutate(jobID, func(item *Job) {
		item.Stage = "normalize"
		setStage(item, "normalize", StatusRunning, 20, "生成链级标准化结果")
	})
	contractPath, contractRows, activityPath, activityRows, err := m.writeNormalizedAnalytics(
		ctx, jobID, settings, network, targetPath, transactionPaths, receiptPath,
	)
	if err != nil {
		return analyticsOutcome{}, err
	}
	result.ContractCreations = contractRows
	result.ActivityRows = activityRows
	if contractPath != "" {
		result.Outputs = append(result.Outputs, contractPath)
	}
	result.Outputs = append(result.Outputs, activityPath)
	m.mutate(jobID, func(item *Job) {
		setStage(item, "normalize", StatusDone, 100, fmt.Sprintf("确认 %d 笔准确合约创建", contractRows))
		item.Stage = "activity"
		setStage(item, "activity", StatusDone, 100, fmt.Sprintf("生成 %d 条地址统一流水", activityRows))
	})
	return result, nil
}

func (m *Manager) fetchReceipts(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	transactionPaths []string,
) (string, int64, error) {
	endpoint := strings.TrimSpace(os.Getenv(network.RPCEnv))
	client, err := rpcsource.New(endpoint, nil)
	if err != nil {
		return "", 0, err
	}
	jobTemp := filepath.Join(settings.DataRoot, "tmp", "job-"+jobID)
	if err := os.MkdirAll(jobTemp, 0755); err != nil {
		return "", 0, err
	}
	hashPath := filepath.Join(jobTemp, "receipt_hashes.csv")
	receiptCSV := filepath.Join(jobTemp, "receipts.csv")
	defer os.Remove(hashPath)
	defer os.Remove(receiptCSV)
	copyHashes := duckDBSettingsSQL(settings) +
		"; COPY (SELECT DISTINCT lower(tx_hash) AS tx_hash FROM read_parquet(" +
		sqlStringList(transactionPaths) + ", union_by_name=true) ORDER BY tx_hash) TO " +
		sqlString(hashPath) + " (FORMAT CSV, HEADER true)"
	if _, err := m.engine.ExecSQL(ctx, copyHashes); err != nil {
		return "", 0, fmt.Errorf("生成 Receipt 哈希队列: %w", err)
	}

	hashFile, err := os.Open(hashPath)
	if err != nil {
		return "", 0, err
	}
	defer hashFile.Close()
	scanner := bufio.NewScanner(hashFile)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	if scanner.Scan() && strings.TrimSpace(scanner.Text()) != "tx_hash" {
		return "", 0, errors.New("Receipt 哈希队列表头异常")
	}

	output, err := os.Create(receiptCSV)
	if err != nil {
		return "", 0, err
	}
	writer := csv.NewWriter(output)
	if err := writer.Write([]string{"chain_key", "chain_id", "tx_hash", "status", "gas_used", "effective_gas_price", "contract_address", "logs_count"}); err != nil {
		output.Close()
		return "", 0, err
	}
	var batch []string
	var receiptRows int64
	probed := false
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if !probed {
			if err := client.Probe(ctx, network, batch[0]); err != nil {
				return err
			}
			probed = true
		}
		receipts, err := client.Receipts(ctx, network, batch)
		if err != nil {
			return err
		}
		for _, receipt := range receipts {
			if err := writeReceiptCSV(writer, receipt); err != nil {
				return err
			}
			receiptRows++
		}
		m.mutate(jobID, func(item *Job) {
			item.ReceiptRows = receiptRows
			progress := 5.0
			if item.MatchedRows > 0 {
				progress = minFloat(99, float64(receiptRows)/float64(item.MatchedRows)*100)
			}
			setStage(item, "receipts", StatusRunning, progress, fmt.Sprintf("已获取 %d / %d", receiptRows, item.MatchedRows))
		})
		batch = batch[:0]
		return nil
	}
	for scanner.Scan() {
		hash := strings.TrimSpace(scanner.Text())
		if hash == "" {
			continue
		}
		batch = append(batch, hash)
		if len(batch) >= settings.ReceiptBatchSize {
			if err := flush(); err != nil {
				output.Close()
				return "", 0, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		output.Close()
		return "", 0, err
	}
	if err := flush(); err != nil {
		output.Close()
		return "", 0, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		output.Close()
		return "", 0, err
	}
	if err := output.Close(); err != nil {
		return "", 0, err
	}

	receiptDir := filepath.Join(settings.DataRoot, "warehouse", "transaction_receipts", "chain="+network.Key, "job="+jobID)
	if err := os.MkdirAll(receiptDir, 0755); err != nil {
		return "", 0, err
	}
	receiptPath := filepath.Join(receiptDir, "receipts.parquet")
	sqlText := duckDBSettingsSQL(settings) + `;
COPY (
  SELECT
    lower(chain_key) AS chain_key,
    TRY_CAST(chain_id AS UBIGINT) AS chain_id,
    lower(tx_hash) AS tx_hash,
    TRY_CAST(status AS UINTEGER) AS status,
    gas_used,
    effective_gas_price,
    nullif(lower(contract_address), '') AS contract_address,
    TRY_CAST(logs_count AS UINTEGER) AS logs_count
  FROM read_csv(` + sqlString(receiptCSV) + `, header=true, all_varchar=true)
) TO ` + sqlString(receiptPath) + ` (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 250000)`
	if _, err := m.engine.ExecSQL(ctx, sqlText); err != nil {
		return "", 0, fmt.Errorf("写入 transaction_receipts: %w", err)
	}
	return receiptPath, receiptRows, nil
}

func (m *Manager) writeNormalizedAnalytics(
	ctx context.Context,
	jobID string,
	settings Settings,
	network chain.EVM,
	targetPath string,
	transactionPaths []string,
	receiptPath string,
) (string, int64, string, int64, error) {
	contractPath := ""
	receiptJoin := ""
	contractSQL := ""
	if receiptPath != "" {
		contractDir := filepath.Join(settings.DataRoot, "warehouse", "contract_creations", "chain="+network.Key, "job="+jobID)
		if err := os.MkdirAll(contractDir, 0755); err != nil {
			return "", 0, "", 0, err
		}
		contractPath = filepath.Join(contractDir, "contract-creations.parquet")
		receiptJoin = "LEFT JOIN read_parquet(" + sqlString(receiptPath) + ") r ON r.chain_key = t.chain_key AND r.chain_id = t.chain_id AND r.tx_hash = t.tx_hash"
		contractSQL = `;
CREATE OR REPLACE TEMP TABLE confirmed_contract_creations AS
SELECT
  t.chain_key,
  t.chain_id,
  t.from_address AS creator,
  r.contract_address,
  t.tx_hash,
  t.block_number,
  t.block_time,
  'CREATE' AS creation_type,
  r.status
FROM normalized_transactions t
JOIN read_parquet(` + sqlString(receiptPath) + `) r
  ON r.chain_key = t.chain_key AND r.chain_id = t.chain_id AND r.tx_hash = t.tx_hash
WHERE t.is_contract_creation_candidate
  AND r.status = 1
  AND r.contract_address IS NOT NULL
  AND r.contract_address <> '';
COPY confirmed_contract_creations TO ` + sqlString(contractPath) + ` (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 250000)`
	}
	activityDir := filepath.Join(settings.DataRoot, "warehouse", "address_activity", "chain="+network.Key, "job="+jobID)
	if err := os.MkdirAll(activityDir, 0755); err != nil {
		return "", 0, "", 0, err
	}
	activityPath := filepath.Join(activityDir, "address-activity-native.parquet")
	activityType := `CASE
WHEN t.method_id IN ('0x095ea7b3', '0xa22cb465') THEN 'APPROVE'
WHEN t.method_id IN ('0x38ed1739', '0x7ff36ab5', '0x18cbafe5') THEN 'SWAP'
WHEN t.input IS NOT NULL AND t.input NOT IN ('', '0x') THEN 'CONTRACT_CALL'
ELSE 'NATIVE_TRANSFER' END`
	activityStatus := `CASE
WHEN t.status = 1 THEN 'SUCCESS'
WHEN t.status = 0 THEN 'FAILED'
ELSE 'UNKNOWN' END`
	outCounterparty := "coalesce(t.to_address, '')"
	if receiptPath != "" {
		activityType = `CASE
WHEN t.is_contract_creation_candidate AND r.status = 1 AND r.contract_address IS NOT NULL THEN 'CONTRACT_CREATE'
WHEN t.method_id IN ('0x095ea7b3', '0xa22cb465') THEN 'APPROVE'
WHEN t.method_id IN ('0x38ed1739', '0x7ff36ab5', '0x18cbafe5') THEN 'SWAP'
WHEN t.input IS NOT NULL AND t.input NOT IN ('', '0x') THEN 'CONTRACT_CALL'
ELSE 'NATIVE_TRANSFER' END`
		outCounterparty = "coalesce(t.to_address, r.contract_address, '')"
		activityStatus = `CASE
WHEN COALESCE(r.status, t.status) = 1 THEN 'SUCCESS'
WHEN COALESCE(r.status, t.status) = 0 THEN 'FAILED'
ELSE 'UNKNOWN' END`
	}
	sqlText := duckDBSettingsSQL(settings) + `;
CREATE OR REPLACE TEMP TABLE normalized_transactions AS
SELECT * FROM read_parquet(` + sqlStringList(transactionPaths) + `, union_by_name=true);
CREATE OR REPLACE TEMP TABLE target_addresses AS
SELECT DISTINCT lower(address) AS address FROM read_csv(` + sqlString(targetPath) + `, header=true, all_varchar=true)` +
		contractSQL + `;
CREATE OR REPLACE TEMP TABLE normalized_activity AS
SELECT
  t.chain_key,
  t.chain_id,
  a.address,
  CASE WHEN a.address = t.from_address THEN ` + outCounterparty + ` ELSE t.from_address END AS counterparty,
  CASE WHEN a.address = t.from_address THEN 'OUT' ELSE 'IN' END AS direction,
  ` + activityType + ` AS activity_type,
  'NATIVE' AS asset_type,
  NULL::VARCHAR AS asset_address,
  ` + sqlString(network.NativeSymbol) + ` AS symbol,
  t.value_raw AS amount_raw,
  t.value_native AS amount,
  t.tx_hash,
  t.block_time,
  t.method_id,
  0::UINTEGER AS trace_depth,
  ` + activityStatus + ` AS status,
  'AWS_TRANSACTION' AS source
FROM normalized_transactions t
JOIN target_addresses a ON a.address = t.from_address OR a.address = t.to_address
` + receiptJoin + `;
COPY normalized_activity TO ` + sqlString(activityPath) + ` (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 250000);
SELECT
  ` + contractCountExpression(receiptPath) + ` AS contract_rows,
  (SELECT COUNT(*) FROM normalized_activity) AS activity_rows`
	rows, err := m.engine.ExecSQLJSON(ctx, sqlText)
	if err != nil {
		return "", 0, "", 0, fmt.Errorf("生成标准化分析表: %w", err)
	}
	if len(rows) == 0 {
		return "", 0, "", 0, errors.New("DuckDB 未返回标准化分析统计")
	}
	return contractPath, numberToInt64(rows[0]["contract_rows"]), activityPath, numberToInt64(rows[0]["activity_rows"]), nil
}

func writeReceiptCSV(writer *csv.Writer, receipt normalize.TransactionReceipt) error {
	return writer.Write([]string{
		receipt.ChainKey,
		strconv.FormatInt(receipt.ChainID, 10),
		receipt.TxHash,
		strconv.FormatUint(receipt.Status, 10),
		receipt.GasUsed,
		receipt.EffectiveGasPrice,
		receipt.ContractAddress,
		strconv.Itoa(receipt.LogsCount),
	})
}

func duckDBSettingsSQL(settings Settings) string {
	return "SET memory_limit=" + sqlString(settings.MemoryLimit) +
		"; SET threads=" + strconv.Itoa(settings.DuckDBThreads) +
		"; SET temp_directory=" + sqlString(filepath.Join(settings.DataRoot, "tmp")) +
		"; SET preserve_insertion_order=false"
}

func sqlStringList(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, sqlString(value))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func contractCountExpression(receiptPath string) string {
	if receiptPath == "" {
		return "0"
	}
	return "(SELECT COUNT(*) FROM confirmed_contract_creations)"
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
