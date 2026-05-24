package dbimport

import (
	"os"
	"path/filepath"
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
