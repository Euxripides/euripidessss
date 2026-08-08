package graphcache

import (
	"math/big"
	"time"
)

// Merge 将 delta 聚合结果合并进 base（设计 §43 Incremental Graph Rebuild 的缓存层：
// 只聚合增量，再与旧图合并，避免全量重算）。
func Merge(base, delta *Result) *Result {
	if base == nil {
		return delta
	}
	if delta == nil {
		return base
	}
	out := &Result{
		Key:              base.Key,
		TotalInflow:      sumBig(base.TotalInflow, delta.TotalInflow),
		TotalOutflow:     sumBig(base.TotalOutflow, delta.TotalOutflow),
		CounterpartyCount: base.CounterpartyCount,
		Coverage:         base.Coverage,
		Certification:    base.Certification,
		GeneratedAt:      time.Now(),
		Source:           "merged",
	}
	edges := map[string]*Edge{}
	for i := range base.Edges {
		e := base.Edges[i]
		edges[e.EdgeKey()] = &e
	}
	for i := range delta.Edges {
		e := delta.Edges[i]
		k := e.EdgeKey()
		if cur, ok := edges[k]; ok {
			cur.TxCount += e.TxCount
			cur.Inflow = sumBig(cur.Inflow, e.Inflow)
			cur.Outflow = sumBig(cur.Outflow, e.Outflow)
			cur.LastSeen = maxStr(cur.LastSeen, e.LastSeen)
			if cur.FirstSeen == "" {
				cur.FirstSeen = e.FirstSeen
			}
		} else {
			edges[k] = &e
		}
	}
	out.CounterpartyCount = len(edges)
	for _, e := range edges {
		out.Edges = append(out.Edges, *e)
	}
	nodes := map[string]*Node{}
	for i := range base.Nodes {
		n := base.Nodes[i]
		nodes[n.NodeKey()] = &n
	}
	for i := range delta.Nodes {
		n := delta.Nodes[i]
		k := n.NodeKey()
		if cur, ok := nodes[k]; ok {
			cur.TxCount += n.TxCount
			cur.Inflow = sumBig(cur.Inflow, n.Inflow)
			cur.Outflow = sumBig(cur.Outflow, n.Outflow)
			cur.LastSeen = maxStr(cur.LastSeen, n.LastSeen)
			if cur.FirstSeen == "" {
				cur.FirstSeen = n.FirstSeen
			}
		} else {
			nodes[k] = &n
		}
	}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, *n)
	}
	return out
}

func sumBig(a, b string) string {
	ai, ok1 := parseBig(a)
	bi, ok2 := parseBig(b)
	if !ok1 {
		if ok2 {
			return b
		}
		return "0"
	}
	if !ok2 {
		return a
	}
	return new(big.Int).Add(ai, bi).String()
}

func maxStr(a, b string) string {
	if a >= b {
		return a
	}
	return b
}

