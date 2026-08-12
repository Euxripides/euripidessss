package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/etl/backend/internal/explorer"
	"github.com/etl/backend/internal/financialanalytics"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

var (
	explorerSearchAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	explorerSearchHash    = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	explorerSearchBlock   = regexp.MustCompile(`^[0-9]{1,20}$`)
)

const (
	zeroAddress = "0x0000000000000000000000000000000000000000"
	deadAddress = "0x000000000000000000000000000000000000dead"
)

func registerExplorerIntelligenceRoutes(api *gin.RouterGroup) {
	api.GET("/v2/explorer/search", handleExplorerIntelligenceSearch)
	api.GET("/v2/explorer/:chain/home", handleExplorerIntelligenceHome)
	api.GET("/v2/explorer/:chain/address/:address/header", handleExplorerIntelligenceHeader)
	api.GET("/v2/explorer/:chain/block/:block", handleExplorerIntelligenceBlock)
}

func handleExplorerIntelligenceSearch(c *gin.Context) {
	if clickHouseClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Explorer search is unavailable"})
		return
	}
	chainID, ok := chainIDFromKey(defaultString(c.Query("chain"), "bsc"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "unsupported chain"})
		return
	}
	raw := strings.TrimSpace(c.Query("q"))
	if raw == "" || len([]rune(raw)) > 96 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "search query must contain 1-96 characters"})
		return
	}
	ctx := c.Request.Context()
	items := make([]gin.H, 0, 12)
	switch {
	case explorerSearchAddress.MatchString(raw):
		address := strings.ToLower(raw)
		if name, system := explorerSystemAddress(address); system {
			items = append(items, gin.H{"kind": "ADDRESS", "title": name, "subtitle": "System address", "value": address, "chain_id": chainID, "verified": true})
			break
		}
		rows, err := clickHouseClient.QueryJSON(ctx, fmt.Sprintf(`SELECT kind,title,subtitle,verified,logo_uri FROM (
SELECT 'TOKEN' kind,if(symbol!='',symbol,contract_address) title,if(name!='',name,'Unknown Token') subtitle,is_verified verified,logo_uri FROM onchain.token_metadata_registry FINAL WHERE chain_id=%d AND contract_address='%s'
UNION ALL SELECT 'CONTRACT',if(contract_name!='',contract_name,'Contract'),contract_address,is_verified,'' FROM onchain.contracts FINAL WHERE chain_id=%d AND contract_address='%s'
UNION ALL SELECT 'ADDRESS',if(label_name!='',label_name,'Address'),address,confidence IN ('HIGH','VERIFIED'),'' FROM onchain.address_labels FINAL WHERE chain_id=%d AND address='%s'
) ORDER BY kind LIMIT 8`, chainID, address, chainID, address, chainID, address))
		if err != nil {
			writeExplorerIntelligenceError(c, err)
			return
		}
		for _, row := range rows {
			items = append(items, searchItem(row, address, chainID))
		}
		if len(items) == 0 {
			items = append(items, gin.H{"kind": "ADDRESS", "title": address, "subtitle": "Unlabeled EVM address", "value": address, "chain_id": chainID, "verified": false})
		}
	case explorerSearchHash.MatchString(raw):
		hash := strings.ToLower(raw)
		items = append(items, gin.H{"kind": "TRANSACTION", "title": hash, "subtitle": "Transaction hash or block hash", "value": hash, "chain_id": chainID})
	case explorerSearchBlock.MatchString(raw):
		block, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid block number"})
			return
		}
		items = append(items, gin.H{"kind": "BLOCK", "title": strconv.FormatUint(block, 10), "subtitle": "Block number", "value": strconv.FormatUint(block, 10), "chain_id": chainID})
	default:
		query, valid := explorerSearchText(raw)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"detail": "search text contains unsupported characters"})
			return
		}
		rows, err := clickHouseClient.QueryJSON(ctx, fmt.Sprintf(`SELECT kind,title,subtitle,value,verified,logo_uri FROM (
SELECT 'TOKEN' kind,symbol title,name subtitle,contract_address value,is_verified verified,logo_uri FROM onchain.token_metadata_registry FINAL WHERE chain_id=%d AND (positionCaseInsensitiveUTF8(symbol,'%s')>0 OR positionCaseInsensitiveUTF8(name,'%s')>0)
UNION ALL SELECT 'ENTITY',entity_name,entity_type,toString(entity_id),is_verified,'' FROM onchain.entity_registry FINAL WHERE positionCaseInsensitiveUTF8(entity_name,'%s')>0
UNION ALL SELECT 'LABEL',label_name,label_type,address,confidence IN ('HIGH','VERIFIED'),'' FROM onchain.address_labels FINAL WHERE chain_id=%d AND positionCaseInsensitiveUTF8(label_name,'%s')>0
) ORDER BY verified DESC,kind,title LIMIT 12`, chainID, query, query, query, chainID, query))
		if err != nil {
			writeExplorerIntelligenceError(c, err)
			return
		}
		for _, row := range rows {
			item := searchItem(row, stringField(row, "value"), chainID)
			items = append(items, item)
		}
	}
	c.JSON(http.StatusOK, gin.H{"query": raw, "chain_id": chainID, "items": items})
}

func handleExplorerIntelligenceHome(c *gin.Context) {
	chainID, ok := canonicalScope(c)
	if !ok {
		return
	}
	key := strconv.FormatUint(uint64(chainID), 10)
	value, err := explorerHomeFlight.Do(key, func() (any, error) {
		return computeExplorerHome(c.Request.Context(), chainID)
	})
	if err != nil {
		writeExplorerIntelligenceError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}

func computeExplorerHome(ctx context.Context, chainID uint32) (gin.H, error) {
	rows, err := clickHouseClient.QueryJSON(ctx, fmt.Sprintf(`SELECT
(SELECT max(block_number) FROM onchain.chain_blocks FINAL WHERE chain_id=%d) latest_block,
(SELECT count() FROM onchain.chain_transactions FINAL WHERE chain_id=%d) transaction_count,
(SELECT count() FROM onchain.token_transfers FINAL WHERE chain_id=%d) token_transfer_count,
(SELECT count() FROM onchain.data_coverage FINAL WHERE chain_id=%d AND status='COMPLETE') complete_ranges`, chainID, chainID, chainID, chainID))
	if err != nil || len(rows) != 1 {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("explorer home summary query returned no rows")
	}
	latest, latestErr := clickHouseClient.QueryJSON(ctx, fmt.Sprintf(`SELECT tx_hash,block_number,block_time,from_address,to_address,method_name,status,toString(value_decimal) amount,native_symbol FROM onchain.chain_transactions FINAL WHERE chain_id=%d ORDER BY block_time DESC,block_number DESC,transaction_index DESC LIMIT 10`, chainID))
	if latestErr != nil {
		return nil, latestErr
	}
	large, largeErr := clickHouseClient.QueryJSON(ctx, fmt.Sprintf(`SELECT tx_hash,block_number,block_time,address,counterparty_address,direction,token_symbol,toString(amount) amount,toString(historical_value_usdt) historical_value_usdt,toString(historical_value_usdt) usd_value FROM
(SELECT a.*,multiIf(isNotNull(a.usd_value),a.usd_value,a.token_address='0x55d398326f99059ff775485246999027b3197955',CAST(a.amount AS Nullable(Decimal(38,18))),q.token_address!='',CAST(a.amount*if(q.vwap>0,q.vwap,q.close) AS Nullable(Decimal(38,18))),p.token_address!='' AND dateDiff('second',p.timestamp_bucket,a.block_time)<=86400,CAST(a.amount*p.price_usd AS Nullable(Decimal(38,18))),a.token_address IN ('0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d','0xe9e7cea3dedca5984780bafc599bd69add087d56','0xc5f0f7b66764f6ec8c8dff7ba683102295e16409'),CAST(a.amount AS Nullable(Decimal(38,18))),CAST(NULL AS Nullable(Decimal(38,18)))) historical_value_usdt
FROM onchain.address_activity AS a FINAL
LEFT JOIN (SELECT chain_id,token_address,minute,vwap,close FROM onchain.token_price_1m FINAL WHERE chain_id=%d) q ON a.chain_id=q.chain_id AND if(a.token_address='',concat('native:',toString(a.chain_id)),a.token_address)=q.token_address AND toStartOfMinute(a.block_time)=q.minute
ASOF LEFT JOIN (SELECT * FROM onchain.token_prices FINAL WHERE chain_id=%d ORDER BY chain_id,token_address,timestamp_bucket) p ON a.chain_id=p.chain_id AND if(a.token_address='',concat('native:',toString(a.chain_id)),a.token_address)=p.token_address AND a.block_time>=p.timestamp_bucket WHERE a.chain_id=%d) t
WHERE t.historical_value_usdt>=100000 ORDER BY t.historical_value_usdt DESC,block_time DESC,block_number DESC LIMIT 10`, chainID, chainID, chainID))
	if largeErr != nil {
		return nil, largeErr
	}
	return gin.H{"chain_id": chainID, "coverage_ranges": rows[0]["complete_ranges"], "latest_block": rows[0]["latest_block"], "transaction_count": rows[0]["transaction_count"], "token_transfer_count": rows[0]["token_transfer_count"], "latest_transactions": latest, "large_transfers": large}, nil
}

func handleExplorerIntelligenceHeader(c *gin.Context) {
	chainID, ok := canonicalScope(c)
	if !ok {
		return
	}
	address := strings.ToLower(strings.TrimSpace(c.Param("address")))
	if !explorerSearchAddress.MatchString(address) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid EVM address"})
		return
	}
	summary, summaryErr := clickHouseExplorer.GetAddressSummary(c.Request.Context(), chainID, address)
	if summaryErr != nil && !errors.Is(summaryErr, explorer.ErrNotFound) {
		writeExplorerIntelligenceError(c, summaryErr)
		return
	}
	if errors.Is(summaryErr, explorer.ErrNotFound) {
		summary.ChainID = chainID
		summary.Address = address
		summary.AddressType = "UNKNOWN"
	}
	systemName, isSystem := explorerSystemAddress(address)
	if isSystem {
		summary.AddressType = "SYSTEM"
	}
	labels, err := clickHouseClient.QueryJSON(c.Request.Context(), fmt.Sprintf(`SELECT l.label_name,l.label_type,toString(l.entity_id) entity_id,l.entity_role,l.source,l.confidence,e.entity_name,e.entity_type,e.is_verified FROM onchain.address_labels AS l FINAL LEFT JOIN onchain.entity_registry AS e FINAL ON l.entity_id=e.entity_id WHERE l.chain_id=%d AND l.address='%s' ORDER BY e.is_verified DESC,l.last_verified DESC LIMIT 12`, chainID, address))
	if err != nil {
		writeExplorerIntelligenceError(c, err)
		return
	}
	if isSystem {
		labels = append([]map[string]any{{"label_name": systemName, "label_type": "SYSTEM", "confidence": "VERIFIED", "is_verified": true}}, labels...)
	}
	coverage, err := clickHouseClient.QueryJSON(c.Request.Context(), fmt.Sprintf(`SELECT min(from_time) from_time,max(to_time) to_time,sum(row_count) row_count,countIf(status='COMPLETE') complete_ranges,count() ranges FROM onchain.data_coverage FINAL WHERE chain_id=%d AND (subject='%s' OR subject='')`, chainID, address))
	if err != nil {
		writeExplorerIntelligenceError(c, err)
		return
	}
	financial, financialErr := financialAnalyticsV1.FinancialSummary(c.Request.Context(), financialanalytics.Query{ChainID: chainID, Address: address, Window: financialanalytics.Window30D, LargeThresholdUSD: "100000", EntityMinConfidence: "HIGH", Limit: 10})
	financialAvailable := financialErr == nil
	coverageState := "NO_DATA"
	if !summary.FirstSeenTime.IsZero() {
		coverageState = "PARTIAL"
	}
	if len(coverage) == 1 && uintField(coverage[0], "ranges") > 0 && uintField(coverage[0], "complete_ranges") == uintField(coverage[0], "ranges") {
		coverageState = "COMPLETE"
	}
	c.JSON(http.StatusOK, gin.H{
		"identity":  gin.H{"chain_id": chainID, "address": address, "address_type": defaultString(summary.AddressType, "UNKNOWN")},
		"labels":    labels,
		"balances":  gin.H{"available": false, "items": []any{}, "estimated_portfolio_usd": nil},
		"coverage":  gin.H{"status": coverageState, "detail": firstRow(coverage)},
		"summary":   explorerSummaryDTO(summary),
		"financial": gin.H{"available": financialAvailable, "data": nullableValue(financialAvailable, financial)},
	})
}

func explorerSystemAddress(address string) (string, bool) {
	switch strings.ToLower(address) {
	case zeroAddress:
		return "Zero Address", true
	case deadAddress:
		return "Dead Address", true
	default:
		return "", false
	}
}

func explorerTimeValue(value time.Time) any {
	if value.IsZero() || value.Year() <= 1970 {
		return nil
	}
	return value.UTC()
}

func explorerSummaryDTO(summary explorer.AddressSummary) gin.H {
	return gin.H{
		"chain_id": summary.ChainID, "address": summary.Address, "address_type": summary.AddressType,
		"first_seen_time": explorerTimeValue(summary.FirstSeenTime), "last_seen_time": explorerTimeValue(summary.LastSeenTime),
		"transaction_count": summary.TransactionCount, "incoming_transaction_count": summary.IncomingTransactionCount,
		"outgoing_transaction_count": summary.OutgoingTransactionCount, "token_transfer_count": summary.TokenTransferCount,
		"internal_transaction_count": summary.InternalTransactionCount, "nft_transfer_count": summary.NFTTransferCount,
		"contract_created_count": summary.ContractCreatedCount, "unique_counterparty_count": summary.UniqueCounterpartyCount,
		"native_received": summary.NativeReceived, "native_sent": summary.NativeSent, "native_netflow": summary.NativeNetflow,
		"usd_received": summary.USDReceived, "usd_sent": summary.USDSent, "usd_netflow": summary.USDNetflow,
		"active_days": summary.ActiveDays, "max_single_in_usd": summary.MaxSingleInUSD, "max_single_out_usd": summary.MaxSingleOutUSD,
		"top_counterparty": summary.TopCounterparty, "cex_interaction_count": summary.CEXInteractionCount,
		"dex_interaction_count": summary.DEXInteractionCount, "bridge_interaction_count": summary.BridgeInteractionCount,
		"risk_score": summary.RiskScore, "updated_at": explorerTimeValue(summary.UpdatedAt),
	}
}

func handleExplorerIntelligenceBlock(c *gin.Context) {
	chainID, ok := canonicalScope(c)
	if !ok {
		return
	}
	block, err := strconv.ParseUint(c.Param("block"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid block number"})
		return
	}
	rows, err := clickHouseClient.QueryJSON(c.Request.Context(), fmt.Sprintf(`SELECT chain_id,block_number,block_hash,parent_hash,block_time,miner_address,gas_limit,gas_used,toString(base_fee_per_gas) base_fee_per_gas,tx_count,size_bytes,source_provider FROM onchain.chain_blocks FINAL WHERE chain_id=%d AND block_number=%d ORDER BY ingested_at DESC LIMIT 1`, chainID, block))
	if err != nil {
		writeExplorerIntelligenceError(c, err)
		return
	}
	if len(rows) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"detail": "block not found"})
		return
	}
	c.JSON(http.StatusOK, rows[0])
}

func explorerSearchText(value string) (string, bool) {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || strings.ContainsRune("._-", r) {
			continue
		}
		return "", false
	}
	return strings.TrimSpace(value), true
}

func searchItem(row map[string]any, value string, chainID uint32) gin.H {
	return gin.H{"kind": stringField(row, "kind"), "title": stringField(row, "title"), "subtitle": stringField(row, "subtitle"), "value": value, "chain_id": chainID, "verified": boolField(row, "verified"), "logo_uri": stringField(row, "logo_uri")}
}

func stringField(row map[string]any, key string) string {
	if value, ok := row[key]; ok && value != nil {
		return fmt.Sprint(value)
	}
	return ""
}

func uintField(row map[string]any, key string) uint64 {
	value, _ := strconv.ParseUint(stringField(row, key), 10, 64)
	return value
}

func boolField(row map[string]any, key string) bool {
	value := strings.ToLower(stringField(row, key))
	return value == "1" || value == "true"
}

func firstRow(rows []map[string]any) any {
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

func nullableValue(ok bool, value any) any {
	if !ok {
		return nil
	}
	return value
}

func writeExplorerIntelligenceError(c *gin.Context, err error) {
	if err != nil {
		log.Warn().Err(err).Msg("explorer_intelligence_query_failed")
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Explorer Intelligence query failed"})
}
