package feedback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Record 单次执行结果（传输层 + 最终验证）。
type Record struct {
	ChainID      int64
	Dataset      string
	Provider     string
	ScaleBucket  string
	Rows         int64
	Runtime      time.Duration
	Latency      time.Duration
	Success      bool // TransportSuccess
	FinalSuccess bool // Download + Validation Success（设计 §43/§44）
	HTTPClass    string
	Gap          bool // Validation 检测到缺口（Validation V3 反馈）
	Repair       bool // 缺口补洞成功
}

// Profile Provider 历史画像（设计 §24/§39）。
type Profile struct {
	Key               string    `json:"key"`
	ChainID           int64     `json:"chain_id"`
	Dataset           string    `json:"dataset"`
	Provider          string    `json:"provider"`
	ScaleBucket       string    `json:"scale_bucket"`
	Jobs              int64     `json:"jobs"`
	SuccessCount      int64     `json:"success_count"`
	FinalSuccessCount int64     `json:"final_success_count"`
	RowsPerSecEWMA    float64   `json:"rows_per_sec_ewma"`
	RuntimeSamples    []float64 `json:"runtime_samples"` // 秒，保留最近 100
	HTTP503Rate       float64   `json:"http_503_rate"`
	HTTP429Rate       float64   `json:"http_429_rate"`
	GapRate           float64   `json:"gap_rate"`
	RepairRate        float64   `json:"repair_rate"`
	LastUpdated       time.Time `json:"last_updated"`
}

// History Provider 历史画像存储（文件系统，无数据库）。
type History struct {
	mu       sync.Mutex
	root     string
	profiles map[string]*Profile
}

// NewHistory 创建/加载历史画像。
func NewHistory(root string) *History {
	h := &History{root: root, profiles: map[string]*Profile{}}
	dir := filepath.Join(root, "smart_download", "scheduler", "provider-profiles")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p Profile
		if json.Unmarshal(payload, &p) == nil && p.Key != "" {
			h.profiles[p.Key] = &p
		}
	}
	return h
}

// ScaleBucket 规模分桶。
func ScaleBucket(rows uint64) string {
	switch {
	case rows < 100_000:
		return "<100K"
	case rows < 500_000:
		return "100K-500K"
	case rows < 1_000_000:
		return "500K-1M"
	case rows < 5_000_000:
		return "1M-5M"
	case rows < 20_000_000:
		return "5M-20M"
	default:
		return ">20M"
	}
}

func profileKey(chainID int64, dataset, provider, bucket string) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				return r
			}
			return '_'
		}, s)
	}
	return clean(strings.ToLower(dataset)) + "_" + clean(strings.ToLower(provider)) +
		"_" + clean(bucket) + "_" + itoa(chainID)
}

// Record 记录一次结果并持久化。
func (h *History) Record(rec Record) {
	if h == nil || rec.Provider == "" || rec.Dataset == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key := profileKey(rec.ChainID, rec.Dataset, rec.Provider, rec.ScaleBucket)
	p := h.profiles[key]
	if p == nil {
		p = &Profile{Key: key, ChainID: rec.ChainID, Dataset: rec.Dataset,
			Provider: rec.Provider, ScaleBucket: rec.ScaleBucket}
		h.profiles[key] = p
	}
	p.Jobs++
	if rec.Success {
		p.SuccessCount++
	}
	if rec.FinalSuccess {
		p.FinalSuccessCount++
	}
	if rec.Rows > 0 && rec.Runtime > 0 {
		cur := float64(rec.Rows) / rec.Runtime.Seconds()
		if p.RowsPerSecEWMA <= 0 {
			p.RowsPerSecEWMA = cur
		} else {
			p.RowsPerSecEWMA = 0.3*cur + 0.7*p.RowsPerSecEWMA
		}
	}
	if rec.Runtime > 0 {
		p.RuntimeSamples = append(p.RuntimeSamples, rec.Runtime.Seconds())
		if len(p.RuntimeSamples) > 100 {
			p.RuntimeSamples = p.RuntimeSamples[len(p.RuntimeSamples)-100:]
		}
	}
	switch rec.HTTPClass {
	case "503":
		p.HTTP503Rate = ewmaRate(p.HTTP503Rate, 1)
	case "429":
		p.HTTP429Rate = ewmaRate(p.HTTP429Rate, 1)
	default:
		p.HTTP503Rate = ewmaRate(p.HTTP503Rate, 0)
		p.HTTP429Rate = ewmaRate(p.HTTP429Rate, 0)
	}
	if rec.Gap {
		p.GapRate = ewmaRate(p.GapRate, 1)
	} else if p.Jobs > 1 {
		p.GapRate = ewmaRate(p.GapRate, 0)
	}
	if rec.Repair {
		p.RepairRate = ewmaRate(p.RepairRate, 1)
	} else if p.Jobs > 1 {
		p.RepairRate = ewmaRate(p.RepairRate, 0)
	}
	p.LastUpdated = time.Now().UTC()
	h.saveLocked(p)
}

func ewmaRate(prev float64, hit float64) float64 {
	return 0.05*hit + 0.95*prev
}

// Profile 返回指定画像副本。
func (h *History) Profile(chainID int64, dataset, provider, bucket string) *Profile {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.profiles[profileKey(chainID, dataset, provider, bucket)]
	if !ok {
		return nil
	}
	cp := *p
	cp.RuntimeSamples = append([]float64(nil), p.RuntimeSamples...)
	return &cp
}

// ScoreBonus 历史画像对 Provider 评分的加成（0-20）。
func (h *History) ScoreBonus(chainID int64, dataset, provider, bucket string) float64 {
	p := h.Profile(chainID, dataset, provider, bucket)
	if p == nil || p.Jobs == 0 {
		return 0
	}
	success := float64(p.FinalSuccessCount) / float64(p.Jobs)
	score := success * 10
	if p.RowsPerSecEWMA > 0 {
		score += minFloat(p.RowsPerSecEWMA/5000, 10)
	}
	return score
}

// P50/P95 运行时长分位数（秒）。
func (p *Profile) P50() float64 { return percentile(p.RuntimeSamples, 0.5) }
func (p *Profile) P95() float64 { return percentile(p.RuntimeSamples, 0.95) }

func percentile(samples []float64, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)-1) * q)
	return sorted[idx]
}

func (h *History) saveLocked(p *Profile) {
	dir := filepath.Join(h.root, "smart_download", "scheduler", "provider-profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	payload, _ := json.MarshalIndent(p, "", "  ")
	path := filepath.Join(dir, p.Key+".json")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, payload, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
