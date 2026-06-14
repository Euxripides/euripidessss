package control

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
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
}
