package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"
)

// Config for benchmark execution.
type Config struct {
	Name     string           `json:"name"`
	Workload string           `json:"workload"` // parquet | duckdb | pipeline | all
	Params   map[string]any   `json:"params"`
	Targets  []Target         `json:"targets"`
}

type Target struct {
	RowsPerSec float64 `json:"rows_per_sec"`
	QueryMS    float64 `json:"query_ms"`
	MemMB      float64 `json:"mem_mb"`
}

// Result holds a single benchmark run result.
type Result struct {
	Name       string         `json:"name"`
	Workload   string         `json:"workload"`
	Params     map[string]any `json:"params"`
	Duration   time.Duration  `json:"duration"`
	SysMetrics SystemMetrics  `json:"sys_metrics"`
	AppMetrics AppMetrics     `json:"app_metrics"`
	Passed     bool           `json:"passed"`
	Error      string         `json:"error,omitempty"`
	Output     []string       `json:"output,omitempty"`
}

type SystemMetrics struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryMB    float64 `json:"memory_mb"`
	HeapMB      float64 `json:"heap_mb"`
	NumGC       uint32  `json:"num_gc"`
}

type AppMetrics struct {
	RowsTotal   int64   `json:"rows_total,omitempty"`
	ChunksTotal int64   `json:"chunks_total,omitempty"`
	FilesTotal  int64   `json:"files_total,omitempty"`
	RowsPerSec  float64 `json:"rows_per_sec,omitempty"`
	MBPerSec    float64 `json:"mb_per_sec,omitempty"`
	FileSizeMB  float64 `json:"file_size_mb,omitempty"`
}

// Report is the final benchmark output.
type Report struct {
	Version   string    `json:"version"`
	Generated time.Time `json:"generated"`
	Env       EnvInfo   `json:"env"`
	Results   []Result  `json:"results"`
	Summary   string    `json:"summary"`
}

type EnvInfo struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	GoMax   int    `json:"gomaxprocs"`
	NumCPU  int    `json:"num_cpu"`
	MemMB   uint64 `json:"total_mem_mb"`
}

// Snapshot captures current system state.
func Snapshot() SystemMetrics {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return SystemMetrics{
		MemoryMB: float64(m.Alloc) / 1e6,
		HeapMB:   float64(m.HeapAlloc) / 1e6,
		NumGC:    m.NumGC,
	}
}

// Runner executes benchmark workloads.
type Runner struct {
	Config  Config
	Results []Result
	Env     EnvInfo
}

func New(cfg Config) *Runner {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return &Runner{
		Config: cfg,
		Env: EnvInfo{
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			GoMax:   runtime.GOMAXPROCS(0),
			NumCPU:  runtime.NumCPU(),
		},
	}
}

func (r *Runner) Run(name string, fn func() (AppMetrics, error)) {
	before := Snapshot()
	start := time.Now()
	app, err := fn()
	dur := time.Since(start)
	after := Snapshot()

	result := Result{
		Name:       name,
		Workload:   r.Config.Workload,
		Params:     r.Config.Params,
		Duration:   dur,
		SysMetrics: SystemMetrics{MemoryMB: after.MemoryMB - before.MemoryMB, HeapMB: after.HeapMB, NumGC: after.NumGC - before.NumGC},
		AppMetrics: app,
	}
	if err != nil {
		result.Error = err.Error()
	}
	result.Passed = result.Error == "" && r.checkTargets(result)
	r.Results = append(r.Results, result)
}

func (r *Runner) checkTargets(res Result) bool {
	for _, t := range r.Config.Targets {
		if t.RowsPerSec > 0 && res.AppMetrics.RowsPerSec < t.RowsPerSec {
			return false
		}
		if t.QueryMS > 0 && float64(res.Duration.Milliseconds()) > t.QueryMS {
			return false
		}
	}
	return true
}

func (r *Runner) GenerateReport() Report {
	passed := 0
	for _, res := range r.Results {
		if res.Passed {
			passed++
		}
	}
	summary := fmt.Sprintf("%d/%d passed", passed, len(r.Results))
	if passed < len(r.Results) {
		summary += " — NOT STABLE"
	} else {
		summary += " — STABLE ✅"
	}
	return Report{
		Version:   "V2.1-RC2",
		Generated: time.Now().UTC(),
		Env:       r.Env,
		Results:   r.Results,
		Summary:   summary,
	}
}

func (r *Runner) WriteReport(path string) error {
	report := r.GenerateReport()
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	mdPath := path + ".md"
	md := r.markdownReport(report)
	return os.WriteFile(mdPath, []byte(md), 0644)
}

func (r *Runner) markdownReport(rep Report) string {
	md := fmt.Sprintf(`# Benchmark Report — %s

**Generated:** %s  
**Environment:** %s/%s | %d CPUs | GOMAXPROCS=%d

## Results

| Test | Duration | Rows/s | MB/s | File | Pass |
|------|----------|--------|------|------|------|
`, rep.Version, rep.Generated.Format(time.RFC3339), rep.Env.OS, rep.Env.Arch, rep.Env.NumCPU, rep.Env.GoMax)

	for _, res := range rep.Results {
		pass := "❌"
		if res.Passed {
			pass = "✅"
		}
		md += fmt.Sprintf("| %s | %v | %.0f | %.1f | %.1f MB | %s |\n",
			res.Name, res.Duration.Round(time.Millisecond), res.AppMetrics.RowsPerSec, res.AppMetrics.MBPerSec, res.AppMetrics.FileSizeMB, pass)
	}

	md += fmt.Sprintf("\n## Summary\n\n%s\n", rep.Summary)
	return md
}
