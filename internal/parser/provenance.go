package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const MappingRuleVersion = "funds-analysis-v2-20260729"

// AuditTransactionColumns are internal-only evidence fields. User-facing
// unified CSV, Excel, preview, and database exports contain only configured business columns.
var AuditTransactionColumns = []string{
	"来源类型", "来源表类型", "来源文件", "来源文件SHA256", "来源Sheet", "原始行号",
	"映射规则版本", "来源记录ID",
	"付款方账号", "付款方户名", "付款方开户行",
	"收款方账号", "收款方户名", "收款方开户行",
	"主体判定依据", "主体判定状态", "对手方接收金额",
}

type MappingOptions struct {
	SourceHashes         map[string]string
	IncludeAlipayBalance bool
}

type SourceAuditContext struct {
	Provider  string
	TableType string
	Path      string
	Sheet     string
	FileHash  string
	HeaderRow int
}

func EnsureSourceHashes(files []string, options *MappingOptions) {
	if options.SourceHashes == nil {
		options.SourceHashes = make(map[string]string, len(files))
	}
	for _, path := range files {
		if options.SourceHashes[path] != "" {
			continue
		}
		hash, err := FileSHA256(path)
		if err == nil {
			options.SourceHashes[path] = hash
		}
	}
}

func ApplySourceAudit(row []string, context SourceAuditContext, dataRowIndex int) {
	lineNumber := context.HeaderRow + dataRowIndex + 2
	fileHash := context.FileHash
	sourceIdentity := fileHash
	if sourceIdentity == "" {
		sourceIdentity = context.Path
	}
	sourceID := sourceRecordID(sourceIdentity, context.Sheet, lineNumber)
	display := strings.Join([]string{
		context.Provider,
		filepath.Base(context.Path),
		emptyAs(context.Sheet, "sheet1"),
		strconv.Itoa(lineNumber),
	}, "|")
	setUnifiedValue(row, "数据来源", display)
	setUnifiedValue(row, "来源类型", context.Provider)
	setUnifiedValue(row, "来源表类型", context.TableType)
	setUnifiedValue(row, "来源文件", context.Path)
	setUnifiedValue(row, "来源文件SHA256", fileHash)
	setUnifiedValue(row, "来源Sheet", emptyAs(context.Sheet, "sheet1"))
	setUnifiedValue(row, "原始行号", strconv.Itoa(lineNumber))
	setUnifiedValue(row, "映射规则版本", MappingRuleVersion)
	setUnifiedValue(row, "来源记录ID", sourceID)
	setUnifiedValue(row, "来源表", display)
	setUnifiedValue(row, "来源", display)
}

func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func SourceRecordID(fileHash, sheet string, lineNumber int) string {
	return sourceRecordID(fileHash, sheet, lineNumber)
}

func sourceRecordID(fileHash, sheet string, lineNumber int) string {
	sum := sha256.Sum256([]byte(fileHash + "\x00" + sheet + "\x00" + strconv.Itoa(lineNumber)))
	return hex.EncodeToString(sum[:])
}

func setUnifiedValue(row []string, column, value string) {
	index := colIdx(column)
	if index >= 0 && index < len(row) {
		row[index] = value
	}
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func AuditDisplay(provider, path, sheet string, lineNumber int) string {
	return fmt.Sprintf("%s|%s|%s|%d", provider, filepath.Base(path), emptyAs(sheet, "sheet1"), lineNumber)
}
