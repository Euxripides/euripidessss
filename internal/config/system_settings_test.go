package config

import (
	"os"
	"path/filepath"
	"testing"
)

func testPersistentSettings() PersistentSettings {
	return PersistentSettings{
		ConcurrencyLevel: 8, MaxFileSizeMB: 500, AnalyticsDataSource: "auto",
		ClickHouseEnabled: true, PriceEngineEnabled: true, LogRetentionDays: 30,
		OutputRetentionDays: 30, BackupRetentionCount: 10,
	}
}

func TestPersistentSettingsValidation(t *testing.T) {
	valid := testPersistentSettings()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	invalid := valid
	invalid.ConcurrencyLevel = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected concurrency validation error")
	}
	invalid = valid
	invalid.ClickHouseEnabled = false
	invalid.ClickHouseRequired = true
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected clickhouse dependency validation error")
	}
}

func TestPersistentSettingsPatchAndAtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	settings := testPersistentSettings()
	if err := SavePersistentSettings(dir, settings); err != nil {
		t.Fatalf("first save: %v", err)
	}
	workers := 16
	updated, changed, err := settings.ApplyPatch(PersistentSettingsPatch{ConcurrencyLevel: &workers})
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	if len(changed) != 1 || changed[0] != "concurrency_level" {
		t.Fatalf("unexpected changed keys: %v", changed)
	}
	if err := SavePersistentSettings(dir, updated); err != nil {
		t.Fatalf("overwrite existing settings: %v", err)
	}
	loaded, ok, err := LoadPersistentSettings(dir)
	if err != nil || !ok || loaded.ConcurrencyLevel != 16 {
		t.Fatalf("load after overwrite: ok=%v settings=%+v err=%v", ok, loaded, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, SystemSettingsFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "token", "secret", "url"} {
		if containsFold(string(content), forbidden) {
			t.Fatalf("persistent settings unexpectedly contain %q", forbidden)
		}
	}
}

func containsFold(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		match := true
		for j := range needle {
			a, b := value[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
