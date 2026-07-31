package casefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ── V2.1 RC2: 案件智能报告（7 部分结构 + HTML） ──

// GenerateMarkdownFull 生成 7 部分完整报告。
func (c *Case) GenerateMarkdownFull() string {
	var b strings.Builder
	// 第一部分：案件摘要
	b.WriteString("案件分析报告\n\n")
	b.WriteString("一、案件摘要\n\n")
	b.WriteString(fmt.Sprintf("案件编号：%s\n", c.CaseID))
	b.WriteString(fmt.Sprintf("案件标题：%s\n", c.Title))
	b.WriteString(fmt.Sprintf("调查员：%s\n", c.Investigator))
	b.WriteString(fmt.Sprintf("分析时间：%s\n", c.CreatedAt.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("数据版本：%s\n", c.DatasetVersion))
	b.WriteString(fmt.Sprintf("目标地址：%d 个\n", len(c.TargetAddresses)))
	b.WriteString(fmt.Sprintf("状态：%s\n\n", c.Status))

	// 第二部分：地址画像
	b.WriteString("地址画像\n\n")
	for _, addr := range c.TargetAddresses {
		if s, ok := c.Summaries[addr]; ok {
			b.WriteString(fmt.Sprintf("地址 %s：类型 %s，首次活动 %s，最近活动 %s，交易 %d 笔，风险 %.1f（%s）\n",
				addr, s.AddressType, s.Profile.FirstActivityTime, s.Profile.LastActivityTime,
				s.Profile.TransactionCount, s.Risk.RiskScore, s.Risk.RiskLevel))
		}
	}

	// 第三部分：资产概览
	b.WriteString("\n资产概览\n\n")
	for _, addr := range c.TargetAddresses {
		if snap, ok := c.Assets[addr]; ok {
			b.WriteString(fmt.Sprintf("地址 %s：\n", addr))
			for _, bal := range snap.Balances {
				b.WriteString(fmt.Sprintf("  %s：%s（raw，in %s / out %s）\n",
					bal.Symbol, bal.Balance, bal.InTotal, bal.OutTotal))
			}
			for _, h := range snap.HistoryHigh {
				b.WriteString(fmt.Sprintf("  历史最高 %s：%s @ block %s\n", h.Symbol, h.Balance, h.Block))
			}
			b.WriteString(fmt.Sprintf("  风险：%s（change_rate %.2f，liquidation %v）\n",
				snap.Risk.Level, snap.Risk.BalanceChangeRate, snap.Risk.LiquidationSignal))
		}
	}

	// 第四部分：资金流分析
	b.WriteString("\n资金流分析\n\n")
	b.WriteString(fmt.Sprintf("追踪路径 %d 条\n", len(c.TracePaths)))
	for _, p := range c.TracePaths {
		b.WriteString(fmt.Sprintf("- %s（%d 跳）", strings.Join(p.Nodes, " → "), p.Hops))
		if len(p.Edges) > 0 {
			e := p.Edges[0]
			b.WriteString(fmt.Sprintf("  %s %s（block %s，tx %s）\n", e.Token, e.Amount, e.Block, e.TxHash))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n公共资金来源\n\n")
	for _, s := range c.CommonSources {
		b.WriteString(fmt.Sprintf("- %s（覆盖 %d 个目标）\n", s.Address, s.Count))
	}
	b.WriteString("\n公共去向\n\n")
	for _, s := range c.CommonSinks {
		b.WriteString(fmt.Sprintf("- %s（覆盖 %d 个目标）\n", s.Address, s.Count))
	}

	// 第五部分：资金路径
	b.WriteString("\n资金路径\n\n")
	for _, p := range c.TracePaths {
		b.WriteString(fmt.Sprintf("- %s\n", strings.Join(p.Nodes, " ↓ ")))
		for _, e := range p.Edges {
			b.WriteString(fmt.Sprintf("  %s %s %s（block %s，tx %s）\n", e.Token, e.Amount, e.TxHash, e.Block, e.TxHash))
		}
	}

	// 第六部分：关系图谱
	b.WriteString("\n关系图谱\n\n")
	if c.Graph != nil {
		b.WriteString(fmt.Sprintf("节点 %d 个，边 %d 条\n", len(c.Graph.Nodes), len(c.Graph.Edges)))
	}
	b.WriteString("\n关联地址\n\n")
	for _, r := range c.Related {
		b.WriteString(fmt.Sprintf("- %s（score %.3f，共享 %d 对手）\n", r.Address, r.Score, r.SharedCounterparties))
	}

	// 第七部分：风险分析
	b.WriteString("\n风险分析\n\n")
	for _, addr := range c.TargetAddresses {
		if r, ok := c.Risks[addr]; ok {
			b.WriteString(fmt.Sprintf("地址 %s：风险 %.1f（%s），模式：%s\n", addr, r.Risk.RiskScore, r.Risk.RiskLevel, r.Pattern))
			for _, li := range r.LargeInflows {
				b.WriteString(fmt.Sprintf("  大额转入 %s %s ← %s（block %s）\n", li.Token, li.Amount, li.From, li.Block))
			}
			for _, st := range r.SpreadTargets {
				b.WriteString(fmt.Sprintf("  分散目标 %s：%d 笔，合计 %s\n", st.Address, st.Count, st.Total))
			}
		}
	}

	// 结论
	b.WriteString("\n调查结论\n\n")
	if c.Status == StatusCompleted {
		b.WriteString("已完成调查分析：地址画像、资产快照、资金流向、关系网络、风险判断均已生成，证据链完整可追溯（dataset_version + block + tx_hash + log_index）。\n")
	} else {
		b.WriteString(fmt.Sprintf("调查未完成：%s\n", c.Error))
	}
	return b.String()
}

// GenerateHTML 生成 7 部分 HTML 报告（自包含，浏览展示/案件归档）。
func (c *Case) GenerateHTML(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh\"><head><meta charset=\"utf-8\">")
	b.WriteString("<title>" + htmlEscape(c.Title) + "</title>")
	b.WriteString("<style>body{font-family:仿宋,serif;font-size:12pt;margin:2em;line-height:1.6}")
	b.WriteString("h1,h2,h3{font-family:黑体,serif}.sec{margin-bottom:1.5em}table{border-collapse:collapse;width:100%}")
	b.WriteString("td,th{border:1px solid #ccc;padding:4px 8px;font-size:11pt}</style></head><body>")
	b.WriteString("<h1>案件分析报告</h1>")

	// 摘要
	b.WriteString("<div class=\"sec\"><h2>一、案件摘要</h2>")
	b.WriteString(fmt.Sprintf("<p>案件编号：%s</p><p>案件标题：%s</p>", htmlEscape(c.CaseID), htmlEscape(c.Title)))
	b.WriteString(fmt.Sprintf("<p>调查员：%s</p><p>分析时间：%s</p><p>数据版本：%s</p><p>目标地址：%d 个</p><p>状态：%s</p></div>",
		htmlEscape(c.Investigator), c.CreatedAt.Format("2006-01-02 15:04:05"),
		htmlEscape(c.DatasetVersion), len(c.TargetAddresses), c.Status))

	// 画像
	b.WriteString("<div class=\"sec\"><h2>二、地址画像</h2>")
	b.WriteString("<table><tr><th>地址</th><th>类型</th><th>首次活动</th><th>最近活动</th><th>交易数</th><th>风险</th></tr>")
	for _, addr := range c.TargetAddresses {
		if s, ok := c.Summaries[addr]; ok {
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td>%.1f(%s)</td></tr>",
				htmlEscape(addr), s.AddressType, s.Profile.FirstActivityTime, s.Profile.LastActivityTime,
				s.Profile.TransactionCount, s.Risk.RiskScore, s.Risk.RiskLevel))
		}
	}
	b.WriteString("</table></div>")

	// 资产
	b.WriteString("<div class=\"sec\"><h2>三、资产概览</h2>")
	for _, addr := range c.TargetAddresses {
		if snap, ok := c.Assets[addr]; ok {
			b.WriteString(fmt.Sprintf("<h3>%s</h3><table><tr><th>Token</th><th>余额</th><th>流入</th><th>流出</th></tr>", htmlEscape(addr)))
			for _, bal := range snap.Balances {
				b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
					bal.Symbol, bal.Balance, bal.InTotal, bal.OutTotal))
			}
			b.WriteString("</table>")
			b.WriteString(fmt.Sprintf("<p>风险：%s（change_rate %.2f，liquidation %v）</p>",
				snap.Risk.Level, snap.Risk.BalanceChangeRate, snap.Risk.LiquidationSignal))
		}
	}
	b.WriteString("</div>")

	// 资金流
	b.WriteString("<div class=\"sec\"><h2>四、资金流分析</h2>")
	b.WriteString(fmt.Sprintf("<p>追踪路径 %d 条</p>", len(c.TracePaths)))
	b.WriteString("<table><tr><th>路径</th><th>跳数</th><th>Token</th><th>金额</th><th>block</th><th>tx_hash</th></tr>")
	for _, p := range c.TracePaths {
		if len(p.Edges) > 0 {
			e := p.Edges[0]
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>",
				htmlEscape(strings.Join(p.Nodes, "→")), p.Hops, e.Token, e.Amount, e.Block, e.TxHash))
		}
	}
	b.WriteString("</table></div>")

	// 路径
	b.WriteString("<div class=\"sec\"><h2>五、资金路径</h2><pre>")
	for _, p := range c.TracePaths {
		b.WriteString(htmlEscape(strings.Join(p.Nodes, "\n↓\n")) + "\n\n")
	}
	b.WriteString("</pre></div>")

	// 图谱
	b.WriteString("<div class=\"sec\"><h2>六、关系图谱</h2>")
	if c.Graph != nil {
		b.WriteString(fmt.Sprintf("<p>节点 %d 个，边 %d 条</p>", len(c.Graph.Nodes), len(c.Graph.Edges)))
	}
	b.WriteString("<table><tr><th>关联地址</th><th>score</th><th>共享对手</th></tr>")
	for _, r := range c.Related {
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%.3f</td><td>%d</td></tr>",
			htmlEscape(r.Address), r.Score, r.SharedCounterparties))
	}
	b.WriteString("</table></div>")

	// 风险
	b.WriteString("<div class=\"sec\"><h2>七、风险分析</h2>")
	for _, addr := range c.TargetAddresses {
		if r, ok := c.Risks[addr]; ok {
			b.WriteString(fmt.Sprintf("<p>%s：风险 %.1f（%s），模式：%s</p>",
				htmlEscape(addr), r.Risk.RiskScore, r.Risk.RiskLevel, r.Pattern))
		}
	}
	b.WriteString("</div>")

	b.WriteString("<div class=\"sec\"><h2>调查结论</h2><p>")
	if c.Status == StatusCompleted {
		b.WriteString("已完成调查分析，证据链完整可追溯。")
	} else {
		b.WriteString(htmlEscape(c.Error))
	}
	b.WriteString("</p></div></body></html>")

	path := filepath.Join(dir, "case-report.html")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// ── 证据链管理 ──

// EvidenceItem 是带溯源的单条证据。
type EvidenceItem struct {
	Kind           string `json:"kind"` // profile/transfer/path/relation/asset
	DatasetVersion string `json:"dataset_version"`
	BlockNumber    string `json:"block_number,omitempty"`
	TxHash         string `json:"tx_hash,omitempty"`
	LogIndex       string `json:"log_index,omitempty"`
	Address        string `json:"address,omitempty"`
	Token          string `json:"token,omitempty"`
	Amount         string `json:"amount,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

// BuildEvidenceChain 构建完整证据链（每条证据可追溯到 dataset/block/tx/log_index）。
func (c *Case) BuildEvidenceChain() []EvidenceItem {
	var items []EvidenceItem
	// 地址证据（画像/风险）
	for _, addr := range c.TargetAddresses {
		if s, ok := c.Summaries[addr]; ok {
			items = append(items, EvidenceItem{
				Kind: "profile", DatasetVersion: c.DatasetVersion, Address: addr,
				Detail: fmt.Sprintf("type=%s tx=%d risk=%.1f", s.AddressType, s.Profile.TransactionCount, s.Risk.RiskScore),
			})
		}
	}
	// 交易证据（路径边）
	seen := map[string]bool{}
	for _, p := range c.TracePaths {
		for _, e := range p.Edges {
			key := e.TxHash + "/" + e.Block
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, EvidenceItem{
				Kind: "transfer", DatasetVersion: c.DatasetVersion,
				BlockNumber: e.Block, TxHash: e.TxHash, LogIndex: e.LogIdx,
				Address: e.From + "→" + e.To, Token: e.Token, Amount: e.Amount,
			})
		}
	}
	// 资产证据
	for _, addr := range c.TargetAddresses {
		if snap, ok := c.Assets[addr]; ok {
			for _, bal := range snap.Balances {
				items = append(items, EvidenceItem{
					Kind: "asset", DatasetVersion: c.DatasetVersion, Address: addr,
					Token: bal.Token, Amount: bal.Balance,
					Detail: fmt.Sprintf("symbol=%s decimals=%d", bal.Symbol, bal.Decimals),
				})
			}
		}
	}
	return items
}

// ExportEvidenceChain 导出证据链（evidence_bundle.json）。
func (c *Case) ExportEvidenceChain(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	items := c.BuildEvidenceChain()
	bundle := map[string]any{
		"case_id":         c.CaseID,
		"dataset_version": c.DatasetVersion,
		"evidence_count":  len(items),
		"evidence":        items,
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "evidence_bundle.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// ClassifyTimeline 生成事件分类时间线（资金进入/转移/快速清空/大额异常）。
func (c *Case) ClassifyTimeline() []TimelineEvent {
	classified := make([]TimelineEvent, 0, len(c.Timeline))
	// 复制并标注事件类别
	for _, ev := range c.Timeline {
		e := ev
		switch {
		case strings.Contains(e.Event, "IN"):
			e.Event = "资金进入"
		case strings.Contains(e.Event, "OUT"):
			e.Event = "资金转移"
		default:
			e.Event = "资金转移"
		}
		classified = append(classified, e)
	}
	// 大额异常（每 token P90 以上进入）
	if len(c.Assets) > 0 {
		for _, addr := range c.TargetAddresses {
			if snap, ok := c.Assets[addr]; ok {
				for _, li := range snap.LargeInflows {
					classified = append(classified, TimelineEvent{
						Time: li.Block, Address: addr, Event: "大额异常",
						Token: li.Token, Amount: li.Amount, TxHash: li.TxHash, Block: li.Block,
					})
				}
				for _, ro := range snap.RapidOutflows {
					classified = append(classified, TimelineEvent{
						Time: ro.Block, Address: addr, Event: "快速清空",
						Token: ro.Token, Amount: ro.Amount, TxHash: ro.TxHash, Block: ro.Block,
					})
				}
			}
		}
	}
	sort.Slice(classified, func(i, j int) bool { return classified[i].Block < classified[j].Block })
	return classified
}
