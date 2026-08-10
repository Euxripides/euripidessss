package smartdownload

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Range Ledger 事件类型（实施方案 §5：恢复和审计的事实账本）。
const (
	LedgerRangeCreated            = "RANGE_CREATED"
	LedgerRangeStarted            = "RANGE_STARTED"
	LedgerPartCommitted           = "PART_COMMITTED"
	LedgerRangeCompleted          = "RANGE_COMPLETED"
	LedgerRangeEmpty              = "RANGE_EMPTY"
	LedgerRangeFailed             = "RANGE_FAILED"
	LedgerProviderFailed          = "PROVIDER_FAILED"
	LedgerProviderSwitched        = "PROVIDER_SWITCHED"
	LedgerCloudTierAssigned       = "CLOUD_TIER_ASSIGNED"
	LedgerCloudTierUpgraded       = "CLOUD_TIER_UPGRADED"
	LedgerCloudTierDowngraded     = "CLOUD_TIER_DOWNGRADED"
	LedgerFeedbackAction          = "FEEDBACK_ACTION"
	LedgerPaused                  = "PAUSED"
	LedgerResumed                 = "RESUMED"
	LedgerCanceled                = "CANCELED"
	LedgerRangeAssigned           = "RANGE_ASSIGNED"
	LedgerModeSwitched            = "MODE_SWITCHED"
	LedgerRangeResharded          = "RANGE_RESHARDED"
	LedgerRangeCertified          = "RANGE_CERTIFIED"
	LedgerDatasetPartialCertified = "DATASET_PARTIAL_CERTIFIED"
	LedgerHedgeStarted            = "HEDGE_STARTED"
	LedgerHedgeWon                = "HEDGE_WON"
	LedgerPausedByPriority        = "PAUSED_BY_PRIORITY"
	LedgerAutoResumed             = "AUTO_RESUMED_BY_PRIORITY"
	LedgerSelfRecovery            = "SELF_RECOVERY"
)

// LedgerEntry 单条 Range Ledger 记录。
type LedgerEntry struct {
	TS           time.Time `json:"ts"`
	Event        string    `json:"event"`
	DatasetJobID string    `json:"dataset_job_id,omitempty"`
	RangeID      string    `json:"range_id,omitempty"`
	FromBlock    uint64    `json:"from_block,omitempty"`
	ToBlock      uint64    `json:"to_block,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	Owner        string    `json:"owner,omitempty"`
	Part         string    `json:"part,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	Rows         int64     `json:"rows,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Ledger 每个 DatasetJob 一个 ndjson 追加日志（root/smart_download/ledgers/{id}.ndjson）。
type Ledger struct {
	mu   sync.Mutex
	path string
}

// NewLedger 创建/打开 Range Ledger。
func NewLedger(root, datasetJobID string) *Ledger {
	return &Ledger{path: filepath.Join(root, "smart_download", "ledgers", datasetJobID+".ndjson")}
}

// Append 追加一条事件（先落盘再返回；文件句柄每次打开关闭，保证跨进程可见）。
func (l *Ledger) Append(e LedgerEntry) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(payload, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Replay 顺序回放全部事件。
func (l *Ledger) Replay() ([]LedgerEntry, error) {
	if l == nil {
		return nil, nil
	}
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []LedgerEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e LedgerEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("ledger 损坏: %w", err)
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Path 返回 ledger 文件路径。
func (l *Ledger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
