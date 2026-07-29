package etl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/etl/backend/internal/scanner"
)

func TestBuildSubjectIdentifiersUsesAccountFilesAndExplicitValues(t *testing.T) {
	dir := t.TempDir()
	accountPath := filepath.Join(dir, "826648753_注册信息.csv")
	content := "用户ID,登录邮箱,登录手机,账户名称\n826648753,subject@example.com,13800138000,测试主体\n"
	if err := os.WriteFile(accountPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	scan := &scanner.DirectoryScan{
		Accounts: []scanner.SheetCandidate{{Path: accountPath, Kind: "account", Provider: "支付宝"}},
	}
	identifiers := buildSubjectIdentifiers(scan, []string{"explicit-account"})
	for _, value := range []string{"826648753", "subject@example.com", "13800138000", "explicit-account"} {
		if !identifiers[value] {
			t.Fatalf("expected subject identifier %q in %#v", value, identifiers)
		}
	}
	if identifiers["测试主体"] {
		t.Fatal("account names must not be treated as account identifiers")
	}
}
