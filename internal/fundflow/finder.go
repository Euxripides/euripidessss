package fundflow

import (
	"context"
	"math/big"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// findPaths 有界 DFS 路径发现（设计 §34-§37）。
func (e *Engine) findPaths(_ context.Context, chainKey, root, token string, g *EntityAwareFlowGraph, goal string, maxDepth int, _ string) []*Path {
	// 邻接表
	adj := map[string][]EntityAwareEdge{}
	for _, edge := range g.Edges {
		adj[edge.From] = append(adj[edge.From], edge)
	}
	// 每层按金额 Top-K
	for k := range adj {
		sort.Slice(adj[k], func(i, j int) bool {
			return amountGt(adj[k][i].Amount, adj[k][j].Amount)
		})
		if len(adj[k]) > e.cfg.TopKPerLayer {
			adj[k] = adj[k][:e.cfg.TopKPerLayer]
		}
	}
	var paths []*Path
	visited := map[string]bool{}
	var walk func(addr string, nodes []PathNode, depth int)
	walk = func(addr string, nodes []PathNode, depth int) {
		if depth >= maxDepth || len(paths) >= 50 || len(nodes) >= e.cfg.MaxNodes {
			return
		}
		for _, edge := range adj[addr] {
			to := edge.To
			if visited[to] {
				continue
			}
			visited[to] = true
			node := PathNode{
				Address: to, EntityID: edge.ToEntity,
				InAmount: edge.Amount, EdgeType: edge.EdgeType, EdgeTxHash: edge.TxHash,
				Token: edge.Token, BlockNumber: edge.BlockNumber,
			}
			if ent := nodeEntity(g, to); ent != nil {
				node.EntityID = ent.EntityID
				node.EntityName = ent.EntityName
				node.EntityType = ent.EntityType
			}
			next := append(append([]PathNode{}, nodes...), node)
			paths = append(paths, e.pathFromNodes(chainKey, root, goal, next))
			walk(to, next, depth+1)
			delete(visited, to)
			if len(paths) >= 50 {
				return
			}
		}
	}
	visited[root] = true
	walk(root, []PathNode{}, 0)
	// 排序并截断
	sort.Slice(paths, func(i, j int) bool { return paths[i].Score > paths[j].Score })
	if len(paths) > 50 {
		paths = paths[:50]
	}
	return paths
}

func (e *Engine) pathFromNodes(chainKey, root, goal string, nodes []PathNode) *Path {
	now := e.now().UTC()
	p := &Path{
		ID: uuid.NewString(), RootAddress: root, ChainKey: chainKey,
		Goal: goal, Nodes: nodes, Hops: len(nodes), CreatedAt: now,
	}
	total := big.NewInt(0)
	for _, n := range nodes {
		if amt, ok := parseBigInt(n.InAmount); ok {
			total.Add(total, amt)
		}
	}
	p.TotalAmount = total.String()
	p.PathType, p.TerminalType = classifyPath(nodes)
	p.Score, p.Confidence = scorePath(p, goal)
	p.Evidence = pathEvidence(p)
	return p
}

func classifyPath(nodes []PathNode) (string, string) {
	if len(nodes) == 0 {
		return "UNKNOWN", ""
	}
	term := nodes[len(nodes)-1]
	t := strings.ToUpper(term.EntityType)
	switch t {
	case "EXCHANGE", "CEX_DEPOSIT", "CEX_HOT_WALLET", "CEX_COLD_WALLET", "PAYMENT_SERVICE", "CUSTODIAN":
		if len(nodes) == 1 {
			return "DIRECT_CASHOUT", t
		}
		return "MULTI_HOP_CASHOUT", t
	case "BRIDGE":
		return "BRIDGE_EXIT", t
	case "UNKNOWN_SERVICE":
		return "COLLECT_AND_SETTLE", t
	}
	if term.EntityName != "" && strings.Contains(term.EntityName, "沉淀") {
		return "COLLECT_AND_SETTLE", "SETTLEMENT"
	}
	return "UNKNOWN", t
}

func nodeEntity(g *EntityAwareFlowGraph, addr string) *EntityAwareNode {
	for i := range g.Nodes {
		if g.Nodes[i].Address == addr {
			return &g.Nodes[i]
		}
	}
	return nil
}

func amountGt(a, b string) bool {
	ai, ok1 := parseBigInt(a)
	bi, ok2 := parseBigInt(b)
	if !ok1 {
		return false
	}
	if !ok2 {
		return true
	}
	return ai.Cmp(bi) > 0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func pathEvidence(p *Path) []EvidenceRefLite {
	var out []EvidenceRefLite
	for _, n := range p.Nodes {
		if n.EdgeTxHash != "" {
			out = append(out, EvidenceRefLite{
				SourceType: "FLOW_EDGE", SourceName: "链上资金边",
				Observation: n.Address + " edge=" + string(n.EdgeType) + " amount=" + n.InAmount,
				Confidence: 0.8,
			})
		}
	}
	return out
}
