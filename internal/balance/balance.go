// Package balance 实现 Token 余额与资产快照系统：
// Transfer 数据 → Balance Engine → 余额快照 → 资产分析 → 案件报告资产信息。
package balance

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
)

// TokenMeta 是 Token 元数据（decimals 映射）。
type TokenMeta struct {
	Address  string `json:"address"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
}

// knownTokens 内置 BSC 主流 Token 元数据（BSC 主流均为 18 decimals）。
var knownTokens = []TokenMeta{
	{Address: "0x55d398326f99059ff775485246999027b3197955", Symbol: "USDT", Decimals: 18},
	{Address: "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", Symbol: "USDC", Decimals: 18},
	{Address: "0xe9e7cea3dedca5984780bafc599bd69add087d56", Symbol: "BUSD", Decimals: 18},
	{Address: "0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c", Symbol: "WBNB", Decimals: 18},
	{Address: "0x2170ed0880ac9a755fd29b2688956bd959f933f8", Symbol: "ETH", Decimals: 18},
}

// Transfer 是一条 Transfer 记录。
type Transfer struct {
	Token  string
	From   string
	To     string
	Amount string // decimal string（raw，未除 decimals）
	Block  string
	TxHash string
	LogIdx string
	Time   string
}

// BalanceEngine 计算余额。
type BalanceEngine struct {
	engine  *duckdb.Engine
	parquet string

	transfers []Transfer
	loaded    bool
	tokenMeta map[string]TokenMeta

	// 全量余额（惰性构建一次，查询 O(1)）
	allBalances map[string]map[string]*inOut
}

type inOut struct {
	in  *big.Int
	out *big.Int
}

// New 创建余额引擎。
func New(engine *duckdb.Engine, parquetPath string) *BalanceEngine {
	meta := map[string]TokenMeta{}
	for _, t := range knownTokens {
		meta[t.Address] = t
	}
	return &BalanceEngine{
		engine:    engine,
		parquet:   strings.ReplaceAll(parquetPath, "\\", "/"),
		tokenMeta: meta,
	}
}

// TokenMetaOf 返回 token 元数据（未知返回默认 18 且 Symbol 为地址）。
func (b *BalanceEngine) TokenMetaOf(token string) TokenMeta {
	if m, ok := b.tokenMeta[token]; ok {
		return m
	}
	return TokenMeta{Address: token, Symbol: shortAddr(token), Decimals: 18}
}

// Load 加载全部 Transfer 边（惰性一次）。
func (b *BalanceEngine) Load(ctx context.Context) error {
	if b.loaded {
		return nil
	}
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	rows, err := b.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT %[1]s AS f, %[2]s AS t, address AS token, data, transaction_hash, log_index,
			block_number, block_time
		 FROM read_parquet('%[3]s')
		 WHERE topic0 IN ('%[4]s','%[5]s','%[6]s')`, norm1, norm2, b.parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch))
	if err != nil {
		return err
	}
	transfers := make([]Transfer, 0, len(rows))
	for _, r := range rows {
		amount := ""
		if d := fmt.Sprintf("%v", r["data"]); len(d) >= 3 {
			hexPart := strings.TrimPrefix(strings.ToLower(d), "0x")
			if len(hexPart) > 64 {
				hexPart = hexPart[len(hexPart)-64:]
			}
			if n, ok := new(big.Int).SetString(hexPart, 16); ok {
				amount = n.String()
			}
		}
		transfers = append(transfers, Transfer{
			Token:  fmt.Sprintf("%v", r["token"]),
			From:   strings.ToLower(fmt.Sprintf("%v", r["f"])),
			To:     strings.ToLower(fmt.Sprintf("%v", r["t"])),
			Amount: amount,
			Block:  fmt.Sprintf("%v", r["block_number"]),
			TxHash: fmt.Sprintf("%v", r["transaction_hash"]),
			LogIdx: fmt.Sprintf("%v", r["log_index"]),
			Time:   fmt.Sprintf("%v", r["block_time"]),
		})
	}
	b.transfers = transfers
	b.loaded = true
	return nil
}

// Balance 是地址在某 token 上的余额（raw 值）。
type Balance struct {
	Address  string `json:"address"`
	Token    string `json:"token"`
	Symbol   string `json:"symbol"`
	Decimals int    `json:"decimals"`
	Balance  string `json:"balance"` // raw decimal
	InTotal  string `json:"in_total"`
	OutTotal string `json:"out_total"`
}

// ComputeBalances 计算指定地址的余额（全量已加载时 O(1) 查询）。
func (b *BalanceEngine) ComputeBalances(ctx context.Context, addresses []string) (map[string][]Balance, error) {
	if !b.loaded {
		if err := b.Load(ctx); err != nil {
			return nil, err
		}
	}
	if b.allBalances == nil {
		b.buildAllBalances()
	}
	result := map[string][]Balance{}
	for _, addr := range addresses {
		addr = strings.ToLower(addr)
		pairs := b.allBalances[addr]
		var list []Balance
		// 稳定排序的 token 列表
		var tokens []string
		for token := range pairs {
			tokens = append(tokens, token)
		}
		sort.Strings(tokens)
		for _, token := range tokens {
			p := pairs[token]
			if p == nil {
				continue
			}
			bal := new(big.Int).Sub(p.in, p.out)
			meta := b.TokenMetaOf(token)
			list = append(list, Balance{
				Address: addr, Token: token, Symbol: meta.Symbol, Decimals: meta.Decimals,
				Balance: bal.String(), InTotal: p.in.String(), OutTotal: p.out.String(),
			})
		}
		result[addr] = list
	}
	return result, nil
}

// buildAllBalances 一次构建全量余额索引（O(transfers)）。
func (b *BalanceEngine) buildAllBalances() {
	all := map[string]map[string]*inOut{}
	for _, t := range b.transfers {
		amount, ok := new(big.Int).SetString(t.Amount, 10)
		if !ok {
			continue
		}
		if t.From != "" {
			if all[t.From] == nil {
				all[t.From] = map[string]*inOut{}
			}
			if all[t.From][t.Token] == nil {
				all[t.From][t.Token] = &inOut{in: new(big.Int), out: new(big.Int)}
			}
			all[t.From][t.Token].out.Add(all[t.From][t.Token].out, amount)
		}
		if t.To != "" {
			if all[t.To] == nil {
				all[t.To] = map[string]*inOut{}
			}
			if all[t.To][t.Token] == nil {
				all[t.To][t.Token] = &inOut{in: new(big.Int), out: new(big.Int)}
			}
			all[t.To][t.Token].in.Add(all[t.To][t.Token].in, amount)
		}
	}
	b.allBalances = all
}

// ── 资产快照 ──

// Snapshot 是单地址资产快照。
type Snapshot struct {
	Address       string          `json:"address"`
	Balances      []Balance       `json:"balances"`
	HistoryHigh   []HighBalance   `json:"history_high"`
	Timeline      []BalanceChange `json:"timeline"`
	LargeInflows  []LargeFlow     `json:"large_inflows"`
	RapidOutflows []LargeFlow     `json:"rapid_outflows"`
	Risk          AssetRisk       `json:"risk"`
}

// HighBalance 是历史最高余额。
type HighBalance struct {
	Token   string `json:"token"`
	Symbol  string `json:"symbol"`
	Balance string `json:"balance"`
	Block   string `json:"block"`
	Time    string `json:"time"`
}

// BalanceChange 是余额变化点。
type BalanceChange struct {
	Time    string `json:"time"`
	Block   string `json:"block"`
	Balance string `json:"balance"`
	Change  string `json:"change"`
	TxHash  string `json:"tx_hash"`
}

// LargeFlow 是大额资金流动。
type LargeFlow struct {
	Token  string `json:"token"`
	Symbol string `json:"symbol"`
	Amount string `json:"amount"`
	Other  string `json:"counterparty"`
	Block  string `json:"block"`
	TxHash string `json:"tx_hash"`
}

// AssetRisk 是资产风险指标。
type AssetRisk struct {
	AssetValue        string  `json:"asset_value_raw"`
	BalanceChangeRate float64 `json:"balance_change_rate"`
	LiquidationSignal bool    `json:"liquidation_signal"`
	Level             string  `json:"level"`
}

// AssetSummary 是资产摘要（报告用）。
type AssetSummary struct {
	Address  string    `json:"address"`
	Snapshot *Snapshot `json:"snapshot"`
}

// BuildSnapshot 构建单地址资产快照（当前余额/历史最高/时间线/大额变化/风险）。
func (b *BalanceEngine) BuildSnapshot(ctx context.Context, address string) (*Snapshot, error) {
	if !b.loaded {
		if err := b.Load(ctx); err != nil {
			return nil, err
		}
	}
	addr := strings.ToLower(address)
	snap := &Snapshot{Address: addr}
	// 余额
	balMap, err := b.ComputeBalances(ctx, []string{addr})
	if err != nil {
		return nil, err
	}
	snap.Balances = balMap[addr]

	// 该地址相关 Transfer（按 token 分组的时序）
	type entry struct {
		t  Transfer
		in bool
	}
	var events []entry
	for _, t := range b.transfers {
		if t.To == addr {
			events = append(events, entry{t, true})
		} else if t.From == addr {
			events = append(events, entry{t, false})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].t.Block < events[j].t.Block })

	// 时间线 + 历史最高（按 token 独立累计）
	byToken := map[string]*big.Int{}
	type high struct {
		bal   *big.Int
		block string
		time  string
	}
	highs := map[string]*high{}
	for _, ev := range events {
		token := ev.t.Token
		if byToken[token] == nil {
			byToken[token] = new(big.Int)
		}
		amount, ok := new(big.Int).SetString(ev.t.Amount, 10)
		if !ok {
			continue
		}
		if ev.in {
			byToken[token].Add(byToken[token], amount)
		} else {
			byToken[token].Sub(byToken[token], amount)
		}
		cur := new(big.Int).Set(byToken[token])
		snap.Timeline = append(snap.Timeline, BalanceChange{
			Time: ev.t.Time, Block: ev.t.Block, Balance: cur.String(),
			Change: amount.String(), TxHash: ev.t.TxHash,
		})
		if highs[token] == nil || cur.Cmp(highs[token].bal) > 0 {
			highs[token] = &high{bal: cur, block: ev.t.Block, time: ev.t.Time}
		}
	}
	for token, h := range highs {
		meta := b.TokenMetaOf(token)
		snap.HistoryHigh = append(snap.HistoryHigh, HighBalance{
			Token: token, Symbol: meta.Symbol, Balance: h.bal.String(),
			Block: h.block, Time: h.time,
		})
	}
	sort.Slice(snap.HistoryHigh, func(i, j int) bool { return snap.HistoryHigh[i].Symbol < snap.HistoryHigh[j].Symbol })

	// 大额进入（该地址收到的 amount > 该 token 全量 P95）
	thresholds := b.amountThresholds()
	for _, ev := range events {
		if !ev.in {
			continue
		}
		amount, ok := new(big.Int).SetString(ev.t.Amount, 10)
		if !ok {
			continue
		}
		th, has := thresholds[ev.t.Token]
		if has && amount.Cmp(th) >= 0 {
			meta := b.TokenMetaOf(ev.t.Token)
			snap.LargeInflows = append(snap.LargeInflows, LargeFlow{
				Token: ev.t.Token, Symbol: meta.Symbol, Amount: ev.t.Amount,
				Other: ev.t.From, Block: ev.t.Block, TxHash: ev.t.TxHash,
			})
		}
	}
	// 快速清空：大额进入后短时间（同 token 后续流出累计 ≥ 大额的 80%）
	if len(snap.LargeInflows) > 0 {
		for _, li := range snap.LargeInflows {
			afterOut := new(big.Int)
			liAmt, _ := new(big.Int).SetString(li.Amount, 10)
			for _, ev := range events {
				if !ev.in && ev.t.Token == li.Token && ev.t.Block >= li.Block {
					if n, ok := new(big.Int).SetString(ev.t.Amount, 10); ok {
						afterOut.Add(afterOut, n)
					}
				}
			}
			if afterOut.Cmp(new(big.Int).Mul(liAmt, big.NewInt(80))) >= 0 &&
				liAmt.Sign() > 0 && afterOut.Cmp(liAmt) >= 0 {
				snap.RapidOutflows = append(snap.RapidOutflows, li)
				break
			}
		}
	}
	sort.Slice(snap.LargeInflows, func(i, j int) bool {
		ai, _ := new(big.Int).SetString(snap.LargeInflows[i].Amount, 10)
		aj, _ := new(big.Int).SetString(snap.LargeInflows[j].Amount, 10)
		return ai.Cmp(aj) > 0
	})
	if len(snap.LargeInflows) > 5 {
		snap.LargeInflows = snap.LargeInflows[:5]
	}

	// 风险指标
	snap.Risk = b.assetRisk(snap)
	return snap, nil
}

// amountThresholds 计算每 token 的 P95 金额阈值（全量）。
func (b *BalanceEngine) amountThresholds() map[string]*big.Int {
	perToken := map[string][]*big.Int{}
	for _, t := range b.transfers {
		if n, ok := new(big.Int).SetString(t.Amount, 10); ok {
			perToken[t.Token] = append(perToken[t.Token], n)
		}
	}
	out := map[string]*big.Int{}
	for token, list := range perToken {
		sort.Slice(list, func(i, j int) bool { return list[i].Cmp(list[j]) < 0 })
		idx := int(float64(len(list)-1) * 0.95)
		out[token] = list[idx]
	}
	return out
}

// assetRisk 计算资产风险指标（asset_value/balance_change_rate/liquidation_signal）。
func (b *BalanceEngine) assetRisk(snap *Snapshot) AssetRisk {
	totalValue := new(big.Int)
	totalIn := new(big.Int)
	totalOut := new(big.Int)
	for _, bal := range snap.Balances {
		if n, ok := new(big.Int).SetString(bal.Balance, 10); ok {
			totalValue.Add(totalValue, n)
		}
		if n, ok := new(big.Int).SetString(bal.InTotal, 10); ok {
			totalIn.Add(totalIn, n)
		}
		if n, ok := new(big.Int).SetString(bal.OutTotal, 10); ok {
			totalOut.Add(totalOut, n)
		}
	}
	risk := AssetRisk{AssetValue: totalValue.String()}
	// balance_change_rate = out/(in+out)（窗口内流出占比）
	denom := new(big.Int).Add(totalIn, totalOut)
	if denom.Sign() > 0 {
		rate, _ := new(big.Float).Quo(
			new(big.Float).SetInt(totalOut), new(big.Float).SetInt(denom)).Float64()
		risk.BalanceChangeRate = rate
	}
	// liquidation_signal：流出占比 > 80% 且当前余额 < 流入 20%
	if totalIn.Sign() > 0 {
		threshold := new(big.Int).Mul(totalIn, big.NewInt(20))
		if totalOut.Cmp(threshold) >= 0 && totalValue.Sign() <= 0 {
			risk.LiquidationSignal = true
		}
	}
	switch {
	case risk.LiquidationSignal:
		risk.Level = "High"
	case risk.BalanceChangeRate > 0.5:
		risk.Level = "Medium"
	default:
		risk.Level = "Low"
	}
	return risk
}

func shortAddr(addr string) string {
	if len(addr) >= 10 {
		return addr[:6] + "…" + addr[len(addr)-4:]
	}
	return addr
}

// ── 输出 ──

// Export 输出 balances.csv / balance_timeline.csv / asset_summary.json。
func Export(dir string, snapshots []*Snapshot, allBalances map[string][]Balance) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// balances.csv
	bf, err := os.Create(filepath.Join(dir, "balances.csv"))
	if err != nil {
		return err
	}
	bw := csv.NewWriter(bf)
	_ = bw.Write([]string{"address", "token", "symbol", "decimals", "balance", "in_total", "out_total"})
	var addrs []string
	for addr := range allBalances {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		for _, b := range allBalances[addr] {
			_ = bw.Write([]string{b.Address, b.Token, b.Symbol, fmt.Sprintf("%d", b.Decimals),
				b.Balance, b.InTotal, b.OutTotal})
		}
	}
	bw.Flush()
	bf.Close()
	// balance_timeline.csv
	tf, err := os.Create(filepath.Join(dir, "balance_timeline.csv"))
	if err != nil {
		return err
	}
	tw := csv.NewWriter(tf)
	_ = tw.Write([]string{"address", "time", "block", "balance", "change", "tx_hash"})
	for _, s := range snapshots {
		for _, ev := range s.Timeline {
			_ = tw.Write([]string{s.Address, ev.Time, ev.Block, ev.Balance, ev.Change, ev.TxHash})
		}
	}
	tw.Flush()
	tf.Close()
	// asset_summary.json
	summary := map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"addresses":    len(snapshots),
	}
	byAddr := map[string]*Snapshot{}
	for _, s := range snapshots {
		byAddr[s.Address] = s
	}
	summary["snapshots"] = byAddr
	data, _ := json.MarshalIndent(summary, "", "  ")
	return os.WriteFile(filepath.Join(dir, "asset_summary.json"), data, 0644)
}
