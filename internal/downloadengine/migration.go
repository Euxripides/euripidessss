package downloadengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ── Migration Framework ──
// 规则: 业务代码禁止零散执行 CREATE TABLE / ALTER TABLE。
// 所有 DDL 通过 Migration Runner 管理，确保幂等和可回滚。

type Migration struct {
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	UpSQL       string `json:"up_sql"`
	DownSQL     string `json:"down_sql,omitempty"` // 回滚脚本
}

type MigrationState struct {
	CurrentVersion int    `json:"current_version"`
	LastMigration  string `json:"last_migration"`
	AppliedAt      string `json:"applied_at"`
}

type MigrationRunner struct {
	mu          sync.Mutex
	migrations  []Migration
	storeDir    string
	statePath   string
	execFn      func(sql string) error // 注入 SQL 执行函数
}

func NewMigrationRunner(storeDir string, execFn func(string) error) *MigrationRunner {
	return &MigrationRunner{
		storeDir: storeDir,
		statePath: filepath.Join(storeDir, "schema_version.json"),
		execFn:   execFn,
	}
}

// Register 注册迁移脚本。版本号必须递增，禁止插入中间版本。
func (r *MigrationRunner) Register(m Migration) error {
	if len(r.migrations) > 0 {
		last := r.migrations[len(r.migrations)-1].Version
		if m.Version <= last {
			return fmt.Errorf("迁移版本必须递增: 当前最新 %d, 新注册 %d", last, m.Version)
		}
	}
	r.migrations = append(r.migrations, m)
	return nil
}

// Run 执行所有未应用的迁移，幂等（已执行的跳过）。
func (r *MigrationRunner) Run() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.execFn == nil {
		return fmt.Errorf("MigrationRunner.execFn 未设置")
	}

	state := r.loadState()
	applied := state.CurrentVersion

	// 排序确保顺序
	sort.Slice(r.migrations, func(i, j int) bool {
		return r.migrations[i].Version < r.migrations[j].Version
	})

	for _, m := range r.migrations {
		if m.Version <= applied {
			continue // 已执行
		}
		if m.UpSQL == "" {
			continue
		}
		if err := r.execFn(m.UpSQL); err != nil {
			return fmt.Errorf("迁移 %d (%s) 执行失败: %w", m.Version, m.Name, err)
		}
		state.CurrentVersion = m.Version
		state.LastMigration = m.Name
		r.saveState(state)
	}
	return nil
}

// Rollback 回滚到指定版本（从高到低执行 down_sql）。
func (r *MigrationRunner) Rollback(targetVersion int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := r.loadState()
	if targetVersion >= state.CurrentVersion {
		return fmt.Errorf("回滚目标版本 %d 必须小于当前版本 %d", targetVersion, state.CurrentVersion)
	}

	sort.Slice(r.migrations, func(i, j int) bool {
		return r.migrations[i].Version > r.migrations[j].Version // 降序
	})

	for _, m := range r.migrations {
		if m.Version <= targetVersion {
			break
		}
		if m.Version > state.CurrentVersion {
			continue
		}
		if m.DownSQL == "" {
			return fmt.Errorf("迁移 %d (%s) 缺少回滚脚本", m.Version, m.Name)
		}
		if err := r.execFn(m.DownSQL); err != nil {
			// 部分失败：保存已回滚到的版本，下次 Run 可从此恢复
			r.saveState(state)
			return fmt.Errorf("回滚迁移 %d (%s) 失败 (已回滚至版本 %d): %w", m.Version, m.Name, state.CurrentVersion, err)
		}
		// 逐步保存，防止部分失败后状态不一致
		state.CurrentVersion = m.Version - 1
		state.LastMigration = fmt.Sprintf("rolled_back_%s", m.Name)
		r.saveState(state)
	}

	state.CurrentVersion = targetVersion
	r.saveState(state)
	return nil
}

// CurrentVersion 返回当前 schema 版本。
func (r *MigrationRunner) CurrentVersion() int {
	state := r.loadState()
	return state.CurrentVersion
}

func (r *MigrationRunner) loadState() MigrationState {
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		return MigrationState{}
	}
	var state MigrationState
	if json.Unmarshal(data, &state) != nil {
		return MigrationState{}
	}
	return state
}

func (r *MigrationRunner) saveState(state MigrationState) {
	_ = os.MkdirAll(r.storeDir, 0755)
	data, _ := json.MarshalIndent(state, "", "  ")
	_ = os.WriteFile(r.statePath, data, 0644)
}

// ── V2 内置迁移脚本 ──

func V2Migrations() []Migration {
	return []Migration{
		{
			Version:     1,
			Name:        "create_address_first_seen",
			Description: "地址首次出现时间缓存表",
			UpSQL: `CREATE TABLE IF NOT EXISTS address_first_seen (
				chain_id VARCHAR NOT NULL,
				address VARCHAR NOT NULL,
				address_type VARCHAR,
				first_seen_block BIGINT,
				first_seen_time VARCHAR,
				first_seen_source VARCHAR,
				coverage_status VARCHAR NOT NULL DEFAULT 'UNKNOWN',
				query_status VARCHAR NOT NULL,
				provider VARCHAR,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (chain_id, address)
			)`,
			DownSQL: "DROP TABLE IF EXISTS address_first_seen",
		},
		{
			Version:     2,
			Name:        "create_download_jobs",
			Description: "V2 下载任务表",
			UpSQL: `CREATE TABLE IF NOT EXISTS download_jobs (
				job_id VARCHAR PRIMARY KEY,
				job_type VARCHAR NOT NULL,
				chain_id VARCHAR NOT NULL,
				status VARCHAR NOT NULL DEFAULT 'CREATED',
				stage VARCHAR NOT NULL DEFAULT 'IDLE',
				priority INT NOT NULL DEFAULT 2,
				range_mode VARCHAR,
				use_first_seen BOOLEAN NOT NULL DEFAULT TRUE,
				effective_start_block BIGINT,
				effective_end_block BIGINT,
				effective_start_time VARCHAR,
				effective_end_time VARCHAR,
				start_time_source VARCHAR,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				finished_at TIMESTAMP
			)`,
			DownSQL: "DROP TABLE IF EXISTS download_jobs",
		},
		{
			Version:     3,
			Name:        "create_download_chunks",
			Description: "V2 Chunk 状态表",
			UpSQL: `CREATE TABLE IF NOT EXISTS download_chunks (
				chunk_id VARCHAR PRIMARY KEY,
				job_id VARCHAR NOT NULL,
				chain_id VARCHAR NOT NULL,
				address_group_id VARCHAR,
				dataset_type VARCHAR NOT NULL,
				start_block BIGINT NOT NULL,
				end_block BIGINT NOT NULL,
				provider VARCHAR,
				attempt INT NOT NULL DEFAULT 0,
				status VARCHAR NOT NULL DEFAULT 'PENDING',
				rows_written BIGINT DEFAULT 0,
				bytes_written BIGINT DEFAULT 0,
				checksum VARCHAR,
				started_at TIMESTAMP,
				completed_at TIMESTAMP,
				error_code VARCHAR,
				error_message VARCHAR
			)`,
			DownSQL: "DROP TABLE IF EXISTS download_chunks",
		},
		{
			Version:     4,
			Name:        "create_download_checkpoints",
			Description: "V2 Checkpoint 表",
			UpSQL: `CREATE TABLE IF NOT EXISTS download_checkpoints (
				job_id VARCHAR PRIMARY KEY,
				completed_chunks TEXT,
				failed_chunks TEXT,
				last_success_block BIGINT,
				manifest_version INT NOT NULL DEFAULT 2,
				updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			DownSQL: "DROP TABLE IF EXISTS download_checkpoints",
		},
	}
}
