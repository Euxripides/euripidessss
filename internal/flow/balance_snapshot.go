package flow

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/investigationstore"
)

// ── 余额快照（V2.0 设计 §8）──
//
// 快照目录：backend/data/investigation/balance-snapshots/（设计 §8/§31）。
// 快照必须保存：chain、chain_id、address、block_number、captured_at、RPC source、asset list。
// 复用 investigationstore.JSONStore 原子写（schema_version + tmp+fsync+rename）。

// BalanceSnapshot 是一条余额快照（设计 §8 字段）。
type BalanceSnapshot struct {
	Chain       string         `json:"chain"`
	ChainID     int64          `json:"chain_id"`
	Address     string         `json:"address"`
	BlockNumber string         `json:"block_number,omitempty"`
	CapturedAt  time.Time      `json:"captured_at"`
	Source      string         `json:"source"`
	Assets      []AssetBalance `json:"assets"`
}

// BalanceSnapshotStore 管理余额快照（目录 = dataRoot/investigation/balance-snapshots）。
type BalanceSnapshotStore struct {
	mu    sync.Mutex
	store *investigationstore.JSONStore[BalanceSnapshot]
	dir   string
}

// NewBalanceSnapshotStore 创建快照存储。dir 为空则仅内存（测试用）。
func NewBalanceSnapshotStore(dir string) *BalanceSnapshotStore {
	s := &BalanceSnapshotStore{
		store: investigationstore.NewJSONStore[BalanceSnapshot](dir),
		dir:   dir,
	}
	return s
}

// keyFor 快照 key：address + captured_at 时间戳（幂等：同秒同地址去重）。
func keyFor(chain, address string, capturedAt time.Time) string {
	return strings.ToLower(chain) + "-" + strings.ToLower(address) + "-" + capturedAt.UTC().Format("20060102T150405")
}

// Save 保存快照（原子写，设计 §8）。
func (s *BalanceSnapshotStore) Save(snap BalanceSnapshot) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap.CapturedAt.IsZero() {
		snap.CapturedAt = time.Now().UTC()
	}
	key := keyFor(snap.Chain, snap.Address, snap.CapturedAt)
	if err := s.store.Save(key, snap); err != nil {
		return "", err
	}
	return key, nil
}

// Latest 返回地址最新快照（用于历史对比，设计 §8 变化量/变化率）。
func (s *BalanceSnapshotStore) Latest(chain, address string) (*BalanceSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := strings.ToLower(chain) + "-" + strings.ToLower(address) + "-"
	var latest *BalanceSnapshot
	var latestKey string
	for _, key := range s.store.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if key > latestKey {
			if snap, ok := s.store.Get(key); ok {
				latest = &snap
				latestKey = key
			}
		}
	}
	return latest, latest != nil
}

// List 返回地址全部快照（按时间倒序）。
func (s *BalanceSnapshotStore) List(chain, address string) []BalanceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := strings.ToLower(chain) + "-" + strings.ToLower(address) + "-"
	var out []BalanceSnapshot
	for _, key := range s.store.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if snap, ok := s.store.Get(key); ok {
			out = append(out, snap)
		}
	}
	// 时间倒序（key 含时间戳，倒序即新→旧）
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CapturedAt.After(out[j-1].CapturedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// SnapshotDiff 是历史对比（设计 §8：实时 vs 最近快照，变化量与变化率）。
type SnapshotDiff struct {
	Address    string  `json:"address"`
	Chain      string  `json:"chain"`
	Symbol     string  `json:"symbol"`
	Current    string  `json:"current"`     // 实时余额
	Snapshot   string  `json:"snapshot"`    // 快照余额
	SnapshotAt string  `json:"snapshot_at"` // 快照时间
	Change     float64 `json:"change"`      // 变化量（数值差值）
	ChangePct  float64 `json:"change_pct"`  // 变化率（%）
}

// Compare 对比实时资产与最新快照（设计 §8）。
func (s *BalanceSnapshotStore) Compare(chain, address string, current *AddressAssets) []SnapshotDiff {
	if current == nil {
		return nil
	}
	latest, ok := s.Latest(chain, address)
	if !ok {
		return nil
	}
	var diffs []SnapshotDiff
	snapBySymbol := map[string]AssetBalance{}
	for _, a := range latest.Assets {
		snapBySymbol[a.Symbol] = a
	}
	for _, cur := range current.Assets {
		if cur.Status != "success" {
			continue
		}
		snap, ok := snapBySymbol[cur.Symbol]
		if !ok || snap.Status != "success" {
			continue
		}
		curVal := parseFloatOrZero(cur.Balance)
		snapVal := parseFloatOrZero(snap.Balance)
		change := curVal - snapVal
		pct := 0.0
		if snapVal != 0 {
			pct = change / snapVal * 100
		}
		diffs = append(diffs, SnapshotDiff{
			Address:    current.Address,
			Chain:      chain,
			Symbol:     cur.Symbol,
			Current:    cur.Balance,
			Snapshot:   snap.Balance,
			SnapshotAt: latest.CapturedAt.UTC().Format("2006-01-02 15:04"),
			Change:     change,
			ChangePct:  pct,
		})
	}
	return diffs
}

// snapshotFilePath 返回快照目录（供装配使用；注意 dataRoot 为 cfg.RootDir，
// 实际目录为 <root>/backend/data/investigation/balance-snapshots/）。
func snapshotRoot(dataRoot string) string {
	return filepath.Join(dataRoot, "backend", "data", "investigation", "balance-snapshots")
}

// EnsureSnapshotDir 确保快照目录存在（装配时调用；should-fix：目录与 store
// 实际路径一致，避免留下废弃的 <root>/investigation/... 空目录）。
func EnsureSnapshotDir(dataRoot string) error {
	return os.MkdirAll(snapshotRoot(dataRoot), 0755)
}

// parseFloatOrZero 解析余额字符串。
func parseFloatOrZero(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}
