package dbimport

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStoreEncryptsSavedConnectionsAndHidesPassword(t *testing.T) {
	store := NewStore(t.TempDir())

	saved, err := store.SaveConnection(Connection{
		Name:           "local mysql",
		Type:           DBTypeMySQL,
		Host:           "127.0.0.1",
		Username:       "root",
		Password:       "secret-password",
		SavePassword:   true,
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("SaveConnection failed: %v", err)
	}
	if saved.HasPassword != true {
		t.Fatalf("public connection must hide password and expose HasPassword")
	}

	raw, err := os.ReadFile(filepath.Join(store.baseDir, "db_import_config.enc"))
	if err != nil {
		t.Fatalf("read encrypted config: %v", err)
	}
	if strings.Contains(string(raw), "secret-password") {
		t.Fatalf("encrypted config leaked plaintext password")
	}

	loaded, err := store.GetConnection(saved.ID)
	if err != nil {
		t.Fatalf("GetConnection failed: %v", err)
	}
	if loaded.Password != "secret-password" {
		t.Fatalf("stored password was not recoverable")
	}
}

func TestBuildAutoMappingsRequiresUserForMissingRequiredFields(t *testing.T) {
	mappings := BuildAutoMappings([]ColumnInfo{
		{Name: "付款时间", DataType: "varchar"},
		{Name: "金额", DataType: "decimal"},
	})

	if err := validateMappings(mappings); err == nil {
		t.Fatalf("expected required mapping validation to fail when counterpart is missing")
	}
}

func TestBuildSelectUsesWhitelistedColumnsAndParameters(t *testing.T) {
	columns := []ColumnInfo{
		{Name: "姓名", DataType: "text"},
		{Name: "金额", DataType: "numeric"},
	}
	req := TableDataRequest{
		TableRef: TableRef{Database: "case", Schema: "public", Table: "流水"},
		Page:     1,
		PageSize: 100,
		Search:   "alice",
		SearchColumns: []string{
			"姓名",
			"金额; drop table users;--",
		},
		Filters: []AdvancedFilter{
			{Column: "金额", Operator: ">=", Value: 10},
			{Column: "bad_column", Operator: "=", Value: "x"},
		},
		OrderBy: "姓名",
	}

	query, args := buildSelect(DBTypePostgres, req, columns, true)
	if strings.Contains(query, "drop table") || strings.Contains(query, "bad_column") {
		t.Fatalf("query contains untrusted identifiers: %s", query)
	}
	if !strings.Contains(query, `"姓名"`) || !strings.Contains(query, "$1") || !strings.Contains(query, "$2") {
		t.Fatalf("query did not quote identifiers or parameterize values: %s", query)
	}
	if len(args) != 2 || args[0] != "%alice%" || args[1] != 10 {
		t.Fatalf("unexpected query args: %#v", args)
	}
}

func TestBuildImportSelectUsesMappedColumnsWithoutOffset(t *testing.T) {
	query, err := buildImportSelect(DBTypePostgres, TableRef{Schema: "public", Table: "交易明细"}, []FieldMapping{
		{SourceColumn: "交易户名", TargetField: "交易方户名"},
		{SourceColumn: "交易金额", TargetField: "交易金额"},
		{SourceColumn: "交易户名", TargetField: "对手户名"},
		{TargetField: "备注"},
	}, 5000)
	if err != nil {
		t.Fatalf("buildImportSelect failed: %v", err)
	}
	if strings.Contains(strings.ToLower(query), "offset") || strings.Contains(query, "*") {
		t.Fatalf("import query should stream mapped columns without offset: %s", query)
	}
	if strings.Count(query, `"交易户名"`) != 1 {
		t.Fatalf("duplicate mapped source column should be selected once: %s", query)
	}
	if !strings.Contains(query, `select "交易户名","交易金额" from "public"."交易明细" limit 5000`) {
		t.Fatalf("unexpected import query: %s", query)
	}
}

func TestImportRowMapperMatchesMapMapping(t *testing.T) {
	headers := flowCSVHeaders()
	mappings := []FieldMapping{
		{SourceColumn: "交易户名", TargetField: "交易方户名"},
		{SourceColumn: "交易时间", TargetField: "交易时间", TargetType: "datetime"},
		{SourceColumn: "交易金额", TargetField: "交易金额", TargetType: "decimal"},
		{SourceColumn: "方向", TargetField: "收付标志", TargetType: "direction"},
		{SourceColumn: "对手户名", TargetField: "对手户名"},
	}
	row := map[string]interface{}{
		"交易户名": "张三",
		"交易时间": "2024/01/02 03:04:05",
		"交易金额": "￥1,234.5",
		"方向":   "O",
		"对手户名": "李四",
	}
	expected, snapshot, err := mapImportRow(row, mappings, headers)
	if err != nil {
		t.Fatalf("mapImportRow failed: %v snapshot=%s", err, snapshot)
	}

	mapper, err := newImportRowMapper([]string{"交易户名", "交易时间", "交易金额", "方向", "对手户名"}, mappings, headers, buildHeaderIndex(headers))
	if err != nil {
		t.Fatalf("newImportRowMapper failed: %v", err)
	}
	record := make([]string, len(headers))
	values := []interface{}{"张三", "2024/01/02 03:04:05", "￥1,234.5", "O", "李四"}
	snapshot, err = mapper.mapValuesInto(values, record)
	if err != nil {
		t.Fatalf("mapValuesInto failed: %v snapshot=%s", err, snapshot)
	}
	if !reflect.DeepEqual(record, expected) {
		t.Fatalf("indexed mapper mismatch\nexpected: %#v\nactual:   %#v", expected, record)
	}
}

func TestImportRowMapperRejectsMissingReturnedColumns(t *testing.T) {
	_, err := newImportRowMapper([]string{"交易户名"}, []FieldMapping{
		{SourceColumn: "交易时间", TargetField: "交易时间"},
	}, flowCSVHeaders(), buildHeaderIndex(flowCSVHeaders()))
	if err == nil {
		t.Fatalf("expected missing returned source column to fail")
	}
}

func TestStoreCompactsLargeImportTaskPayloads(t *testing.T) {
	store := NewStore(t.TempDir())
	errors := make([]ImportError, maxPersistedTaskErrors+25)
	for i := range errors {
		errors[i] = ImportError{Reason: "bad row"}
	}
	sample := make([]map[string]any, maxPersistedTaskSample+5)
	for i := range sample {
		sample[i] = map[string]any{"row": i}
	}
	task := ImportTask{
		ID:     "task-large",
		Name:   "large task",
		Status: "completed_with_errors",
		Errors: errors,
		Sample: sample,
	}
	if err := store.SaveTask(task); err != nil {
		t.Fatalf("SaveTask failed: %v", err)
	}
	loaded, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if len(loaded.Errors) != maxPersistedTaskErrors {
		t.Fatalf("expected %d persisted errors, got %d", maxPersistedTaskErrors, len(loaded.Errors))
	}
	if len(loaded.Sample) != maxPersistedTaskSample {
		t.Fatalf("expected %d sample rows, got %d", maxPersistedTaskSample, len(loaded.Sample))
	}
}

func TestDangerousWriteDetection(t *testing.T) {
	if !isDangerousWrite("update accounts set amount = 0") {
		t.Fatalf("unconditional update must be rejected")
	}
	if !isDangerousWrite("delete from accounts") {
		t.Fatalf("unconditional delete must be rejected")
	}
	if isDangerousWrite("update accounts set amount = 0 where id = 1") {
		t.Fatalf("conditional update should be allowed by the guard")
	}
}

func BenchmarkImportRowMapping(b *testing.B) {
	headers := flowCSVHeaders()
	mappings := []FieldMapping{
		{SourceColumn: "交易户名", TargetField: "交易方户名"},
		{SourceColumn: "交易时间", TargetField: "交易时间", TargetType: "datetime"},
		{SourceColumn: "交易金额", TargetField: "交易金额", TargetType: "decimal"},
		{SourceColumn: "方向", TargetField: "收付标志", TargetType: "direction"},
		{SourceColumn: "对手户名", TargetField: "对手户名"},
	}
	row := map[string]interface{}{
		"交易户名": "张三",
		"交易时间": "2024-01-02 03:04:05",
		"交易金额": "1234.5",
		"方向":   "O",
		"对手户名": "李四",
	}
	b.Run("map", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, err := mapImportRow(row, mappings, headers); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("indexed", func(b *testing.B) {
		b.ReportAllocs()
		mapper, err := newImportRowMapper([]string{"交易户名", "交易时间", "交易金额", "方向", "对手户名"}, mappings, headers, buildHeaderIndex(headers))
		if err != nil {
			b.Fatal(err)
		}
		values := []interface{}{"张三", "2024-01-02 03:04:05", "1234.5", "O", "李四"}
		record := make([]string, len(headers))
		for i := 0; i < b.N; i++ {
			if _, err := mapper.mapValuesInto(values, record); err != nil {
				b.Fatal(err)
			}
		}
	})
}
