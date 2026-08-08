package fundflow

import (
	"context"
	"math/big"
	"sort"
	"strings"

	"github.com/etl/backend/internal/analyticsapi"
)

// buildGraph 构建实体感知资金图（有界 BFS，跟随资金去向）。
func (e *Engine) buildGraph(ctx context.Context, chainKey, root, token string, depth int, invID string) (*EntityAwareFlowGraph, error) {
	g := &EntityAwareFlowGraph{Root: root}
	flowsCache := map[string][]analyticsapi.FlowEdge{}
	entityCache := map[string]*entityInfo{}
	visited := map[string]int{}
	frontier := []string{root}
	visited[root] = 0
	nodeAgg := map[string]*nodeAggState{}
	edgeSeen := map[string]bool{}

	for d := 0; d < depth && len(frontier) > 0 && len(visited) < e.cfg.MaxNodes; d++ {
		var next []string
		for _, addr := range frontier {
			flows, ok := flowsCache[addr]
			if !ok {
				f, err := e.src.Flows(ctx, addr, token)
				if err != nil {
					continue
				}
				flows = f
				flowsCache[addr] = f
			}
			for _, f := range flows {
				if !strings.EqualFold(f.Direction, "outgoing") {
					continue
				}
				to := strings.ToLower(strings.TrimSpace(f.Counterparty))
				if to == "" || to == addr {
					continue
				}
				key := addr + "|" + to + "|" + strings.ToLower(f.Token)
				if edgeSeen[key] {
					continue
				}
				edgeSeen[key] = true
				sa := nodeAgg[addr]
				if sa == nil {
					sa = &nodeAggState{out: big.NewInt(0), in: big.NewInt(0)}
					nodeAgg[addr] = sa
				}
				ta := nodeAgg[to]
				if ta == nil {
					ta = &nodeAggState{out: big.NewInt(0), in: big.NewInt(0)}
					nodeAgg[to] = ta
				}
				if amt, ok := parseBigInt(f.Amount); ok {
					sa.out.Add(sa.out, amt)
					ta.in.Add(ta.in, amt)
				}
				et := classifyEdge(e.entityOf(ctx, chainKey, addr, entityCache, invID),
					e.entityOf(ctx, chainKey, to, entityCache, invID))
				g.Edges = append(g.Edges, EntityAwareEdge{
					From: addr, To: to,
					FromEntity: entityOfID(e.entityOf(ctx, chainKey, addr, entityCache, invID)),
					ToEntity:   entityOfID(e.entityOf(ctx, chainKey, to, entityCache, invID)),
					Token: strings.ToLower(f.Token), Amount: f.Amount,
					TxHash: f.TxHash, BlockNumber: blockNum(f.Block), EdgeType: et,
				})
				if _, ok := visited[to]; !ok {
					visited[to] = d + 1
					next = append(next, to)
				}
			}
		}
		frontier = next
	}
	// 节点聚合
	for addr, st := range nodeAgg {
		ent := e.entityOf(ctx, chainKey, addr, entityCache, invID)
		node := EntityAwareNode{
			Address: addr,
			GrossInflow: st.in.String(), GrossOutflow: st.out.String(),
			NetFlow: new(big.Int).Sub(st.in, st.out).String(),
		}
		if ent != nil {
			node.EntityID = ent.ID
			node.EntityName = ent.Name
			node.EntityType = string(ent.Type)
		}
		g.Nodes = append(g.Nodes, node)
		if ent != nil && ent.ID != "" {
			g.CollapsedEntities++
		}
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].Address < g.Nodes[j].Address })
	return g, nil
}

type nodeAggState struct {
	in, out *big.Int
}

type entityInfo struct {
	ID   string
	Name string
	Type entityType
}

type entityType string

func (e *Engine) entityOf(ctx context.Context, chainKey, addr string, cache map[string]*entityInfo, invID string) *entityInfo {
	if e.entities == nil {
		return nil
	}
	if cached, ok := cache[addr]; ok {
		return cached
	}
	res, err := e.entities.Resolve(ctx, chainKey, addr, invID)
	if err != nil || res == nil || res.Entity == nil {
		cache[addr] = nil
		return nil
	}
	info := &entityInfo{ID: res.Entity.ID, Name: res.Entity.Name, Type: entityType(res.Entity.EntityType)}
	cache[addr] = info
	return info
}

func entityOfID(info *entityInfo) string {
	if info == nil {
		return ""
	}
	return info.ID
}

func classifyEdge(from, to *entityInfo) EdgeType {
	if from != nil && to != nil && from.ID != "" && from.ID == to.ID {
		return EdgeInternalEntity
	}
	if to != nil {
		switch to.Type {
		case entityType("EXCHANGE"), entityType("CEX_DEPOSIT"), entityType("CEX_HOT_WALLET"), entityType("CEX_COLD_WALLET"):
			return EdgeDeposit
		case entityType("BRIDGE"):
			return EdgeBridgeOut
		case entityType("DEX"), entityType("ROUTER"):
			return EdgeSwapOut
		}
	}
	if from != nil {
		switch from.Type {
		case entityType("BRIDGE"):
			return EdgeBridgeIn
		case entityType("DEX"), entityType("ROUTER"):
			return EdgeSwapIn
		}
	}
	return EdgeTokenTransfer
}

func parseBigInt(s string) (*big.Int, bool) {
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
