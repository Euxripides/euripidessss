package validation

// RangePurpose Range 目的（设计 §25）。
type RangePurpose string

const (
	PurposePrimary RangePurpose = "PRIMARY"
	PurposeRepair  RangePurpose = "REPAIR"
	PurposeVerify  RangePurpose = "VERIFY"
)

// RepairPlanner 补洞 Provider 选择（设计 §26/§27）：
// 优先未使用过且不在黑名单的 Provider；顺序偏好 RPC 精确补洞 > SQD > SQD Cloud。
type RepairPlanner struct {
	Available  []string // 全部可用 Provider
	Used       []string // 主任务已使用 Provider
	Blacklist  []string // 疑似导致缺口的 Provider（设计 §27）
	Preference []string // 优先顺序（rpc, sqd, sqd_cloud, csv）
}

// NewRepairPlanner 创建补洞规划器。
func NewRepairPlanner(available, used, blacklist []string) *RepairPlanner {
	return &RepairPlanner{
		Available: available, Used: used, Blacklist: blacklist,
		Preference: []string{"rpc", "sqd", "sqd_cloud"},
	}
}

// Select 选择补洞 Provider；无可用返回空。
func (p *RepairPlanner) Select() string {
	contains := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	// 第一轮：未使用过 且 不在黑名单，按偏好顺序
	for _, pref := range p.Preference {
		if contains(p.Available, pref) && !contains(p.Used, pref) && !contains(p.Blacklist, pref) {
			return pref
		}
	}
	// 第二轮：未使用过但偏好未命中（任意可用非黑名单）
	for _, a := range p.Available {
		if !contains(p.Used, a) && !contains(p.Blacklist, a) {
			return a
		}
	}
	// 第三轮：全部已使用过，但避开黑名单
	for _, a := range p.Available {
		if !contains(p.Blacklist, a) {
			return a
		}
	}
	return ""
}

// MaxRepairAttempts 单个 Gap 补洞上限（设计 §28）。
const MaxRepairAttempts = 3
