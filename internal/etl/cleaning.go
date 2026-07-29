package etl

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
)

var commonFailedFeedbackPattern = regexp.MustCompile(`(?i)(查询失败|失败|无记录|无此记录|查无此|no record)`)

func CleanTransactions(txns []model.TransactionRow) []model.TransactionRow {
	cleaned := make([]model.TransactionRow, 0, len(txns))
	for _, txn := range txns {
		cleanCommonAccountNumbers(txn)
		normalizeCommonTransactionFields(txn)
		if shouldSkipTransaction(txn) {
			continue
		}
		cleaned = append(cleaned, txn)
	}
	return cleaned
}

func shouldSkipTransaction(txn model.TransactionRow) bool {
	return transactionRejectReason(txn) != ""
}

func transactionRejectReason(txn model.TransactionRow) string {
	if commonFailedFeedbackPattern.MatchString(txn["查询反馈结果原因"]) {
		return "查询反馈结果表明交易无有效记录"
	}
	for _, req := range RequiredTransactionColumns {
		if strings.TrimSpace(txn[req]) == "" {
			if req == "收付标志" && strings.TrimSpace(txn["主体判定状态"]) != "" {
				return "缺少收付标志：" + txn["主体判定状态"] + "；" + txn["主体判定依据"]
			}
			return "缺少必填字段：" + req
		}
	}
	if !parser.IsValidDirection(strings.TrimSpace(txn["收付标志"])) {
		return "收付标志无法标准化为进或出：" + txn["收付标志"]
	}
	return ""
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
	_, key := buildDedupIdentity(txn)
	return key
}

func buildDedupIdentity(txn model.TransactionRow) (string, string) {
	provider := strings.TrimSpace(txn["来源类型"])
	subject := firstNonEmptyTransactionValue(txn, "交易账号", "交易卡号")
	serial := strings.TrimSpace(txn["交易流水号"])
	fingerprintFields := []string{
		"交易时间", "交易金额", "交易余额", "收付标志",
		"交易卡号", "交易账号", "交易户名", "交易对手账卡号", "对手户名",
		"摘要说明", "交易币种", "交易是否成功", "交易流水号", "商户流水号",
		"付款方账号", "付款方户名", "收款方账号", "收款方户名",
	}
	fingerprintParts := make([]string, 0, len(fingerprintFields))
	for _, field := range fingerprintFields {
		fingerprintParts = append(fingerprintParts, strings.TrimSpace(txn[field]))
	}
	fingerprint := hashDedupValue(strings.Join(fingerprintParts, "\x1f"))
	if serial != "" {
		return "交易流水号+完整业务指纹", hashDedupValue(strings.Join([]string{
			"serial", provider, subject, serial, fingerprint,
		}, "\x1f"))
	}
	if sourceID := strings.TrimSpace(txn["来源记录ID"]); sourceID != "" {
		return "来源记录ID", hashDedupValue("source\x1f" + sourceID)
	}
	return "完整业务指纹", fingerprint
}

func hashDedupValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func firstNonEmptyTransactionValue(txn model.TransactionRow, fields ...string) string {
	for _, field := range fields {
		if value := strings.TrimSpace(txn[field]); value != "" {
			return value
		}
	}
	return ""
}
