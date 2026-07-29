package etl

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
	"github.com/xuri/excelize/v2"
)

func TestRunPipelineSeparateMergePreservesSourceHeadersAndSheets(t *testing.T) {
	inputDir := filepath.Join(t.TempDir(), "uploads", "current", "transactions")
	outputDir := t.TempDir()
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	alipayCSV := "交易号,交易创建时间,金额（元）,收/支,交易对方信息\n" +
		"A-1,2026-07-01 10:00:00,12.50,支出,支付宝对手\n"
	wechatCSV := "交易单号,交易时间,交易类型,收/支/其他,交易方式,金额(元),交易对方,商户单号\n" +
		"W-1,2026-07-02 11:00:00,转账,收入,零钱,20.00,微信对手,M-1\n"
	if err := os.WriteFile(filepath.Join(inputDir, "支付宝账户明细.csv"), []byte(alipayCSV), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputDir, "微信账单.csv"), []byte(wechatCSV), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := RunPipelineWithOptions(filepath.Dir(inputDir), outputDir, "separate-test", PipelineOptions{
		UnifySources: false,
	})
	if err != nil {
		t.Fatalf("run separate pipeline: %v", err)
	}
	if result.MergeMode != "separate" {
		t.Fatalf("expected separate merge mode, got %q", result.MergeMode)
	}
	if len(result.Transactions) != 2 {
		t.Fatalf("expected 2 preview rows, got %d", len(result.Transactions))
	}
	if result.Transactions[0][sourceTypeColumn] == "" || result.Transactions[1][sourceTypeColumn] == "" {
		t.Fatalf("expected source type in preview rows, got %#v", result.Transactions)
	}
	if len(result.SourceSheets) != 2 {
		t.Fatalf("expected 2 source sheets, got %d", len(result.SourceSheets))
	}
	for _, artifactID := range []string{"source-1", "source-2", "raw-alipay", "raw-wechat"} {
		artifact := findArtifact(result.Artifacts, artifactID)
		if artifact == nil {
			t.Fatalf("expected artifact %s, got %#v", artifactID, result.Artifacts)
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("artifact %s missing: %v", artifactID, err)
		}
	}

	workbook, err := excelize.OpenFile(result.OutputPath)
	if err != nil {
		t.Fatalf("open separate output: %v", err)
	}
	defer workbook.Close()
	sheets := workbook.GetSheetList()
	if !containsString(sheets, "支付宝") || !containsString(sheets, "微信") {
		t.Fatalf("expected 支付宝 and 微信 sheets, got %v", sheets)
	}
	alipayRows, err := workbook.GetRows("支付宝")
	if err != nil {
		t.Fatal(err)
	}
	if len(alipayRows) != 2 {
		t.Fatalf("expected header and one Alipay row, got %d rows", len(alipayRows))
	}
	if !containsString(alipayRows[0], "金额（元）") {
		t.Fatalf("expected original Alipay amount header, got %v", alipayRows[0])
	}
	if containsString(alipayRows[0], "交易金额") {
		t.Fatalf("did not expect unified amount header in separate mode: %v", alipayRows[0])
	}
	if containsString(alipayRows[0], sourceTypeColumn) {
		t.Fatalf("did not expect preview-only source column in exported source sheet: %v", alipayRows[0])
	}
}

func TestRunPipelineDefaultsToUnifiedMerge(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := t.TempDir()
	csvText := "交易号,商户订单号,交易创建时间,付款时间,最近修改时间,交易来源地,类型,用户信息,交易对方信息,消费名称,金额（元）,收/支,交易状态,备注,对应的协查数据\n" +
		"A-1,,2026-07-01 10:00:00,,,广东,转账,用户A,用户B,测试,12.50,支出,成功,,\n"
	if err := os.WriteFile(filepath.Join(inputDir, "支付宝账户明细.csv"), []byte(csvText), 0644); err != nil {
		t.Fatal(err)
	}

	stageDone := make(map[string]bool)
	result, err := RunPipelineWithOptions(inputDir, outputDir, "unified-default", PipelineOptions{
		UnifySources: true,
		Progress: func(event ProgressEvent) {
			if event.Status == "done" {
				stageDone[event.Stage] = true
			}
		},
	})
	if err != nil {
		t.Fatalf("run default pipeline: %v", err)
	}
	if result.MergeMode != "unified" {
		t.Fatalf("expected unified merge mode, got %q", result.MergeMode)
	}
	if len(result.Transactions) != 1 || result.Transactions[0]["交易金额"] != "12.50" {
		t.Fatalf("expected unified transaction, got %#v", result.Transactions)
	}
	row := result.Transactions[0]
	if row["来源类型"] != "支付宝" || row["来源表类型"] != "账户明细" ||
		row["来源Sheet"] != "sheet1" || row["原始行号"] != "2" ||
		len(row["来源文件SHA256"]) != 64 || row["来源记录ID"] == "" ||
		row["映射规则版本"] != parser.MappingRuleVersion {
		t.Fatalf("expected complete transaction provenance, got %#v", row)
	}
	for _, stage := range []string{"scan", "preserve", "source_merge", "normalize", "final_merge", "export"} {
		if !stageDone[stage] {
			t.Fatalf("expected completed progress stage %s, got %#v", stage, stageDone)
		}
	}
	for _, artifactID := range []string{
		"source-1", "raw-alipay", "unified-alipay", "final-csv", "final-xlsx",
		"duplicate-file-audit-csv", "duplicate-audit-csv", "rejected-audit-csv",
	} {
		artifact := findArtifact(result.Artifacts, artifactID)
		if artifact == nil {
			t.Fatalf("expected artifact %s, got %#v", artifactID, result.Artifacts)
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			t.Fatalf("artifact %s missing: %v", artifactID, err)
		}
	}
	for _, artifactID := range []string{"unified-alipay", "final-csv"} {
		artifact := findArtifact(result.Artifacts, artifactID)
		file, err := os.Open(artifact.Path)
		if err != nil {
			t.Fatal(err)
		}
		headers, err := csv.NewReader(file).Read()
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(headers) != len(FinalTransactionColumns) {
			t.Fatalf("%s exported %d columns, want original %d", artifactID, len(headers), len(FinalTransactionColumns))
		}
		for index := range FinalTransactionColumns {
			if headers[index] != FinalTransactionColumns[index] {
				t.Fatalf("%s column %d = %q, want %q", artifactID, index, headers[index], FinalTransactionColumns[index])
			}
		}
	}
	preview, columns := BuildPreview(result.Transactions, 10)
	if len(columns) != len(FinalTransactionColumns) || len(preview) != 1 {
		t.Fatalf("preview must expose the configured unified columns: columns=%d rows=%d", len(columns), len(preview))
	}
}

func findArtifact(artifacts []model.PipelineArtifact, id string) *model.PipelineArtifact {
	for index := range artifacts {
		if artifacts[index].ID == id {
			return &artifacts[index]
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
