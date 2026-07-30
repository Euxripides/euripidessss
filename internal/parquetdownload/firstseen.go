package parquetdownload

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/etl/backend/internal/chain"
)

// ── FirstSeen types ──

type FirstSeenStatus string

const (
	FirstSeenFound                  FirstSeenStatus = "found"
	FirstSeenPartial                FirstSeenStatus = "partial"
	FirstSeenNotFound               FirstSeenStatus = "not_found"
	FirstSeenTemporarilyUnavailable FirstSeenStatus = "temporarily_unavailable"
	FirstSeenFailed                 FirstSeenStatus = "failed"
)

// FirstSeen coverage labels (distinct from Job file-coverage constants)
const (
	FSCoverageFull    = "FULL"
	FSCoveragePartial = "PARTIAL"
	FSCoverageUnknown = "UNKNOWN"
)

type FirstSeenResponse struct {
	ChainID         string         `json:"chain_id"`
	Address         string         `json:"address"`
	AddressType     string         `json:"address_type"`
	FirstSeenBlock  *int64         `json:"first_seen_block,omitempty"`
	FirstSeenTime   *string        `json:"first_seen_time,omitempty"`
	FirstSeenSource string         `json:"first_seen_source,omitempty"`
	CoverageStatus  string         `json:"coverage_status"`
	Status          FirstSeenStatus `json:"status"`
	Provider        string         `json:"provider,omitempty"`
	ErrorMessage    string         `json:"error_message,omitempty"`
}

// ── FirstSeen query ──

var errFirstSeenNotFound = errors.New("该地址在当前链上未发现活动记录")

func (m *Manager) queryFirstSeen(ctx context.Context, chainKey, address string) (*FirstSeenResponse, error) {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return nil, fmt.Errorf("不支持的链: %w", err)
	}
	chainKey = network.Key

	// 1. Try DuckDB cache
	cached, err := m.cachedFirstSeen(ctx, chainKey, address)
	if err == nil && cached != nil {
		return cached, nil
	}

	// 2. Determine address type: EOA or Contract
	addrType := m.detectAddressType(ctx, chainKey, address)

	// 3. Query earliest activity
	resp, err := m.computeFirstSeen(ctx, chainKey, address, addrType)
	if err != nil {
		if errors.Is(err, errFirstSeenNotFound) {
			return &FirstSeenResponse{
				ChainID:        chainKey,
				Address:        address,
				AddressType:    addrType,
				CoverageStatus: FSCoverageUnknown,
				Status:         FirstSeenNotFound,
			}, nil
		}
		return &FirstSeenResponse{
			ChainID:      chainKey,
			Address:      address,
			AddressType:  addrType,
			Status:       FirstSeenFailed,
			ErrorMessage: err.Error(),
		}, nil
	}

	// 4. Cache result
	_ = m.cacheFirstSeen(ctx, chainKey, address, addrType, resp)

	return resp, nil
}

func (m *Manager) detectAddressType(ctx context.Context, chainKey, address string) string {
	network, err := chain.Resolve(chainKey)
	if err != nil {
		return "UNKNOWN"
	}
	m.mu.RLock()
	manager := m.rpcManager
	m.mu.RUnlock()
	if manager != nil && manager.HasConfigured(network.Key) {
		enriched, enrichErr := manager.Address(ctx, network.Key, address, false)
		if enrichErr == nil {
			if enriched.AddressType == "CONTRACT" {
				return "CONTRACT"
			}
			if enriched.AddressType == "EOA" {
				return "EOA"
			}
		}
	}
	return "EOA"
}

func (m *Manager) computeFirstSeen(ctx context.Context, chainKey, address, addrType string) (*FirstSeenResponse, error) {
	// Determine coverage status by checking warehouse data availability
	coverage := m.detectCoverage(chainKey)

	// For Contract: check contract creation first
	if addrType == "CONTRACT" {
		if resp := m.findContractCreation(ctx, chainKey, address, coverage); resp != nil {
			return resp, nil
		}
	}

	// For EOA or Contract fallback: find earliest activity
	return m.findEarliestActivity(ctx, chainKey, address, addrType, coverage)
}

func (m *Manager) detectCoverage(chainKey string) string {
	paths := m.warehouseFiles("address_activity", chainKey)
	if len(paths) == 0 {
		return FSCoverageUnknown
	}
	return FSCoveragePartial // default; could be refined by checking job coverage
}

func (m *Manager) findContractCreation(ctx context.Context, chainKey, address, coverage string) *FirstSeenResponse {
	// Check traces warehouse for CREATE / CREATE2
	tracePaths := m.warehouseFiles("traces", chainKey)
	if len(tracePaths) > 0 {
		rows, err := m.engine.ExecSQLJSON(ctx,
			`SELECT block_number, block_time, trace_type FROM read_parquet(`+sqlStringList(tracePaths)+`, union_by_name=true)
WHERE chain_key = `+sqlString(chainKey)+` AND to_address = `+sqlString(address)+`
  AND trace_type IN ('create','create2')
ORDER BY block_number LIMIT 1`)
		if err == nil && len(rows) > 0 {
			return buildFirstSeen(chainKey, address, "CONTRACT", "SQD_TRACE_CREATE", coverage, rows[0])
		}
	}
	return nil
}

func (m *Manager) findEarliestActivity(ctx context.Context, chainKey, address, addrType, coverage string) (*FirstSeenResponse, error) {
	var minBlock *int64
	var minTime *string
	var bestSource string

	// Query each activity table for earliest block
	queries := []struct {
		table  string
		where  string
		source string
	}{
		{"address_activity", `chain_key = ` + sqlString(chainKey) + ` AND address = ` + sqlString(address), "SQD_ACTIVITY"},
		{"traces", `chain_key = ` + sqlString(chainKey) + ` AND from_address = ` + sqlString(address), "SQD_TRACE_FROM"},
		{"traces", `chain_key = ` + sqlString(chainKey) + ` AND to_address = ` + sqlString(address), "SQD_TRACE_TO"},
		{"token_transfers", `chain_key = ` + sqlString(chainKey) + ` AND from_address = ` + sqlString(address), "TOKEN_TRANSFER_FROM"},
		{"token_transfers", `chain_key = ` + sqlString(chainKey) + ` AND to_address = ` + sqlString(address), "TOKEN_TRANSFER_TO"},
	}

	for _, q := range queries {
		paths := m.warehouseFiles(q.table, chainKey)
		if len(paths) == 0 {
			continue
		}
		rows, err := m.engine.ExecSQLJSON(ctx,
			`SELECT block_number, block_time FROM read_parquet(`+sqlStringList(paths)+`, union_by_name=true)
WHERE `+q.where+` ORDER BY block_number LIMIT 1`)
		if err != nil || len(rows) == 0 {
			continue
		}
		bn := parseBlock(rows[0]["block_number"])
		bt := parseTime(rows[0]["block_time"])
		if bn != nil && (minBlock == nil || *bn < *minBlock) {
			minBlock = bn
			minTime = bt
			bestSource = q.source
		}
	}

	if minBlock == nil {
		return nil, errFirstSeenNotFound
	}

	return &FirstSeenResponse{
		ChainID:         chainKey,
		Address:         address,
		AddressType:     addrType,
		FirstSeenBlock:  minBlock,
		FirstSeenTime:   minTime,
		FirstSeenSource: bestSource,
		CoverageStatus:  coverage,
		Status:          firstSeenStatusForCoverage(coverage),
		Provider:        "LOCAL",
	}, nil
}

// ── Cache ──

func (m *Manager) cachedFirstSeen(ctx context.Context, chainKey, address string) (*FirstSeenResponse, error) {
	// Check if address_first_seen table exists in DuckDB
	rows, err := m.engine.ExecSQLJSON(ctx,
		`SELECT * FROM address_first_seen
WHERE chain_id = `+sqlString(chainKey)+` AND address = `+sqlString(address)+`
ORDER BY updated_at DESC LIMIT 1`)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("cache miss: %w", err)
	}
	return rowToFirstSeen(rows[0]), nil
}

func (m *Manager) cacheFirstSeen(ctx context.Context, chainKey, address, addrType string, resp *FirstSeenResponse) error {
	// Ensure table exists
	_, _ = m.engine.ExecSQL(ctx, `CREATE TABLE IF NOT EXISTS address_first_seen (
		chain_id VARCHAR,
		address VARCHAR,
		address_type VARCHAR,
		first_seen_block BIGINT,
		first_seen_time VARCHAR,
		first_seen_source VARCHAR,
		coverage_status VARCHAR,
		query_status VARCHAR,
		provider VARCHAR,
		error_message VARCHAR,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		PRIMARY KEY (chain_id, address)
	)`)

	now := time.Now().UTC().Format(time.RFC3339)
	stmt := fmt.Sprintf(
		`INSERT OR REPLACE INTO address_first_seen VALUES (
			%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s
		)`,
		sqlString(chainKey),
		sqlString(address),
		sqlString(addrType),
		sqlNullableInt(resp.FirstSeenBlock),
		sqlNullableString(resp.FirstSeenTime),
		sqlString(resp.FirstSeenSource),
		sqlString(string(resp.CoverageStatus)),
		sqlString(string(resp.Status)),
		sqlString(resp.Provider),
		sqlString(resp.ErrorMessage),
		sqlString(now),
		sqlString(now),
	)
	_, _ = m.engine.ExecSQL(ctx, stmt)
	return nil
}

// ── Effective date range resolver ──

type EffectiveDateRange struct {
	StartTime       string `json:"start_time"`
	EndTime         string `json:"end_time"`
	StartTimeSource string `json:"start_time_source"` // "FIRST_SEEN" or "USER_SELECTED"
}

func (m *Manager) resolveEffectiveDateRange(
	ctx context.Context,
	chainKey, address string,
	useFirstSeen bool,
	requestedStart, requestedEnd string,
) (*EffectiveDateRange, error) {
	now := time.Now().UTC()

	// Resolve end time
	endTime := now.Format(time.RFC3339)
	if requestedEnd != "" {
		if t, err := time.Parse(time.RFC3339, requestedEnd); err == nil {
			endTime = t.UTC().Format(time.RFC3339)
		}
	}

	if !useFirstSeen {
		// Manual mode: start_time is required
		if requestedStart == "" {
			return nil, errors.New("手动模式下必须提供开始时间")
		}
		start, err := time.Parse(time.RFC3339, requestedStart)
		if err != nil {
			return nil, fmt.Errorf("开始时间格式错误: %w", err)
		}
		startTime := start.UTC().Format(time.RFC3339)
		if startTime >= endTime {
			return nil, errors.New("开始时间必须早于结束时间")
		}
		return &EffectiveDateRange{
			StartTime:       startTime,
			EndTime:         endTime,
			StartTimeSource: "USER_SELECTED",
		}, nil
	}

	// First-seen mode
	resp, err := m.queryFirstSeen(ctx, chainKey, address)
	if err != nil {
		return nil, err
	}
	if resp.Status != FirstSeenFound && resp.Status != FirstSeenPartial {
		return nil, fmt.Errorf("无法解析首次出现时间 (status=%s)", resp.Status)
	}
	if resp.FirstSeenTime == nil || *resp.FirstSeenTime == "" {
		return nil, errors.New("首次出现时间为空")
	}

	startTime := *resp.FirstSeenTime
	source := "FIRST_SEEN_" + resp.FirstSeenSource
	return &EffectiveDateRange{
		StartTime:       startTime,
		EndTime:         endTime,
		StartTimeSource: source,
	}, nil
}

// ── Helpers ──

func buildFirstSeen(chainKey, address, addrType, source, coverage string, row map[string]any) *FirstSeenResponse {
	bn := parseBlock(row["block_number"])
	bt := parseTime(row["block_time"])
	return &FirstSeenResponse{
		ChainID:         chainKey,
		Address:         address,
		AddressType:     addrType,
		FirstSeenBlock:  bn,
		FirstSeenTime:   bt,
		FirstSeenSource: source,
		CoverageStatus:  coverage,
		Status:          firstSeenStatusForCoverage(coverage),
		Provider:        "LOCAL",
	}
}

func firstSeenStatusForCoverage(c string) FirstSeenStatus {
	switch c {
	case FSCoverageFull:
		return FirstSeenFound
	case FSCoveragePartial:
		return FirstSeenPartial
	default:
		return FirstSeenFound
	}
}

func rowToFirstSeen(row map[string]any) *FirstSeenResponse {
	return &FirstSeenResponse{
		ChainID:         fmt.Sprint(row["chain_id"]),
		Address:         fmt.Sprint(row["address"]),
		AddressType:     fmt.Sprint(row["address_type"]),
		FirstSeenBlock:  parseBlock(row["first_seen_block"]),
		FirstSeenTime:   parseTimeString(row["first_seen_time"]),
		FirstSeenSource: fmt.Sprint(row["first_seen_source"]),
		CoverageStatus:  fmt.Sprint(row["coverage_status"]),
		Status:          FirstSeenStatus(fmt.Sprint(row["query_status"])),
		Provider:        fmt.Sprint(row["provider"]),
		ErrorMessage:    fmt.Sprint(row["error_message"]),
	}
}

func parseBlock(value any) *int64 {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case float64:
		n := int64(v)
		return &n
	case int64:
		return &v
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil
		}
		return &n
	}
	return nil
}

func parseTime(value any) *string {
	if value == nil {
		return nil
	}
	s := fmt.Sprint(value)
	if s == "" || s == "<nil>" {
		return nil
	}
	return &s
}

func parseTimeString(value any) *string {
	return parseTime(value)
}

func sqlNullableInt(v *int64) string {
	if v == nil {
		return "NULL"
	}
	return strconv.FormatInt(*v, 10)
}

func sqlNullableString(v *string) string {
	if v == nil || *v == "" {
		return "NULL"
	}
	return sqlString(*v)
}
