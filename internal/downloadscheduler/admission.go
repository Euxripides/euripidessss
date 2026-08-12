package downloadscheduler

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CloudBudget Cloud 预算 Guard（设计 §82/§107：保守 V1 上限）。
type CloudBudget struct {
	Enabled                bool `json:"enabled"`
	DailyLimitMinutes      int  `json:"daily_limit_minutes"`
	MaxConcurrentWorkers   int  `json:"max_concurrent_workers"`
	IdleRemoveAfterMinutes int  `json:"idle_remove_after_minutes"`
	DeployTimeoutMinutes   int  `json:"deploy_timeout_minutes"`
}

// DefaultCloudBudget V1 推荐：启用、每日 60 分钟、单 Worker、空闲 20 分钟回收。
func DefaultCloudBudget() CloudBudget {
	return CloudBudget{
		Enabled:                true,
		DailyLimitMinutes:      60,
		MaxConcurrentWorkers:   1,
		IdleRemoveAfterMinutes: 20,
		DeployTimeoutMinutes:   10,
	}
}

// CloudUsageRecord Cloud 用量审计记录（设计 §99：任何 Cloud 启动都可审计）。
type CloudUsageRecord struct {
	JobID           string    `json:"job_id"`
	PlanID          string    `json:"plan_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	Mode            string    `json:"mode"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	DurationMinutes int       `json:"duration_minutes"`
	Rows            int64     `json:"rows"`
	Output          string    `json:"output,omitempty"`
	Success         bool      `json:"success"`
}

// CloudUsage 当日用量汇总（cloud_usage.json）。
type CloudUsage struct {
	Date        string             `json:"date"` // YYYY-MM-DD（本地）
	UsedMinutes int                `json:"used_minutes"`
	Records     []CloudUsageRecord `json:"records"`
}

// CloudUsageStore 文件系统用量审计（原子写，保留最近 200 条）。
type CloudUsageStore struct {
	mu   sync.Mutex
	path string
}

// NewCloudUsageStore 创建用量存储；path 为空时不持久化（仅内存）。
func NewCloudUsageStore(path string) *CloudUsageStore {
	return &CloudUsageStore{path: path}
}

// TodayUsedMinutes 返回当日已用分钟数。
func (s *CloudUsageStore) TodayUsedMinutes() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.load()
	if u.Date != time.Now().Format("2006-01-02") {
		return 0
	}
	return u.UsedMinutes
}

// Record 记录一次 Cloud 用量。
func (s *CloudUsageStore) Record(rec CloudUsageRecord) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.load()
	if rec.DurationMinutes < 0 {
		return errors.New("Cloud 用量时长不能为负数")
	}
	today := time.Now().Format("2006-01-02")
	if u.Date != today {
		u = CloudUsage{Date: today}
	}
	u.Records = append(u.Records, rec)
	if len(u.Records) > 200 {
		u.Records = u.Records[len(u.Records)-200:]
	}
	u.UsedMinutes = 0
	for _, r := range u.Records {
		u.UsedMinutes += r.DurationMinutes
	}
	return s.save(u)
}

// Usage 返回当前用量快照。
func (s *CloudUsageStore) Usage() CloudUsage {
	if s == nil {
		return CloudUsage{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.load()
	if u.Date != time.Now().Format("2006-01-02") {
		return CloudUsage{Date: time.Now().Format("2006-01-02")}
	}
	return u
}

func (s *CloudUsageStore) load() CloudUsage {
	if s.path == "" {
		return CloudUsage{Date: time.Now().Format("2006-01-02")}
	}
	payload, err := os.ReadFile(s.path)
	if err != nil {
		return CloudUsage{Date: time.Now().Format("2006-01-02")}
	}
	var u CloudUsage
	if json.Unmarshal(payload, &u) != nil {
		return CloudUsage{Date: time.Now().Format("2006-01-02")}
	}
	return u
}

func (s *CloudUsageStore) save(u CloudUsage) error {
	payload, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// CloudRuntimeStatus Cloud 运行时状态（Admission Gate 输入，避免调度器依赖 cloudruntime 类型）。
type CloudRuntimeStatus struct {
	State                   string `json:"state"`
	Mode                    string `json:"mode,omitempty"`
	Available               bool   `json:"available"`
	Reason                  string `json:"reason,omitempty"`
	QueuedJobs              int    `json:"queued_jobs"`
	LeasedJobs              int    `json:"leased_jobs"`
	RunningJob              string `json:"running_job,omitempty"`
	FailureCooldownUntil    string `json:"failure_cooldown_until,omitempty"`
	DeploymentKeyConfigured bool   `json:"deployment_key_configured"`
	R2Configured            bool   `json:"r2_configured"`
}

// CloudAdmissionDecision Cloud Admission Gate 判定结果（设计 §15/§80）。
type CloudAdmissionDecision struct {
	Allowed                  bool              `json:"allowed"`
	Reason                   string            `json:"reason,omitempty"`
	MissingCoverage          bool              `json:"missing_coverage"`
	DatasetSupported         bool              `json:"dataset_supported"`
	NormalProvidersExhausted bool              `json:"normal_providers_exhausted"`
	CloudEligible            bool              `json:"cloud_eligible"`
	BudgetAllowed            bool              `json:"budget_allowed"`
	RuntimeAvailable         bool              `json:"runtime_available"`
	RuntimeDeployable        bool              `json:"runtime_deployable"`
	RuntimeState             string            `json:"runtime_state,omitempty"`
	ProviderStates           map[string]string `json:"provider_states,omitempty"`
}

// CloudAdmissionGate 判断当前任务是否真正需要启用付费 Cloud Provider（设计 §4/§15）。
// 必须同时满足：覆盖缺口、数据类型支持、所有常规 Provider 耗尽、CloudEligible、预算允许、运行时可用。
type CloudAdmissionGate struct {
	usage  *CloudUsageStore
	health *ProviderHealthTracker
	budget CloudBudget
}

// NewCloudAdmissionGate 创建准入 Gate。
func NewCloudAdmissionGate(usage *CloudUsageStore, health *ProviderHealthTracker, budget CloudBudget) *CloudAdmissionGate {
	return &CloudAdmissionGate{usage: usage, health: health, budget: budget}
}

// CanUseSQDCloud 执行 Cloud 准入判定（V1 简化版，设计 §80）。
// coverage 为 nil 时视为存在覆盖缺口（本地数据集不可用时无法证明已覆盖）。
func (g *CloudAdmissionGate) CanUseSQDCloud(
	req Requirement,
	coverage *CoverageResult,
	candidates []ProviderScore,
	states map[ProviderKind]ProviderStateInfo,
	rt CloudRuntimeStatus,
) CloudAdmissionDecision {
	d := CloudAdmissionDecision{
		ProviderStates: map[string]string{},
	}
	for k, v := range states {
		d.ProviderStates[string(k)] = string(v.State)
	}

	// 条件 A：Dataset Coverage 存在真实缺口
	d.MissingCoverage = true
	if coverage != nil {
		for _, item := range coverage.Items {
			if item.Dataset == req.Dataset && item.Have {
				d.MissingCoverage = false
				break
			}
		}
	}
	if !d.MissingCoverage {
		d.Reason = "LOCAL_COVERAGE_FULL：本地数据已覆盖，禁止启动 Cloud"
		return d
	}

	// 条件 B：当前数据类型适合 SQD Cloud（V1 仅 token_transfer）
	d.DatasetSupported = req.Dataset == DatasetTokenTransfer
	if !d.DatasetSupported {
		d.Reason = "CLOUD_UNSUPPORTED_DATASET：V1 仅支持 token_transfer"
		return d
	}

	// 条件 C：所有正常 Provider 都不可用（单次 503 不满足）
	d.NormalProvidersExhausted = !NormalProvidersUsable(candidates, func(k ProviderKind) ProviderState {
		if info, ok := states[k]; ok {
			return info.State
		}
		return ProviderHealthy
	})
	if !d.NormalProvidersExhausted {
		d.Reason = "NORMAL_PROVIDER_AVAILABLE：仍有常规数据源可用，禁止启动 Cloud"
		return d
	}

	// 条件 D：任务 CloudEligible（交互/调查/手动默认允许；后台预取关闭）
	d.CloudEligible = req.CloudAllowed()
	if !d.CloudEligible {
		d.Reason = "CLOUD_NOT_ELIGIBLE：后台/非关键任务不允许触发 Cloud"
		return d
	}

	// 条件 E：预算 Guard
	d.BudgetAllowed = g.budget.Enabled && g.usage.TodayUsedMinutes() < g.budget.DailyLimitMinutes
	if !d.BudgetAllowed {
		if !g.budget.Enabled {
			d.Reason = "CLOUD_DISABLED：Cloud 预算开关关闭"
		} else {
			d.Reason = "CLOUD_BUDGET_EXCEEDED：当日 Cloud 用量已达上限"
		}
		return d
	}

	// 条件 F：运行时已就绪，或处于凭据齐全的 ABSENT 可部署状态。
	// ABSENT 不能冒充 runtime_available/ProviderHealthy；但 Gate 可允许一次受控
	// Submit，由 SubmitJob 在入队前强制 EnsureWorker + sqd list 对账。
	d.RuntimeState = rt.State
	runtimeHealthyState := rt.State == "READY" || rt.State == "BUSY" || rt.State == "IDLE"
	d.RuntimeAvailable = rt.Available && runtimeHealthyState && rt.FailureCooldownUntil == ""
	d.RuntimeDeployable = (rt.State == "ABSENT" || rt.State == "DEPLOYING" || rt.State == "STARTING") && rt.Mode == "cloud" &&
		rt.DeploymentKeyConfigured && rt.R2Configured && rt.FailureCooldownUntil == ""
	if !d.RuntimeAvailable && !d.RuntimeDeployable {
		if strings.Contains(rt.Reason, "SQD_DEPLOY_KEY") || strings.Contains(rt.Reason, "R2/S3") {
			d.Reason = "CREDENTIALS_NOT_CONFIGURED：" + rt.Reason
		} else {
			d.Reason = "CLOUD_RUNTIME_UNAVAILABLE：" + rt.Reason
		}
		return d
	}

	d.Allowed = true
	d.Reason = "ALL_NORMAL_PROVIDERS_EXHAUSTED：常规数据源均不可用，允许应急 Cloud 通道"
	return d
}

// CloudTaskInfo 计划任务中的 Cloud 兜底信息。
type CloudTaskInfo struct {
	JobID    string                 `json:"job_id,omitempty"`
	Decision CloudAdmissionDecision `json:"decision"`
	Mode     string                 `json:"mode,omitempty"`
	Output   string                 `json:"output,omitempty"`
}

// CloudRunInfo 计划级 Cloud 汇总。
type CloudRunInfo struct {
	AdmittedTasks int      `json:"admitted_tasks"`
	RejectedTasks int      `json:"rejected_tasks"`
	RejectReasons []string `json:"reject_reasons,omitempty"`
}
