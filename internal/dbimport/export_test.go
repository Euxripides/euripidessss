package dbimport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateExportRequestDefaults(t *testing.T) {
	request := ExportRequest{
		JobID: "job-1", ConnectionID: "connection-1",
		Database: "funds", Table: "clean_transactions",
	}
	if err := validateExportRequest(&request); err != nil {
		t.Fatalf("validateExportRequest() error = %v", err)
	}
	if request.Schema != "public" || request.Mode != "append" ||
		request.ColumnNaming != "snake_case" || request.DuplicateMode != "skip" {
		t.Fatalf("unexpected defaults: %#v", request)
	}
}

func TestCloneExportTaskKeepsPrivateSourcePath(t *testing.T) {
	task := &ExportTask{ID: "task-1", sourcePath: `C:\data\final.csv`}
	cloned := cloneExportTask(task)
	if cloned.sourcePath != task.sourcePath {
		t.Fatal("worker snapshot lost its private source path")
	}
	payload, err := json.Marshal(cloned)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "final.csv") {
		t.Fatal("private source path must not be exposed in JSON")
	}
}

func TestExportColumnNamesAndDDL(t *testing.T) {
	headers := []string{"交易时间", "交易金额", "对手户名", "交易流水号", "商户流水号", "备注"}
	columns := exportColumnNames(headers, "snake_case")
	want := []string{"transaction_time", "transaction_amount", "counterparty_name", "transaction_serial_no", "merchant_serial_no", "remark"}
	for index := range want {
		if columns[index] != want[index] {
			t.Fatalf("column %d = %q, want %q", index, columns[index], want[index])
		}
	}
	postgres := createExportTableSQL(DBTypePostgres, `"public"."clean_transactions"`, headers, columns)
	for _, fragment := range []string{
		`"transaction_time" timestamp`,
		`"transaction_amount" decimal(20,2)`,
		`"source_row_hash" char(64) NOT NULL UNIQUE`,
		`"imported_at" timestamptz`,
	} {
		if !strings.Contains(postgres, fragment) {
			t.Fatalf("PostgreSQL DDL missing %q: %s", fragment, postgres)
		}
	}
	mysql := createExportTableSQL(DBTypeMySQL, "`funds`.`clean_transactions`", headers, columns)
	for _, fragment := range []string{
		"`transaction_time` datetime(6)",
		"`transaction_amount` decimal(20,2)",
		"`id` bigint NOT NULL AUTO_INCREMENT PRIMARY KEY",
	} {
		if !strings.Contains(mysql, fragment) {
			t.Fatalf("MySQL DDL missing %q: %s", fragment, mysql)
		}
	}
}

func TestExportRowHashDuplicateModes(t *testing.T) {
	record := []string{"2026-07-28 12:00:00", "100.00", "张三"}
	first := exportRowHash(record, "skip", "task-a", 1)
	second := exportRowHash(record, "skip", "task-b", 9)
	if first != second {
		t.Fatal("skip mode must produce a stable content hash")
	}
	allowedFirst := exportRowHash(record, "allow", "task-a", 1)
	allowedSecond := exportRowHash(record, "allow", "task-a", 2)
	if allowedFirst == allowedSecond {
		t.Fatal("allow mode must give repeated rows unique hashes")
	}
	if len(first) != 64 || len(allowedFirst) != 64 {
		t.Fatal("row hashes must be SHA-256 hex strings")
	}
}
