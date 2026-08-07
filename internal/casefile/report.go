package casefile

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/investigation"
)

// Evidence 是案件证据包（snapshots/）。
type Evidence struct {
	CaseID           string                         `json:"case_id"`
	DatasetVersion   string                         `json:"dataset_version"`
	Targets          []string                       `json:"target_addresses"`
	AddressEvidence  []AddressEvidence              `json:"address_evidence"`
	TxEvidence       []TxEvidence                   `json:"transaction_evidence"`
	RelationEvidence []investigation.RelatedAddress `json:"relation_evidence"`
	PathEvidence     []PathEvidence                 `json:"path_evidence"`
}

// AddressEvidence 是地址证据。
type AddressEvidence struct {
	Address   string  `json:"address"`
	Type      string  `json:"type"`
	Activity  int64   `json:"activity"`
	RiskScore float64 `json:"risk_score"`
}

// TxEvidence 是交易证据。
type TxEvidence struct {
	TxHash string `json:"tx_hash"`
	Block  string `json:"block"`
	Token  string `json:"token"`
	Amount string `json:"amount"`
}

// PathEvidence 是路径证据。
type PathEvidence struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Hops   int      `json:"hops"`
	Nodes  []string `json:"nodes"`
	Amount string   `json:"amount,omitempty"`
}

// GenerateEvidence 生成 evidence.json / graph.json / timeline.csv。
func (c *Case) GenerateEvidence(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	ev := &Evidence{
		CaseID:         c.CaseID,
		DatasetVersion: c.DatasetVersion,
		Targets:        c.TargetAddresses,
	}
	for _, addr := range c.TargetAddresses {
		if s, ok := c.Summaries[addr]; ok {
			ev.AddressEvidence = append(ev.AddressEvidence, AddressEvidence{
				Address: addr, Type: s.AddressType,
				Activity:  s.Profile.TransactionCount,
				RiskScore: s.Risk.RiskScore,
			})
		}
	}
	ev.RelationEvidence = c.Related
	for _, p := range c.TracePaths {
		pe := PathEvidence{Source: p.Nodes[0], Target: p.Nodes[len(p.Nodes)-1], Hops: p.Hops, Nodes: p.Nodes}
		if len(p.Edges) > 0 {
			pe.Amount = p.Edges[0].Amount
		}
		ev.PathEvidence = append(ev.PathEvidence, pe)
	}
	seenTx := map[string]bool{}
	for _, p := range c.TracePaths {
		for _, e := range p.Edges {
			if !seenTx[e.TxHash] {
				seenTx[e.TxHash] = true
				ev.TxEvidence = append(ev.TxEvidence, TxEvidence{TxHash: e.TxHash, Block: e.Block, Token: e.Token, Amount: e.Amount})
			}
		}
	}
	data, _ := json.MarshalIndent(ev, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "evidence.json"), data, 0644); err != nil {
		return err
	}

	// graph.json
	if c.Graph != nil {
		gData, _ := json.MarshalIndent(c.Graph, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "graph.json"), gData, 0644); err != nil {
			return err
		}
	}

	// timeline.csv
	f, err := os.Create(filepath.Join(dir, "timeline.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"time", "address", "event", "amount", "tx"})
	for _, e := range c.Timeline {
		_ = w.Write([]string{e.Time, e.Address, e.Event, e.Amount, e.TxHash})
	}
	w.Flush()
	return nil
}

// GenerateMarkdown 生成 case-report.md（无横线，段落式结构）。
func (c *Case) GenerateMarkdown() string {
	var b strings.Builder
	b.WriteString("案件分析报告\n\n")
	b.WriteString(fmt.Sprintf("案件编号：%s\n", c.CaseID))
	b.WriteString(fmt.Sprintf("调查员：%s\n", c.Investigator))
	b.WriteString(fmt.Sprintf("数据版本：%s\n", c.DatasetVersion))
	b.WriteString(fmt.Sprintf("状态：%s\n\n", c.Status))
	b.WriteString("目标地址\n\n")
	for _, addr := range c.TargetAddresses {
		b.WriteString(fmt.Sprintf("- %s\n", addr))
	}
	b.WriteString("\n目标地址分析\n\n")
	for _, addr := range c.TargetAddresses {
		if s, ok := c.Summaries[addr]; ok {
			b.WriteString(fmt.Sprintf("地址 %s：类型 %s，交易 %d 笔（流入 %d / 流出 %d），Token %d 个，风险 %.1f（%s）\n",
				addr, s.AddressType, s.Profile.TransactionCount, s.InCount, s.OutCount,
				s.Profile.TokenCount, s.Risk.RiskScore, s.Risk.RiskLevel))
		}
	}
	b.WriteString("\n资金流向\n\n")
	b.WriteString(fmt.Sprintf("追踪路径 %d 条\n", len(c.TracePaths)))
	for _, p := range c.TracePaths {
		b.WriteString(fmt.Sprintf("- %s（%d 跳）\n", strings.Join(p.Nodes, " → "), p.Hops))
		if len(p.Edges) > 0 {
			b.WriteString(fmt.Sprintf("  %s %s，金额 %s，tx %s\n", p.Edges[0].Token, p.Edges[0].Amount, p.Edges[0].Block, p.Edges[0].TxHash))
		}
	}
	b.WriteString("\n公共资金来源\n\n")
	for _, s := range c.CommonSources {
		b.WriteString(fmt.Sprintf("- %s（覆盖 %d 个目标）\n", s.Address, s.Count))
	}
	b.WriteString("\n公共去向\n\n")
	for _, s := range c.CommonSinks {
		b.WriteString(fmt.Sprintf("- %s（覆盖 %d 个目标）\n", s.Address, s.Count))
	}
	b.WriteString("\n关联地址\n\n")
	for _, r := range c.Related {
		b.WriteString(fmt.Sprintf("- %s（score %.3f，共享 %d 对手）\n", r.Address, r.Score, r.SharedCounterparties))
	}
	b.WriteString("\n风险判断\n\n")
	for _, addr := range c.TargetAddresses {
		if r, ok := c.Risks[addr]; ok {
			b.WriteString(fmt.Sprintf("地址 %s：风险 %.1f（%s），模式：%s\n", addr, r.Risk.RiskScore, r.Risk.RiskLevel, r.Pattern))
		}
	}
	b.WriteString("\n结论\n\n")
	if c.Status == StatusCompleted {
		b.WriteString("已完成调查分析，证据完整，详见证据文件（evidence.json / graph.json / timeline.csv）。\n")
	} else {
		b.WriteString(fmt.Sprintf("调查未完成：%s\n", c.Error))
	}
	return b.String()
}

// GenerateJSON 生成 case-report.json。
func (c *Case) GenerateJSON(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "case-report.json"), data, 0644)
}

// GenerateDOCX 调用 python-docx 生成报告（仿宋、小四、无多余标题横线）。
func (c *Case) GenerateDOCX(dir string, pythonPath, scriptPath string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	jsonPath := filepath.Join(dir, "case-report.json")
	if _, err := os.Stat(jsonPath); err != nil {
		if err := c.GenerateJSON(dir); err != nil {
			return "", err
		}
	}
	outPath := filepath.Join(dir, "case-report.docx")
	cmd := exec.Command(pythonPath, scriptPath, jsonPath, outPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docx 生成失败: %v %s", err, strings.TrimSpace(string(output)))
	}
	return outPath, nil
}
