package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/etl/backend/internal/parser"
	"github.com/rs/zerolog/log"
)

type SheetCandidate struct {
	Path        string   `json:"path"`
	SheetName   string   `json:"sheet_name,omitempty"`
	Kind        string   `json:"kind"`
	Confidence  int      `json:"confidence"`
	Provider    string   `json:"provider"`
	Columns     []string `json:"columns"`
	Reason      []string `json:"reason"`
	RowsSampled int      `json:"rows_sampled"`
	SizeBytes   int64    `json:"size_bytes"`
}

type DirectoryScan struct {
	SourceDir    string          `json:"source_dir"`
	Transactions []SheetCandidate `json:"transactions"`
	Accounts     []SheetCandidate `json:"accounts"`
	Labels       []SheetCandidate `json:"labels"`
	Unknown      []SheetCandidate `json:"unknown"`
}

// Aliases for scoring
var txAliases = map[string][]string{
	"交易卡号": {"交易卡号", "交易卡", "卡号", "银行卡号", "本方卡号", "账户"},
	"交易账号": {"交易账号", "账号", "银行账号", "本方账号"},
	"交易户名": {"交易户名", "本方户名", "账户名称", "客户名称"},
	"交易时间": {"交易时间", "交易日期", "账务日期", "入账时间", "支付时间", "创建时间", "发生时间"},
	"交易金额": {"交易金额", "金额", "收入金额", "支出金额", "交易额", "付款金额"},
	"交易余额": {"交易余额", "余额", "账户余额"},
	"收付标志": {"收付标志", "借贷标志", "借贷方向", "收支类型"},
	"交易对手账卡号": {"交易对手账卡号", "对方账号", "对手账号", "对方卡号", "对手卡号"},
	"对手户名": {"对手户名", "对方户名", "对方姓名", "交易对方"},
	"摘要说明": {"摘要说明", "摘要", "交易摘要", "商品说明"},
	"交易流水号": {"交易流水号", "流水号", "交易号", "商户订单号", "订单号"},
}

var acctAliases = map[string][]string{
	"账户开户名称": {"账户开户名称", "开户名称", "客户名称", "户名", "姓名"},
	"开户人证件号码": {"开户人证件号码", "证件号码", "身份证号"},
	"交易卡号":   {"交易卡号", "卡号", "银行卡号"},
	"交易账号":   {"交易账号", "账号", "银行账号"},
}

var transactionKeywords = []string{"流水", "交易", "明细", "账单", "statement", "transaction", "bill"}
var accountKeywords = []string{"账户", "账号", "开户", "客户信息", "account", "acct"}
var labelKeywords = []string{"标签", "账户性质", "label"}

func ScanDirectory(sourceDir string) (*DirectoryScan, error) {
	resolved, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	if _, err := os.Stat(resolved); os.IsNotExist(err) {
		return nil, fmt.Errorf("目录不存在: %s", sourceDir)
	}

	result := &DirectoryScan{SourceDir: resolved}

	// Walk files with worker pool
	fileChan := make(chan string, 100)
	candidateChan := make(chan []SheetCandidate, 100)
	var wg sync.WaitGroup

	// Walk files
	go func() {
		filepath.Walk(resolved, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if parser.SupportedSuffixes[ext] {
				fileChan <- path
			}
			return nil
		})
		close(fileChan)
	}()

	// Workers
	numWorkers := 4
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range fileChan {
				candidates := inspectFile(path)
				candidateChan <- candidates
			}
		}()
	}

	go func() {
		wg.Wait()
		close(candidateChan)
	}()

	for candidates := range candidateChan {
		for _, c := range candidates {
			switch c.Kind {
			case "transaction":
				result.Transactions = append(result.Transactions, c)
			case "account":
				result.Accounts = append(result.Accounts, c)
			case "label":
				result.Labels = append(result.Labels, c)
			default:
				result.Unknown = append(result.Unknown, c)
			}
		}
	}

	// Sort for determinism
	sort.Slice(result.Transactions, func(i, j int) bool { return result.Transactions[i].Path < result.Transactions[j].Path })
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].Path < result.Accounts[j].Path })
	sort.Slice(result.Labels, func(i, j int) bool { return result.Labels[i].Path < result.Labels[j].Path })
	sort.Slice(result.Unknown, func(i, j int) bool { return result.Unknown[i].Path < result.Unknown[j].Path })

	return result, nil
}

func inspectFile(path string) []SheetCandidate {
	ext := strings.ToLower(filepath.Ext(path))
	if parser.ExcelSuffixes[ext] {
		return inspectExcel(path)
	}
	return []SheetCandidate{inspectDelimited(path)}
}

func inspectExcel(path string) []SheetCandidate {
	sheets, err := parser.ReadExcelFile(path)
	if err != nil {
		log.Warn().Err(err).Str("path", path).Msg("failed to read excel")
		return nil
	}
	var candidates []SheetCandidate
	for sheetName, rows := range sheets {
		columns := sampleExcelColumns(rows)
		c := classifyCandidate(path, sheetName, columns)
		candidates = append(candidates, c)
	}
	return candidates
}

func inspectDelimited(path string) SheetCandidate {
	rows, err := parser.ReadCSVFile(path)
	if err != nil {
		log.Warn().Err(err).Str("path", path).Msg("failed to read csv")
		return SheetCandidate{
			Path: path, Kind: "unknown", Confidence: 0,
			Provider: "未知", SizeBytes: fileSize(path),
		}
	}
	if len(rows) == 0 {
		return SheetCandidate{
			Path: path, Kind: "unknown", Confidence: 0,
			Provider: "未知", SizeBytes: fileSize(path),
		}
	}
	columns := make([]string, len(rows[0]))
	for i, c := range rows[0] {
		columns[i] = parser.NormalizeHeader(c)
	}
	return classifyCandidate(path, "", columns)
}

func sampleExcelColumns(rows [][]string) []string {
	if len(rows) == 0 {
		return nil
	}
	best := rows[0]
	for _, row := range rows {
		if len(row) > len(best) {
			best = row
		}
	}
	cleaned := make([]string, len(best))
	for i, c := range best {
		cleaned[i] = parser.NormalizeHeader(c)
	}
	return cleaned
}

func classifyCandidate(path, sheetName string, columns []string) SheetCandidate {
	text := fmt.Sprintf("%s %s %s", filepath.Base(path), sheetName, strings.Join(columns, " "))
	provider := detectProvider(text)

	txScore, txHits := scoreColumns(columns, txAliases)
	acctScore, acctHits := scoreColumns(columns, acctAliases)
	labelScore := scoreLabel(columns)

	txScore += keywordScore(text, transactionKeywords)
	acctScore += keywordScore(text, accountKeywords)
	labelScore += keywordScore(text, labelKeywords)

	var reasons []string
	if len(txHits) > 0 {
		hits := txHits
		if len(hits) > 8 {
			hits = hits[:8]
		}
		reasons = append(reasons, "流水字段："+strings.Join(hits, "、"))
	}
	if len(acctHits) > 0 {
		hits := acctHits
		if len(hits) > 8 {
			hits = hits[:8]
		}
		reasons = append(reasons, "账户字段："+strings.Join(hits, "、"))
	}
	if labelScore > 0 {
		reasons = append(reasons, "疑似标签表")
	}

	kind := "unknown"
	confidence := txScore
	if acctScore > confidence {
		kind = "account"
		confidence = acctScore
	}
	if labelScore > confidence {
		kind = "label"
		confidence = labelScore
	}
	if txScore >= acctScore && txScore >= labelScore && txScore >= 3 {
		kind = "transaction"
	}

	return SheetCandidate{
		Path:        path,
		SheetName:   sheetName,
		Kind:        kind,
		Confidence:  confidence,
		Provider:    provider,
		Columns:     columns,
		Reason:      reasons,
		RowsSampled: 0,
		SizeBytes:   fileSize(path),
	}
}

func scoreColumns(columns []string, aliases map[string][]string) (int, []string) {
	normalized := make([]string, len(columns))
	for i, c := range columns {
		normalized[i] = strings.ToLower(parser.NormalizeHeader(c))
	}
	var hits []string
	for canonical, names := range aliases {
		for _, col := range normalized {
			for _, name := range names {
				nc := strings.ToLower(parser.NormalizeHeader(name))
				if col == nc || strings.Contains(col, nc) || strings.Contains(nc, col) {
					hits = append(hits, canonical)
					goto nextCanonical
				}
			}
		}
	nextCanonical:
	}
	return len(hits), hits
}

func scoreLabel(columns []string) int {
	normalized := make(map[string]bool)
	for _, c := range columns {
		nc := parser.NormalizeHeader(c)
		normalized[nc] = true
	}
	score := 0
	if normalized["卡号"] || normalized["交易卡号"] || normalized["账号"] {
		score++
	}
	if normalized["标签"] || normalized["账户性质"] || normalized["性质"] {
		score += 2
	}
	return score
}

func keywordScore(text string, keywords []string) int {
	lower := strings.ToLower(text)
	count := 0
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			count++
		}
	}
	return count
}

func detectProvider(text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(text, "支付宝") || strings.Contains(lower, "alipay") {
		return "支付宝"
	}
	if strings.Contains(text, "微信") || strings.Contains(lower, "wechat") || strings.Contains(text, "财付通") {
		return "微信"
	}
	if strings.Contains(text, "银行") || strings.Contains(lower, "bank") {
		return "银行"
	}
	return "未知"
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// PrepareScan prepares a scan for pipeline processing
func PrepareScan(scan *DirectoryScan, targetDir string) map[string]interface{} {
	_ = os.MkdirAll(targetDir, 0755)

	groups := map[string]interface{}{
		"transactions": make([]string, 0),
		"accounts":     make([]string, 0),
		"manifest":     filepath.Join(targetDir, "manifest.json"),
	}

	// Materialize transaction candidates
	for _, c := range scan.Transactions {
		path := materializeCandidate(c, filepath.Join(targetDir, "transactions"))
		groups["transactions"] = append(groups["transactions"].([]string), path)
	}

	// Materialize account candidates
	for _, c := range scan.Accounts {
		path := materializeCandidate(c, filepath.Join(targetDir, "accounts"))
		groups["accounts"] = append(groups["accounts"].([]string), path)
	}

	// Label (first one)
	if len(scan.Labels) > 0 {
		path := materializeCandidate(scan.Labels[0], filepath.Join(targetDir, "labels"))
		groups["label"] = path
	}

	// Write manifest
	manifestJSON, _ := json.MarshalIndent(scan, "", "  ")
	_ = os.WriteFile(groups["manifest"].(string), manifestJSON, 0644)

	return groups
}

func materializeCandidate(c SheetCandidate, targetDir string) string {
	_ = os.MkdirAll(targetDir, 0755)
	path := c.Path
	ext := strings.ToLower(filepath.Ext(path))

	if !parser.ExcelSuffixes[ext] {
		// Copy file
		target := filepath.Join(targetDir, filepath.Base(path))
		input, _ := os.ReadFile(path)
		_ = os.WriteFile(target, input, 0644)
		return target
	}

	// Excel: convert sheet to CSV
	safeSheet := safeFilename(c.SheetName)
	if safeSheet == "" {
		safeSheet = "Sheet1"
	}
	base := strings.TrimSuffix(filepath.Base(path), ext)
	target := filepath.Join(targetDir, fmt.Sprintf("%s__%s.csv", base, safeSheet))

	rows, err := parser.ReadExcelSheet(path, c.SheetName)
	if err != nil {
		log.Warn().Err(err).Str("path", path).Msg("failed to extract excel sheet")
		return path
	}

	lines := make([]string, len(rows))
	for i, row := range rows {
		cols := make([]string, len(row))
		for j, cell := range row {
			cols[j] = cell
		}
		lines[i] = strings.Join(cols, ",")
	}
	_ = os.WriteFile(target, []byte(strings.Join(lines, "\n")), 0644)
	return target
}

func safeFilename(name string) string {
	replacer := strings.NewReplacer(
		"<", "_", ">", "_", ":", "_", "\"", "_",
		"/", "_", "\\", "_", "|", "_", "?", "_", "*", "_",
	)
	return strings.TrimSpace(replacer.Replace(name))
}
