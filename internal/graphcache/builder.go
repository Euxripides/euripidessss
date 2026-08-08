package graphcache

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
)

// FlowSource 提供地址资金流与画像（analyticsapi.Service 满足该接口）。
type FlowSource interface {
	Flows(ctx context.Context, address string, token string) ([]analyticsapi.FlowEdge, error)
	Profile(ctx context.Context, address string) (*analyticsapi.Profile, error)
}

// CoverageInfo 是覆盖查询结果。
type CoverageInfo struct {
	Ratio         float64 `json:"ratio"`
	Full          bool    `json:"full"`
	Certification string  `json:"certification,omitempty"`
}

// CoverageQuerier 查询本地数据覆盖（smartdownload Coverage Index 适配）。
type CoverageQuerier interface {
	QueryCoverage(chainKey, address, dataset string, from, to uint64) CoverageInfo
}

// Builder 从本地资金流数据聚合图扩展结果。
type Builder struct {
	flows    FlowSource
	coverage CoverageQuerier
	now      func() time.Time
}

// NewBuilder 创建构建器。flows 为 nil 时构建返回错误。
func NewBuilder(flows FlowSource, coverage CoverageQuerier) *Builder {
	return &Builder{flows: flows, coverage: coverage, now: time.Now}
}

// Build 聚合指定地址的图扩展结果（直接对手聚合，深度作为键语义保留）。
func (b *Builder) Build(ctx context.Context, key Key) (*Result, error) {
	key = key.Normalized()
	if b.flows == nil {
		return nil, fmt.Errorf("graphcache: 分析数据源不可用")
	}
	flows, err := b.flows.Flows(ctx, key.Address, key.TokenFilter)
	if err != nil {
		return nil, fmt.Errorf("graphcache: 资金流查询失败: %w", err)
	}
	type agg struct {
		in, out       *big.Int
		txCount       int64
		firstSeen     string
		lastSeen      string
		token         string
		counterparty  string
		direction     string
	}
	byEdge := map[string]*agg{}
	totalIn := big.NewInt(0)
	totalOut := big.NewInt(0)
	for _, f := range flows {
		dir := strings.ToUpper(f.Direction)
		switch dir {
		case "INCOMING":
			dir = string(DirectionIn)
		case "OUTGOING":
			dir = string(DirectionOut)
		}
		if key.Direction != "" && key.Direction != string(DirectionAll) && dir != key.Direction {
			continue
		}
		token := strings.ToLower(f.Token)
		cp := strings.ToLower(f.Counterparty)
		if cp == "" || cp == key.Address {
			continue
		}
		eKey := cp + "|" + dir + "|" + token
		a := byEdge[eKey]
		if a == nil {
			a = &agg{in: big.NewInt(0), out: big.NewInt(0), counterparty: cp, direction: dir, token: token}
			byEdge[eKey] = a
		}
		a.txCount++
		if f.Block != "" {
			if a.firstSeen == "" || f.Block < a.firstSeen {
				a.firstSeen = f.Block
			}
			if a.lastSeen == "" || f.Block > a.lastSeen {
				a.lastSeen = f.Block
			}
		}
		if amt, ok := parseBig(f.Amount); ok {
			if dir == string(DirectionIn) {
				a.in.Add(a.in, amt)
				totalIn.Add(totalIn, amt)
			} else {
				a.out.Add(a.out, amt)
				totalOut.Add(totalOut, amt)
			}
		}
	}
	res := &Result{
		Key:              key,
		TotalInflow:      totalIn.String(),
		TotalOutflow:     totalOut.String(),
		CounterpartyCount: len(byEdge),
		GeneratedAt:      b.now(),
		Source:           "rebuilt",
	}
	nodeSet := map[string]*Node{}
	for _, a := range byEdge {
		res.Edges = append(res.Edges, Edge{
			Counterparty: a.counterparty,
			Direction:    a.direction,
			Token:        a.token,
			Inflow:       a.in.String(),
			Outflow:      a.out.String(),
			TxCount:      a.txCount,
			FirstSeen:    a.firstSeen,
			LastSeen:     a.lastSeen,
		})
		for _, addr := range []string{key.Address, a.counterparty} {
			if _, ok := nodeSet[addr]; !ok {
				nodeSet[addr] = &Node{Address: addr}
			}
		}
	}
	// 覆盖度：取请求数据集中最小的覆盖比例（任一数据集缺口都影响秒开）。
	coverage := 1.0
	cert := ""
	if b.coverage != nil && len(key.DatasetSet) > 0 {
		for _, ds := range key.DatasetSet {
			ci := b.coverage.QueryCoverage("", key.Address, ds, key.FromBlock, key.ToBlock)
			if ci.Ratio < coverage {
				coverage = ci.Ratio
			}
			if cert == "" {
				cert = ci.Certification
			}
		}
	}
	res.Coverage = coverage
	res.Certification = cert
	if p, err := b.flows.Profile(ctx, key.Address); err == nil && p != nil {
		if n, ok := nodeSet[key.Address]; ok {
			n.Type = "address"
			n.TxCount = p.TransactionCount
			n.FirstSeen = p.FirstActivityTime
			n.LastSeen = p.LastActivityTime
			n.Inflow = fmt.Sprintf("%d", p.TotalIn)
			n.Outflow = fmt.Sprintf("%d", p.TotalOut)
		}
	}
	for _, n := range nodeSet {
		res.Nodes = append(res.Nodes, *n)
	}
	sort.Slice(res.Edges, func(i, j int) bool {
		if res.Edges[i].TxCount != res.Edges[j].TxCount {
			return res.Edges[i].TxCount > res.Edges[j].TxCount
		}
		return res.Edges[i].Counterparty < res.Edges[j].Counterparty
	})
	sort.Slice(res.Nodes, func(i, j int) bool { return res.Nodes[i].Address < res.Nodes[j].Address })
	return res, nil
}

func parseBig(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "<nil>" {
		return nil, false
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return nil, false
	}
	return n, true
}
