package reportengine

import (
	"fmt"
	"math"
	"time"

	"github.com/etl/backend/internal/fundflow"
)

// BuildTimeline 从资金流分析构建证据时间线（设计 §12-§16，只保留关键事件）。
func BuildTimeline(ff *fundflow.AnalysisResult, findings []Finding) []TimelineEvent {
	if ff == nil {
		return nil
	}
	var events []TimelineEvent
	// 路径节点事件
	for _, p := range ff.Paths {
		for _, n := range p.Nodes {
			evType := "LARGE_INFLOW"
			if n.EdgeType == "SWEEP" || n.EdgeType == "COLLECT" {
				evType = "SWEEP"
			} else if n.EdgeType == "DISTRIBUTE" {
				evType = "DISTRIBUTION"
			} else if n.EdgeType == "DEPOSIT" {
				evType = "EXCHANGE_DEPOSIT"
			} else if n.EdgeType == "BRIDGE_OUT" || n.EdgeType == "BRIDGE_IN" {
				evType = "BRIDGE"
			}
			events = append(events, TimelineEvent{
				ID: "tl_" + p.ID + "_" + shortID(n.Address),
				Timestamp: blockTime(n.BlockNumber),
				Type: evType, SubjectIDs: []string{n.Address, p.RootAddress},
				Summary: fmt.Sprintf("%s 路径事件：%s 收到 %s", p.PathType, n.Address, n.InAmount),
				Amount: n.InAmount, Token: n.Token, TxHash: n.EdgeTxHash,
				EvidenceIDs: []string{"ev_path_" + p.ID},
				ImportanceScore: timelineScore(p.Score, n.InAmount),
			})
		}
	}
	// 兑现事件
	for _, c := range ff.Cashouts {
		events = append(events, TimelineEvent{
			ID: "tl_cashout_" + shortID(c.DestinationAddress),
			Timestamp: time.Now().UTC(), Type: "EXCHANGE_DEPOSIT",
			SubjectIDs: []string{c.SourceAddress, c.DestinationAddress},
			Summary: fmt.Sprintf("资金进入 %s（%s）", c.DestinationAddress, c.EntityName),
			Amount: c.Amount, Token: c.Token, TxHash: c.TxHash,
			EvidenceIDs: []string{"ev_cashout_" + shortID(c.DestinationAddress)},
			ImportanceScore: 1.0,
		})
	}
	// 沉淀事件
	for _, s := range ff.Settlements {
		events = append(events, TimelineEvent{
			ID: "tl_settlement_" + shortID(s.Address),
			Timestamp: time.Now().UTC(), Type: "SETTLEMENT",
			SubjectIDs: []string{s.Address},
			Summary: fmt.Sprintf("%s 沉淀候选（%s），沉淀分 %.2f", s.Address, s.SettlementType, s.SettlementScore),
			Amount: s.RetainedValue, EvidenceIDs: []string{"ev_settlement_" + s.Address},
			ImportanceScore: s.SettlementScore,
		})
	}
	// 排序后截断 200 条
	sortTimeline(events)
	if len(events) > 200 {
		events = events[:200]
	}
	return events
}

func timelineScore(pathScore float64, amount string) float64 {
	s := pathScore
	if v, ok := parseFloatAmount(amount); ok && v > 0 {
		s += math.Min(math.Log10(v+1)/12, 1)
	}
	if s > 1 {
		return 1
	}
	return s
}

func blockTime(block uint64) time.Time {
	// BSC 创世约 2020-08-29 00:00 UTC，3s/块；用于时间线排序与展示（估算值）
	if block == 0 {
		return time.Time{}
	}
	return time.Unix(1598227200+int64(block)*3, 0).UTC()
}

func sortTimeline(events []TimelineEvent) {
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j-1].ImportanceScore < events[j].ImportanceScore; j-- {
			events[j-1], events[j] = events[j], events[j-1]
		}
	}
}
