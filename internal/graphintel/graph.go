// Package graphintel 实现地址图谱与关系网络分析：
// 图构建（Transfer/Interaction 聚合）、核心节点分析（Degree/PageRank）、
// 连通分量簇发现、风险网络识别、图谱查询与报告输出。
package graphintel

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/analyticsapi"
)

// EdgeKind 关系边类型。
type EdgeKind string

const (
	EdgeTransfer      EdgeKind = "TRANSFER"
	EdgeInteraction   EdgeKind = "INTERACTION"
	EdgeCommonCounter EdgeKind = "COMMON_COUNTERPARTY"
)

// Node 是地址节点。
type Node struct {
	Address          string  `json:"address"`
	Type             string  `json:"type"`
	RiskScore        float64 `json:"risk_score"`
	TransactionCount int64   `json:"transaction_count"`
	TotalIn          int64   `json:"total_in"`
	TotalOut         int64   `json:"total_out"`
	FirstActivity    string  `json:"first_activity"`
	LastActivity     string  `json:"last_activity"`
	Degree           int     `json:"degree"`
	WeightedDegree   float64 `json:"weighted_degree"`
	PageRank         float64 `json:"pagerank"`
	ClusterID        int     `json:"cluster_id,omitempty"`
}

// Edge 是关系边（聚合）。
type Edge struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Kind      EdgeKind `json:"kind"`
	Token     string   `json:"token,omitempty"`
	Amount    string   `json:"amount,omitempty"`
	TxCount   int64    `json:"tx_count"`
	FirstTime string   `json:"first_time"`
	LastTime  string   `json:"last_time"`
}

// Graph 是完整关系图谱。
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Builder 构建图谱。
type Builder struct {
	engine  *duckdb.Engine
	parquet string
}

// NewBuilder 创建图构建器。
func NewBuilder(engine *duckdb.Engine, parquetPath string) *Builder {
	return &Builder{
		engine:  engine,
		parquet: strings.ReplaceAll(parquetPath, "\\", "/"),
	}
}

// Build 从 Parquet 构建全图（Transfer 聚合边 + Interaction 边 + 节点画像）。
func (b *Builder) Build(ctx context.Context) (*Graph, error) {
	norm1 := `CASE WHEN length(topic1) = 66 THEN '0x' || substr(topic1, 27) ELSE topic1 END`
	norm2 := `CASE WHEN length(topic2) = 66 THEN '0x' || substr(topic2, 27) ELSE topic2 END`
	// Transfer 边（按 source/target/token 聚合）
	rows, err := b.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT %[1]s AS f, %[2]s AS t, address AS token,
			COUNT(*) AS tx_count,
			SUM(CASE WHEN length(data) > 2 THEN TRY_CAST(concat('0x', substr(lower(data), 3)) AS HUGEINT) ELSE 0 END) AS amount_sum,
			to_timestamp(TRY_CAST(min(block_time) AS UBIGINT))::VARCHAR AS first_time,
			to_timestamp(TRY_CAST(max(block_time) AS UBIGINT))::VARCHAR AS last_time
		 FROM read_parquet('%[3]s')
		 WHERE topic0 IN ('%[4]s','%[5]s','%[6]s') AND %[1]s != %[2]s
		 GROUP BY 1, 2, 3`, norm1, norm2, b.parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch))
	if err != nil {
		return nil, err
	}
	g := &Graph{}
	nodeSet := map[string]bool{}
	edgeSeen := map[string]bool{}
	for _, r := range rows {
		f := strings.ToLower(fmt.Sprintf("%v", r["f"]))
		t := strings.ToLower(fmt.Sprintf("%v", r["t"]))
		nodeSet[f] = true
		nodeSet[t] = true
		amount := ""
		if v, ok := r["amount_sum"]; ok && v != nil {
			amount = fmt.Sprintf("%v", v)
		}
		key := f + "|" + t + "|" + fmt.Sprintf("%v", r["token"])
		if !edgeSeen[key] {
			edgeSeen[key] = true
			g.Edges = append(g.Edges, Edge{
				Source: f, Target: t, Kind: EdgeTransfer,
				Token:     fmt.Sprintf("%v", r["token"]),
				Amount:    amount,
				TxCount:   int64(r["tx_count"].(float64)),
				FirstTime: fmt.Sprintf("%v", r["first_time"]),
				LastTime:  fmt.Sprintf("%v", r["last_time"]),
			})
		}
	}
	// Interaction 边（emitter ↔ topic 地址，非 Transfer 事件）
	irows, err := b.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT address AS emitter, %[1]s AS a, %[2]s AS b, COUNT(*) AS n,
			to_timestamp(TRY_CAST(min(block_time) AS UBIGINT))::VARCHAR AS first_time,
			to_timestamp(TRY_CAST(max(block_time) AS UBIGINT))::VARCHAR AS last_time
		 FROM read_parquet('%[3]s')
		 WHERE topic0 NOT IN ('%[4]s','%[5]s','%[6]s')
		   AND (%[1]s IS NOT NULL AND %[1]s != '' OR %[2]s IS NOT NULL AND %[2]s != '')
		 GROUP BY 1, 2, 3`, norm1, norm2, b.parquet,
		analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch))
	if err != nil {
		return nil, err
	}
	for _, r := range irows {
		emitter := strings.ToLower(fmt.Sprintf("%v", r["emitter"]))
		a := strings.ToLower(fmt.Sprintf("%v", r["a"]))
		b := strings.ToLower(fmt.Sprintf("%v", r["b"]))
		if a != "" && a != emitter {
			nodeSet[emitter] = true
			nodeSet[a] = true
			key := emitter + "|" + a + "|int"
			if !edgeSeen[key] {
				edgeSeen[key] = true
				g.Edges = append(g.Edges, Edge{
					Source: emitter, Target: a, Kind: EdgeInteraction,
					TxCount:   int64(r["n"].(float64)),
					FirstTime: fmt.Sprintf("%v", r["first_time"]),
					LastTime:  fmt.Sprintf("%v", r["last_time"]),
				})
			}
		}
		if b != "" && b != emitter {
			nodeSet[emitter] = true
			nodeSet[b] = true
			key := emitter + "|" + b + "|int"
			if !edgeSeen[key] {
				edgeSeen[key] = true
				g.Edges = append(g.Edges, Edge{
					Source: emitter, Target: b, Kind: EdgeInteraction,
					TxCount:   int64(r["n"].(float64)),
					FirstTime: fmt.Sprintf("%v", r["first_time"]),
					LastTime:  fmt.Sprintf("%v", r["last_time"]),
				})
			}
		}
	}
	// 节点画像（emitter 维度统计）
	nrows, err := b.engine.ExecSQLJSON(ctx, fmt.Sprintf(
		`SELECT address, COUNT(*) AS event_count,
			COUNT(DISTINCT CASE WHEN topic0 IN ('%[2]s','%[3]s','%[4]s') THEN 1 END) AS transfer_count,
			to_timestamp(TRY_CAST(min(block_time) AS UBIGINT))::VARCHAR AS first_time,
			to_timestamp(TRY_CAST(max(block_time) AS UBIGINT))::VARCHAR AS last_time
		 FROM read_parquet('%[1]s') GROUP BY address`,
		b.parquet, analyticsapi.TransferTopic, analyticsapi.TransferSingle, analyticsapi.TransferBatch))
	if err != nil {
		return nil, err
	}
	nodeMeta := map[string]map[string]any{}
	for _, r := range nrows {
		nodeMeta[strings.ToLower(fmt.Sprintf("%v", r["address"]))] = r
	}
	for addr := range nodeSet {
		n := Node{Address: addr, Type: "address"}
		if meta, ok := nodeMeta[addr]; ok {
			n.TransactionCount = int64(meta["event_count"].(float64))
			n.FirstActivity = fmt.Sprintf("%v", meta["first_time"])
			n.LastActivity = fmt.Sprintf("%v", meta["last_time"])
			if tc, ok := meta["transfer_count"].(float64); ok && tc > 0 {
				n.Type = "contract"
			}
		}
		// in/out 度
		for _, e := range g.Edges {
			if e.Kind != EdgeTransfer {
				continue
			}
			if e.Target == addr {
				n.TotalIn++
			}
			if e.Source == addr {
				n.TotalOut++
			}
		}
		g.Nodes = append(g.Nodes, n)
	}
	return g, nil
}

// ── 核心分析 ──

// ComputeMetrics 计算 Degree / WeightedDegree / PageRank / 连通分量。
func ComputeMetrics(g *Graph) {
	idx := map[string]int{}
	for i, n := range g.Nodes {
		idx[n.Address] = i
		g.Nodes[i].Degree = 0
		g.Nodes[i].WeightedDegree = 0
	}
	adj := make([][]int, len(g.Nodes))
	for _, e := range g.Edges {
		s, okS := idx[e.Source]
		t, okT := idx[e.Target]
		if !okS || !okT {
			continue
		}
		g.Nodes[s].Degree++
		g.Nodes[t].Degree++
		if f, ok := parseAmount(e.Amount); ok {
			g.Nodes[s].WeightedDegree += f
			g.Nodes[t].WeightedDegree += f
		}
		adj[s] = append(adj[s], t)
		adj[t] = append(adj[t], s)
	}
	// PageRank（100 次迭代，damping 0.85）
	n := len(g.Nodes)
	if n == 0 {
		return
	}
	pr := make([]float64, n)
	for i := range pr {
		pr[i] = 1.0 / float64(n)
	}
	damping := 0.85
	for iter := 0; iter < 100; iter++ {
		next := make([]float64, n)
		dangling := 0.0
		for i := range pr {
			if len(adj[i]) == 0 {
				dangling += pr[i]
			}
		}
		for i := range pr {
			share := 0.0
			if len(adj[i]) > 0 {
				share = pr[i] / float64(len(adj[i]))
			}
			for _, j := range adj[i] {
				next[j] += share
			}
		}
		base := (1-damping)/float64(n) + damping*dangling/float64(n)
		for i := range next {
			pr[i] = base + damping*next[i]
		}
	}
	for i := range g.Nodes {
		g.Nodes[i].PageRank = math.Round(pr[i]*1e6) / 1e6
	}
	// 连通分量（Union-Find）
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for _, e := range g.Edges {
		if s, ok := idx[e.Source]; ok {
			if t, ok2 := idx[e.Target]; ok2 {
				union(s, t)
			}
		}
	}
	clusterMap := map[int]int{}
	nextID := 0
	for i := range g.Nodes {
		root := find(i)
		if _, ok := clusterMap[root]; !ok {
			clusterMap[root] = nextID
			nextID++
		}
		g.Nodes[i].ClusterID = clusterMap[root]
	}
}

// RiskPattern 是风险网络模式。
type RiskPattern struct {
	Hubs      []string `json:"hubs"`      // 中转：in>=10 && out>=10
	Sinks     []string `json:"sinks"`     // 归集：in>=10 且 in > 2*out
	Spreaders []string `json:"spreaders"` // 分散：out>=10 且 out > 2*in
}

// DetectRiskPatterns 识别风险网络模式。
func DetectRiskPatterns(g *Graph) RiskPattern {
	var rp RiskPattern
	for _, n := range g.Nodes {
		switch {
		case n.TotalIn >= 10 && n.TotalOut >= 10:
			rp.Hubs = append(rp.Hubs, n.Address)
		case n.TotalIn >= 10 && n.TotalIn > 2*n.TotalOut:
			rp.Sinks = append(rp.Sinks, n.Address)
		case n.TotalOut >= 10 && n.TotalOut > 2*n.TotalIn:
			rp.Spreaders = append(rp.Spreaders, n.Address)
		}
	}
	limit := func(list []string) []string {
		sort.Strings(list)
		if len(list) > 10 {
			return list[:10]
		}
		return list
	}
	rp.Hubs = limit(rp.Hubs)
	rp.Sinks = limit(rp.Sinks)
	rp.Spreaders = limit(rp.Spreaders)
	return rp
}

// QueryNeighborhood 查询地址邻域（BFS depth 层）。
func QueryNeighborhood(g *Graph, address string, depth int) *Graph {
	address = strings.ToLower(address)
	idx := map[string]bool{address: true}
	frontier := []string{address}
	for d := 0; d < depth; d++ {
		var next []string
		for _, e := range g.Edges {
			if idx[e.Source] && !idx[e.Target] {
				idx[e.Target] = true
				next = append(next, e.Target)
			}
			if idx[e.Target] && !idx[e.Source] {
				idx[e.Source] = true
				next = append(next, e.Source)
			}
		}
		if len(next) == 0 {
			break
		}
		frontier = append(frontier, next...)
	}
	sub := &Graph{}
	for _, n := range g.Nodes {
		if idx[n.Address] {
			sub.Nodes = append(sub.Nodes, n)
		}
	}
	for _, e := range g.Edges {
		if idx[e.Source] && idx[e.Target] {
			sub.Edges = append(sub.Edges, e)
		}
	}
	return sub
}

func parseAmount(s string) (float64, bool) {
	if s == "" || s == "<nil>" {
		return 0, false
	}
	f := new(big.Float)
	if _, ok := f.SetString(s); !ok {
		return 0, false
	}
	v, _ := f.Float64()
	return v, true
}

// ── 输出 ──

// Export 输出 graph.json + nodes.csv + edges.csv + clusters.csv。
func Export(dir string, g *Graph) error {
	// 导出前确保度/PageRank/簇已计算（幂等）
	ComputeMetrics(g)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	gData, _ := json.MarshalIndent(g, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "graph.json"), gData, 0644); err != nil {
		return err
	}
	// nodes.csv
	nf, err := os.Create(filepath.Join(dir, "nodes.csv"))
	if err != nil {
		return err
	}
	nw := csv.NewWriter(nf)
	_ = nw.Write([]string{"address", "type", "risk_score", "transaction_count", "total_in", "total_out", "first_activity", "last_activity", "degree", "weighted_degree", "pagerank", "cluster_id"})
	for _, n := range g.Nodes {
		_ = nw.Write([]string{n.Address, n.Type, fmt.Sprintf("%.4f", n.RiskScore), fmt.Sprintf("%d", n.TransactionCount),
			fmt.Sprintf("%d", n.TotalIn), fmt.Sprintf("%d", n.TotalOut), n.FirstActivity, n.LastActivity,
			fmt.Sprintf("%d", n.Degree), fmt.Sprintf("%.2f", n.WeightedDegree), fmt.Sprintf("%.6f", n.PageRank),
			fmt.Sprintf("%d", n.ClusterID)})
	}
	nw.Flush()
	nf.Close()
	// edges.csv
	ef, err := os.Create(filepath.Join(dir, "edges.csv"))
	if err != nil {
		return err
	}
	ew := csv.NewWriter(ef)
	_ = ew.Write([]string{"source", "target", "kind", "token", "amount", "tx_count", "first_time", "last_time"})
	for _, e := range g.Edges {
		_ = ew.Write([]string{e.Source, e.Target, string(e.Kind), e.Token, e.Amount, fmt.Sprintf("%d", e.TxCount), e.FirstTime, e.LastTime})
	}
	ew.Flush()
	ef.Close()
	// clusters.csv
	cf, err := os.Create(filepath.Join(dir, "clusters.csv"))
	if err != nil {
		return err
	}
	cw2 := csv.NewWriter(cf)
	_ = cw2.Write([]string{"cluster_id", "size", "members"})
	byCluster := map[int][]string{}
	for _, n := range g.Nodes {
		byCluster[n.ClusterID] = append(byCluster[n.ClusterID], n.Address)
	}
	var clusterIDs []int
	for id := range byCluster {
		clusterIDs = append(clusterIDs, id)
	}
	sort.Ints(clusterIDs)
	for _, id := range clusterIDs {
		members := byCluster[id]
		sort.Strings(members)
		_ = cw2.Write([]string{fmt.Sprintf("%d", id), fmt.Sprintf("%d", len(members)), strings.Join(members, " ")})
	}
	cw2.Flush()
	cf.Close()
	return nil
}

// Stats 返回图统计。
func Stats(g *Graph) map[string]any {
	clusterSizes := map[int]int{}
	maxCluster := 0
	for _, n := range g.Nodes {
		clusterSizes[n.ClusterID]++
		if clusterSizes[n.ClusterID] > maxCluster {
			maxCluster = clusterSizes[n.ClusterID]
		}
	}
	return map[string]any{
		"nodes":           len(g.Nodes),
		"edges":           len(g.Edges),
		"clusters":        len(clusterSizes),
		"largest_cluster": maxCluster,
		"build_time_ms":   0,
	}
}

var _ = time.Now
