package control

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

type Status struct {
	OK        bool   `json:"ok"`
	Path      string `json:"path"`
	Error     string `json:"error,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// AddressAsset is a durable address imported by a user. Runtime download and
// warehouse availability are intentionally enriched by the API layer instead
// of being persisted here, so stale task state cannot masquerade as usable data.
type AddressAsset struct {
	ChainKey      string `json:"chain_key"`
	ChainID       int64  `json:"chain_id"`
	Address       string `json:"address"`
	Label         string `json:"label,omitempty"`
	Source        string `json:"source"`
	SourceName    string `json:"source_name,omitempty"`
	ImportCount   int    `json:"import_count"`
	FirstImported string `json:"first_imported_at"`
	LastImported  string `json:"last_imported_at"`
}

func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("data_dir is empty")
	}
	dbPath := filepath.Join(dataDir, "control", "etl_control.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, path: dbPath}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Status() Status {
	if s == nil || s.db == nil {
		return Status{OK: false, Error: "control store not initialized"}
	}
	if err := s.db.Ping(); err != nil {
		return Status{OK: false, Path: s.path, Error: err.Error()}
	}
	return Status{OK: true, Path: s.path, UpdatedAt: time.Now().Format(time.RFC3339)}
}

func (s *Store) UpsertSession(sessionID, name string, totalRows, nodes, edges int, analysisTable, status string) error {
	if s == nil || s.db == nil {
		return errors.New("control store not initialized")
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO flow_sessions (session_id, name, total_rows, nodes, edges, analysis_table, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		 name=excluded.name, total_rows=excluded.total_rows, nodes=excluded.nodes,
		 edges=excluded.edges, analysis_table=excluded.analysis_table,
		 status=excluded.status, updated_at=excluded.updated_at`,
		sessionID, name, totalRows, nodes, edges, analysisTable, status, now, now,
	)
	return err
}

func (s *Store) RecordImportFile(sessionID, filePath, fileName string, fileSize int64) error {
	if s == nil || s.db == nil {
		return errors.New("control store not initialized")
	}
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO flow_import_files (session_id, file_path, file_name, file_size, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		sessionID, filePath, fileName, fileSize, now,
	)
	return err
}

func (s *Store) GetAnalysisTable(sessionID string) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("control store not initialized")
	}
	var tableName string
	err := s.db.QueryRow(
		"SELECT analysis_table FROM flow_sessions WHERE session_id = ?", sessionID,
	).Scan(&tableName)
	if err != nil {
		return "", err
	}
	return tableName, nil
}

func (s *Store) DeleteSession(sessionID string) error {
	if s == nil || s.db == nil {
		return errors.New("control store not initialized")
	}
	_, err := s.db.Exec("DELETE FROM flow_sessions WHERE session_id = ?", sessionID)
	return err
}

// UpsertAddressAssets persists normalized, validated address assets in one
// transaction. Callers own chain/address validation; this layer remains fully
// parameterized and does not interpolate user-controlled values into SQL.
func (s *Store) UpsertAddressAssets(chainKey string, chainID int64, addresses []string, source, sourceName string) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("control store not initialized")
	}
	chainKey = strings.ToLower(strings.TrimSpace(chainKey))
	source = strings.TrimSpace(source)
	if source == "" {
		source = "import"
	}
	sourceName = strings.TrimSpace(sourceName)
	if len(sourceName) > 255 {
		sourceName = sourceName[:255]
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO address_library
		(chain_key, chain_id, address, label, source, source_name, import_count, first_imported_at, last_imported_at)
		VALUES (?, ?, ?, '', ?, ?, 1, ?, ?)
		ON CONFLICT(chain_key, address) DO UPDATE SET
		chain_id=excluded.chain_id,
		source=excluded.source,
		source_name=excluded.source_name,
		import_count=address_library.import_count+1,
		last_imported_at=excluded.last_imported_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	upserted := 0
	for _, address := range addresses {
		if _, err := stmt.Exec(chainKey, chainID, address, source, sourceName, now, now); err != nil {
			return 0, fmt.Errorf("persist address asset: %w", err)
		}
		upserted++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return upserted, nil
}

// EnsureAddressAssets backfills addresses from historical task state without
// increasing import_count on every service restart.
func (s *Store) EnsureAddressAssets(chainKey string, chainID int64, addresses []string, source string) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("control store not initialized")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO address_library
		(chain_key, chain_id, address, label, source, source_name, import_count, first_imported_at, last_imported_at)
		VALUES (?, ?, ?, '', ?, '', 1, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	inserted := 0
	for _, address := range addresses {
		result, err := stmt.Exec(chainKey, chainID, address, source, now, now)
		if err != nil {
			return 0, err
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// ListAddressAssets returns one bounded page and the full filtered total.
func (s *Store) ListAddressAssets(chainKey, query string, limit, offset int) ([]AddressAsset, int, error) {
	if s == nil || s.db == nil {
		return nil, 0, errors.New("control store not initialized")
	}
	where := []string{"1=1"}
	args := make([]any, 0, 6)
	if chainKey = strings.ToLower(strings.TrimSpace(chainKey)); chainKey != "" {
		where = append(where, "chain_key = ?")
		args = append(args, chainKey)
	}
	if query = strings.ToLower(strings.TrimSpace(query)); query != "" {
		where = append(where, "(address LIKE ? OR lower(label) LIKE ? OR lower(source_name) LIKE ?)")
		like := "%" + query + "%"
		args = append(args, like, like, like)
	}
	filter := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRow("SELECT count(*) FROM address_library WHERE "+filter, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.Query(`SELECT chain_key,chain_id,address,label,source,source_name,import_count,first_imported_at,last_imported_at
		FROM address_library WHERE `+filter+` ORDER BY last_imported_at DESC,address ASC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]AddressAsset, 0, limit)
	for rows.Next() {
		var item AddressAsset
		if err := rows.Scan(&item.ChainKey, &item.ChainID, &item.Address, &item.Label, &item.Source, &item.SourceName, &item.ImportCount, &item.FirstImported, &item.LastImported); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (s *Store) init() error {
	if s == nil || s.db == nil {
		return errors.New("control store not initialized")
	}
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	for _, stmt := range schemaStatements {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS control_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS flow_sessions (
		session_id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		total_rows INTEGER NOT NULL DEFAULT 0,
		nodes INTEGER NOT NULL DEFAULT 0,
		edges INTEGER NOT NULL DEFAULT 0,
		analysis_table TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'created',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS flow_import_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		file_path TEXT NOT NULL,
		file_name TEXT NOT NULL DEFAULT '',
		file_size INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		FOREIGN KEY(session_id) REFERENCES flow_sessions(session_id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_flow_import_files_session ON flow_import_files(session_id)`,
	`CREATE TABLE IF NOT EXISTS address_library (
		chain_key TEXT NOT NULL,
		chain_id INTEGER NOT NULL,
		address TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'import',
		source_name TEXT NOT NULL DEFAULT '',
		import_count INTEGER NOT NULL DEFAULT 1,
		first_imported_at TEXT NOT NULL,
		last_imported_at TEXT NOT NULL,
		PRIMARY KEY(chain_key, address)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_address_library_recent ON address_library(chain_key,last_imported_at DESC)`,
}
