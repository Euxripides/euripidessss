package dynamicinvestigation

// ── 智能采集路由 ──
//
// 普通钱包：SQD 增量（Logs → Transfer → Transactions → Trace 逐级升级）
// 大型实体：CSV 直链下载 → 过滤目标交易 → 导入分析
// 低价值地址：仅保存关系
//
// 数据等级升级：Level 0 发现 → Level 1 Logs → Level 2 Transfer
// → Level 3 Transactions → Level 4 Trace。先低成本判断，再提高采集级别。

// RouteInput 是采集路由决策输入。
type RouteInput struct {
	Entity     EntityType
	Decision   Decision
	Score      float64
	CurrentLevel DataLevel
	Depth      int
}

// RouteResult 是路由决策结果。
type RouteResult struct {
	Mode        AcquisitionMode `json:"mode"`
	TargetLevel DataLevel       `json:"target_level"`
	FromLevel   DataLevel       `json:"from_level"`
	Reason      string          `json:"reason"`
}

// Route 根据实体类型与评分决策选择采集方式。
func Route(input RouteInput, cfg ExpansionConfig) RouteResult {
	// 低价值/忽略：仅保存关系
	if input.Decision == DecisionIgnore {
		return RouteResult{
			Mode:        AcquisitionRelationsOnly,
			TargetLevel: input.CurrentLevel,
			FromLevel:   input.CurrentLevel,
			Reason:      "低价值地址，仅保存关系",
		}
	}

	// 起点地址（深度 0）与普通钱包：SQD 增量
	if input.Depth == 0 || input.Entity == EntityWallet || input.Entity == EntityUnknown {
		if cfg.UseSQD {
			return RouteResult{
				Mode:        AcquisitionSQDLogs,
				TargetLevel: nextLevel(input.CurrentLevel),
				FromLevel:   input.CurrentLevel,
				Reason:      "普通钱包：SQD 增量，数据等级逐级升级",
			}
		}
		return RouteResult{
			Mode:        AcquisitionRelationsOnly,
			TargetLevel: input.CurrentLevel,
			FromLevel:   input.CurrentLevel,
			Reason:      "SQD 未启用，仅保存关系",
		}
	}

	// 大型实体：交易所/桥/DEX → CSV 直链
	switch input.Entity {
	case EntityExchange, EntityBridge, EntityDex, EntityRouter:
		if cfg.UseCSVDirect {
			return RouteResult{
				Mode:        AcquisitionCSVDirect,
				TargetLevel: LevelTransfer, // CSV 直链提供完整转账/交易数据
				FromLevel:   input.CurrentLevel,
				Reason:      "大型实体：CSV 直链下载，过滤目标交易后导入分析",
			}
		}
		// CSV 未启用时退回 SQD
		if cfg.UseSQD {
			return RouteResult{
				Mode:        AcquisitionSQDLogs,
				TargetLevel: nextLevel(input.CurrentLevel),
				FromLevel:   input.CurrentLevel,
				Reason:      "CSV 直链未启用，退回 SQD 增量",
			}
		}
		return RouteResult{
			Mode:        AcquisitionRelationsOnly,
			TargetLevel: input.CurrentLevel,
			FromLevel:   input.CurrentLevel,
			Reason:      "无可用采集通道，仅保存关系",
		}
	case EntityContract:
		// 纯合约：仅当关联金额显著时才采集 Logs
		if cfg.UseSQD && input.Score >= cfg.MinScore {
			return RouteResult{
				Mode:        AcquisitionSQDLogs,
				TargetLevel: LevelLogs,
				FromLevel:   input.CurrentLevel,
				Reason:      "合约地址：按需采集 Logs 事件",
			}
		}
		return RouteResult{
			Mode:        AcquisitionRelationsOnly,
			TargetLevel: input.CurrentLevel,
			FromLevel:   input.CurrentLevel,
			Reason:      "合约地址：仅保存关系",
		}
	}

	return RouteResult{
		Mode:        AcquisitionRelationsOnly,
		TargetLevel: input.CurrentLevel,
		FromLevel:   input.CurrentLevel,
		Reason:      "未识别实体类型，仅保存关系",
	}
}

// nextLevel 返回下一数据等级（上限 LevelTrace）。
func nextLevel(l DataLevel) DataLevel {
	if l >= LevelTrace {
		return LevelTrace
	}
	return l + 1
}

// ShouldUpgrade 判断是否应升级数据等级：
// 当前等级 < 目标等级（且未达上限）→ 可升级。
func ShouldUpgrade(current, target DataLevel) bool {
	return current < target && target <= LevelTrace
}
