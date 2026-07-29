package etl

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/etl/backend/internal/parser"
	"github.com/etl/backend/internal/scanner"
)

var subjectIdentifierHeaders = map[string]bool{
	"用户id": true, "支付宝用户id": true, "支付宝id": true, "微信号": true,
	"账号": true, "账户": true, "登录邮箱": true, "登录手机": true,
	"绑定手机": true, "银行账号": true, "银行卡号": true, "交易卡号": true,
	"交易账号": true, "支付宝账号": true, "支付账号": true,
}

func buildSubjectIdentifiers(scan *scanner.DirectoryScan, explicit []string) map[string]bool {
	result := make(map[string]bool)
	addSubjectIdentifiers(result, explicit...)
	accountPathSet := make(map[string]bool)
	for _, candidate := range append(append([]scanner.SheetCandidate(nil), scan.Accounts...), scan.Transactions...) {
		base := strings.TrimSuffix(filepath.Base(candidate.Path), filepath.Ext(candidate.Path))
		if prefix := strings.SplitN(base, "_", 2)[0]; len([]rune(prefix)) >= 5 {
			addSubjectIdentifiers(result, prefix)
		}
	}
	for _, candidate := range scan.Accounts {
		accountPathSet[candidate.Path] = true
	}
	paths := make([]string, 0, len(accountPathSet))
	for path := range accountPathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		previews, err := parser.ReadTabularPreviews(path, 40)
		if err != nil {
			continue
		}
		headerRows := make(map[string]int)
		headers := make(map[string][]string)
		for sheet, rows := range previews {
			if headerRow := subjectHeaderRow(rows); headerRow >= 0 {
				headerRows[sheet] = headerRow
				headers[sheet] = rows[headerRow]
			}
		}
		_ = parser.StreamTabularFile(path, func(sheet string, rowIndex int, row []string) error {
			headerRow, ok := headerRows[sheet]
			if !ok || rowIndex <= headerRow {
				return nil
			}
			for index, header := range headers[sheet] {
				if index >= len(row) || !subjectIdentifierHeaders[strings.ToLower(parser.NormalizeHeader(header))] {
					continue
				}
				addSubjectIdentifiers(result, row[index])
			}
			return nil
		})
	}
	return result
}

func subjectHeaderRow(rows [][]string) int {
	bestIndex, bestScore := -1, 0
	for index, row := range rows {
		if index >= 40 {
			break
		}
		score := 0
		for _, value := range row {
			if subjectIdentifierHeaders[strings.ToLower(parser.NormalizeHeader(value))] {
				score++
			}
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	if bestScore == 0 {
		return -1
	}
	return bestIndex
}

func addSubjectIdentifiers(target map[string]bool, values ...string) {
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '，' || r == ';' || r == '；' || r == '\n' || r == '\r'
		}) {
			for _, normalized := range parser.NormalizeSubjectIdentifier(part) {
				target[normalized] = true
			}
		}
	}
}
