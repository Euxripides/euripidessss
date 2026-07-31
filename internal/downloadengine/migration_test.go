package downloadengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationRunnerRunAll(t *testing.T) {
	dir := t.TempDir()
	executed := make([]string, 0)
	runner := NewMigrationRunner(dir, func(sql string) error {
		executed = append(executed, sql)
		return nil
	})

	for _, m := range V2Migrations() {
		if err := runner.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := runner.Run(); err != nil {
		t.Fatal(err)
	}

	// 4 migrations should have executed
	if len(executed) != 4 {
		t.Errorf("expected 4 migrations, got %d", len(executed))
	}
	if runner.CurrentVersion() != 4 {
		t.Errorf("expected version 4, got %d", runner.CurrentVersion())
	}
}

func TestMigrationRunnerIdempotent(t *testing.T) {
	dir := t.TempDir()
	executed := make([]string, 0)
	runner := NewMigrationRunner(dir, func(sql string) error {
		executed = append(executed, sql)
		return nil
	})

	for _, m := range V2Migrations() {
		_ = runner.Register(m)
	}

	// First run
	_ = runner.Run()
	firstCount := len(executed)

	// Second run — should execute 0 new
	_ = runner.Run()
	if len(executed) != firstCount {
		t.Errorf("second run should be idempotent: first=%d, total=%d", firstCount, len(executed))
	}
}

func TestMigrationRunnerStatePersists(t *testing.T) {
	dir := t.TempDir()
	runner := NewMigrationRunner(dir, func(sql string) error { return nil })

	_ = runner.Register(V2Migrations()[0])
	_ = runner.Run()

	// Reload from disk
	runner2 := NewMigrationRunner(dir, func(sql string) error { return nil })
	_ = runner2.Register(V2Migrations()[0])
	_ = runner2.Register(V2Migrations()[1])

	executed := make([]string, 0)
	runner2.execFn = func(sql string) error {
		executed = append(executed, sql)
		return nil
	}
	_ = runner2.Run()

	// Only migration 2 should run (v1 already applied)
	if len(executed) != 1 {
		t.Errorf("expected 1 new migration, got %d: %v", len(executed), executed)
	}
	if runner2.CurrentVersion() != 2 {
		t.Errorf("expected version 2, got %d", runner2.CurrentVersion())
	}
}

func TestMigrationRunnerRejectsNonIncremental(t *testing.T) {
	dir := t.TempDir()
	runner := NewMigrationRunner(dir, func(sql string) error { return nil })

	if err := runner.Register(Migration{Version: 5, Name: "v5"}); err != nil {
		t.Fatal(err)
	}
	// 重复版本应拒绝
	if err := runner.Register(Migration{Version: 5, Name: "v5-duplicate"}); err == nil {
		t.Fatal("should reject non-incremental version")
	}
	// 更小版本应拒绝
	if err := runner.Register(Migration{Version: 3, Name: "v3"}); err == nil {
		t.Fatal("should reject smaller version")
	}
}

func TestMigrationRunnerRollback(t *testing.T) {
	dir := t.TempDir()
	runner := NewMigrationRunner(dir, func(sql string) error { return nil })

	for _, m := range V2Migrations() {
		_ = runner.Register(m)
	}
	_ = runner.Run()
	// v4 → v2
	if err := runner.Rollback(2); err != nil {
		t.Fatal(err)
	}
	if runner.CurrentVersion() != 2 {
		t.Errorf("expected version 2 after rollback, got %d", runner.CurrentVersion())
	}
}

func TestMigrationRunnerSchemaVersionJSON(t *testing.T) {
	dir := t.TempDir()
	runner := NewMigrationRunner(dir, func(sql string) error { return nil })

	_ = runner.Register(V2Migrations()[0])
	_ = runner.Run()

	data, err := os.ReadFile(filepath.Join(dir, "schema_version.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state MigrationState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.CurrentVersion != 1 {
		t.Errorf("expected version 1, got %d", state.CurrentVersion)
	}
	if state.LastMigration != "create_address_first_seen" {
		t.Errorf("unexpected migration name: %s", state.LastMigration)
	}
}
