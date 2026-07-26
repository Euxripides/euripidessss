package etl

import (
	"regexp"
	"strings"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
)

var commonFailedFeedbackPattern = regexp.MustCompile(`(?i)(查询失败|失败|无记录|无此记录|查无此|no record)`)

func CleanTransactions(txns []model.TransactionRow) []model.TransactionRow {
	cleaned := make([]model.TransactionRow, 0, len(txns))
	for _, txn := range txns {
		if shouldSkipTransaction(txn) {
			continue
		}
		cleanCommonAccountNumbers(txn)
		normalizeCommonTransactionFields(txn)
		cleaned = append(cleaned, txn)
	}
	return cleaned
}

func shouldSkipTransaction(txn model.TransactionRow) bool {
	if commonFailedFeedbackPattern.MatchString(txn["查询反馈结果原因"]) {
		return true
	}
	for _, req := range RequiredTransactionColumns {
		if strings.TrimSpace(txn[req]) == "" {
			return true
		}
	}
	return false
}

func cleanCommonAccountNumbers(txn model.TransactionRow) {
	for _, col := range []string{"交易卡号", "交易账号", "交易对手账卡号"} {
		if value, ok := txn[col]; ok {
			txn[col] = parser.CleanAccountNumber(value)
		}
	}
}

func normalizeCommonTransactionFields(txn model.TransactionRow) {
	if dir, ok := txn["收付标志"]; ok {
		txn["收付标志"] = parser.NormalizeDirection(dir)
	}
	if dt, ok := txn["交易时间"]; ok {
		txn["交易时间"] = parser.NormalizeDatetime(dt)
	}
	if amt, ok := txn["交易金额"]; ok {
		txn["交易金额"] = parser.FloatToStr(parser.ToNumber(amt))
	}
}

func DeduplicateTransactions(txns []model.TransactionRow) []model.TransactionRow {
	seen := make(map[string]bool)
	result := make([]model.TransactionRow, 0, len(txns))
	for _, txn := range txns {
		key := buildDedupKey(txn)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, txn)
	}
	return result
}

func buildDedupKey(txn model.TransactionRow) string {
	parts := []string{
		txn["交易时间"], txn["交易金额"], txn["收付标志"],
		txn["交易卡号"], txn["交易对手账卡号"],
	}
	return strings.Join(parts, "|")
}
