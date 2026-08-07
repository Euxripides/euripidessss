// Package graphincrement 实现 Graph 增量更新（Phase 5.3 §7/§14）：
// DATASET_INDEXED → 只读取新 registry 条目的本地 parquet → 节点/边按唯一键去重合并
// → GRAPH_READY 状态；重复事件不产生重复边/统计。
package graphincrement

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/analysis/duckdb"
	"github.com/etl/backend/internal/datasetsync"
)

// State 图谱增量状态。
type State struct {
	Status       string    `json:"status"` // GRAPH_READY / GRAPH_WAITING_DATA / GRAPH_EXPAND_COMPLETED
	LastChunks   []string  `json:"last_chunks"`
	NodeCount    int64     `json:"node_count"`
	EdgeCount    int64     `json:"edge_count"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Result 单次增量结果。
type Result struct {
	ChunkKey string `json:"chunk_key"`
	Nodes    int64  `json:"nodes"`
	Edges    int64  `json:"edges"`
	Applied  bool   `json:"applied"`
}

// Incrementer 增量图物化器。
type Incrementer struct {
	mu        sync.Mutex
	engine    *duckdb.Engine
	statePath string
	state     State
}

// NewIncrementer 创建增量器。
func NewIncrementer(engine *duckdb.Engine, statePath string) (*Incrementer, error) {
	inc := &Incrementer{engine: engine, statePath: statePath}
	if err := inc.load(); err != nil {
		return nil, err
	}
	return inc, nil
}

// Apply 应用一个 registry 条目的增量（幂等：唯一键去重 + 已应用 chunk 跳过）。
func (inc *Incrementer) Apply(ctx context.Context, entry *datasetsync.Entry) (Result, error) {
	inc.mu.Lock()
	defer inc.mu.Unlock()
	if entry == nil || entry.ChunkKey == "" {
		return Result{}, fmt.Errorf("invalid registry entry")
	}
	for _, c := range inc.state.LastChunks {
		if c == entry.ChunkKey {
			return Result{ChunkKey: entry.ChunkKey, Applied: false}, nil
		}
	}
	if inc.engine == nil || !inc.engine.Available() {
		return Result{}, fmt.Errorf("duckdb 不可用")
	}
	paths := localParquet(entry.LocalDir)
	created, err := inc.apply(ctx, entry, paths)
	if err != nil {
		return Result{}, err
	}
	if created {
		inc.state.LastChunks = append(inc.state.LastChunks, entry.ChunkKey)
		inc.state.Status = "GRAPH_READY"
		inc.state.UpdatedAt = time.Now()
		_ = inc.save()
	}
	inc.refreshCounts()
	return Result{
		ChunkKey: entry.ChunkKey,
		Nodes:    inc.state.NodeCount,
		Edges:    inc.state.EdgeCount,
		Applied:  created,
	}, nil
}

// Status 返回图谱状态。
func (inc *Incrementer) Status() State {
	inc.mu.Lock()
	defer inc.mu.Unlock()
	inc.refreshCounts()
	return inc.state
}

func (inc *Incrementer) apply(ctx context.Context, entry *datasetsync.Entry, paths []string) (bool, error) {
	if len(paths) == 0 {
		// 0-row chunk 合法：仅标记已应用，不产生节点/边
		return true, nil
	}
	if _, err := inc.engine.ExecSQL(ctx, `
CREATE TABLE IF NOT EXISTS graph_edges (
  edge_key VARCHAR PRIMARY KEY,
  chain_id BIGINT, block_number BIGINT, block_timestamp BIGINT,
  transaction_hash VARCHAR, log_index BIGINT,
  token_address VARCHAR, from_address VARCHAR, to_address VARCHAR, value_raw VARCHAR
)`); err != nil {
		return false, fmt.Errorf("create graph_edges: %w", err)
	}
	if _, err := inc.engine.ExecSQL(ctx, `
CREATE TABLE IF NOT EXISTS graph_nodes (
  node_key VARCHAR PRIMARY KEY,
  chain_id BIGINT, address VARCHAR,
  first_block BIGINT, last_block BIGINT, tx_count BIGINT DEFAULT 0
)`); err != nil {
		return false, fmt.Errorf("create graph_nodes: %w", err)
	}
	list := pathListSQL(paths)
	edgeSQL := fmt.Sprintf(`
INSERT OR IGNORE INTO graph_edges (edge_key, chain_id, block_number, block_timestamp, transaction_hash, log_index, token_address, from_address, to_address, value_raw)
SELECT CAST(chain_id AS VARCHAR)||'|'||CAST(block_number AS VARCHAR)||'|'||transaction_hash||'|'||CAST(log_index AS VARCHAR)||'|'||LOWER(CAST(from_address AS VARCHAR))||'|'||LOWER(CAST(to_address AS VARCHAR))||'|'||LOWER(CAST(token_address AS VARCHAR)),
       chain_id, block_number, block_timestamp, transaction_hash, log_index,
       LOWER(CAST(token_address AS VARCHAR)), LOWER(CAST(from_address AS VARCHAR)), LOWER(CAST(to_address AS VARCHAR)), value_raw
FROM read_parquet(%s)`, list)
	if _, err := inc.engine.ExecSQL(ctx, edgeSQL); err != nil {
		return false, fmt.Errorf("insert edges: %w", err)
	}
	nodeSQL := fmt.Sprintf(`
INSERT OR IGNORE INTO graph_nodes (node_key, chain_id, address, first_block, last_block, tx_count)
SELECT CAST(chain_id AS VARCHAR)||'|'||LOWER(address) AS node_key, chain_id, LOWER(address), min_block, max_block, 0
FROM (
  SELECT chain_id, CAST(from_address AS VARCHAR) AS address, MIN(block_number) AS min_block, MAX(block_number) AS max_block FROM read_parquet(%s) GROUP BY chain_id, from_address
  UNION ALL
  SELECT chain_id, CAST(to_address AS VARCHAR) AS address, MIN(block_number) AS min_block, MAX(block_number) AS max_block FROM read_parquet(%s) GROUP BY chain_id, to_address
)`, list, list)
	if _, err := inc.engine.ExecSQL(ctx, nodeSQL); err != nil {
		return false, fmt.Errorf("insert nodes: %w", err)
	}
	if _, err := inc.engine.ExecSQL(ctx, `
UPDATE graph_nodes SET
  tx_count = (SELECT COUNT(*) FROM graph_edges e WHERE e.from_address = graph_nodes.address OR e.to_address = graph_nodes.address),
  first_block = (SELECT COALESCE(MIN(e.block_number), graph_nodes.first_block) FROM graph_edges e WHERE e.from_address = graph_nodes.address OR e.to_address = graph_nodes.address),
  last_block = (SELECT COALESCE(MAX(e.block_number), graph_nodes.last_block) FROM graph_edges e WHERE e.from_address = graph_nodes.address OR e.to_address = graph_nodes.address)`); err != nil {
		return false, fmt.Errorf("update node stats: %w", err)
	}
	return true, nil
}

func (inc *Incrementer) refreshCounts() {
	if inc.engine == nil || !inc.engine.Available() {
		return
	}
	rows, err := inc.engine.ExecSQLJSON(context.Background(),
		"SELECT (SELECT COUNT(*) FROM graph_nodes) AS n, (SELECT COUNT(*) FROM graph_edges) AS e")
	if err == nil && len(rows) > 0 {
		inc.state.NodeCount = int64(num(rows[0]["n"]))
		inc.state.EdgeCount = int64(num(rows[0]["e"]))
	}
}

func (inc *Incrementer) load() error {
	if inc.statePath == "" {
		return nil
	}
	payload, err := os.ReadFile(inc.statePath)
	if err != nil {
		return nil
	}
	return json.Unmarshal(payload, &inc.state)
}

func (inc *Incrementer) save() error {
	if inc.statePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(inc.statePath), 0o755); err != nil {
		return err
	}
	payload, _ := json.MarshalIndent(inc.state, "", "  ")
	tmp := inc.statePath + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, inc.statePath)
}

func localParquet(dir string) []string {
	var out []string
	if dir == "" {
		return nil
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".parquet") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func pathListSQL(paths []string) string {
	items := make([]string, 0, len(paths))
	for _, p := range paths {
		items = append(items, "'"+strings.ReplaceAll(strings.ReplaceAll(p, "\\", "/"), "'", "''")+"'")
	}
	return "[" + strings.Join(items, ", ") + "]"
}

func num(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}
