package dbimport

import (
	"bufio"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/etl/backend/internal/model"
	"github.com/etl/backend/internal/parser"
)

const (
	importProgressSaveRows     int64 = 200000
	importCancelCheckRows      int64 = 10000
	importProgressSaveInterval       = 5 * time.Second
	maxStoredImportErrors            = 200
	importCSVBufferSize              = 16 * 1024 * 1024
	copySplitFieldsLimit             = 100
)

var FlowTargetFields = []FieldMapping{
	{TargetField: "交易方户名", TargetType: "string", Required: true},
	{TargetField: "交易方账户", TargetType: "string"},
	{TargetField: "交易方身份证号", TargetType: "string"},
	{TargetField: "交易方标签", TargetType: "string"},
	{TargetField: "交易时间", TargetType: "datetime", Required: true},
	{TargetField: "交易金额", TargetType: "decimal", Required: true},
	{TargetField: "收付标志", TargetType: "direction"},
	{TargetField: "交易余额", TargetType: "decimal"},
	{TargetField: "交易对手账卡号", TargetType: "string"},
	{TargetField: "对手户名", TargetType: "string", Required: true},
	{TargetField: "对手身份证号", TargetType: "string"},
	{TargetField: "对手标签", TargetType: "string"},
	{TargetField: "摘要说明", TargetType: "string"},
	{TargetField: "备注", TargetType: "string"},
}

type Service struct {
	store     *Store
	uploadDir string
}

func NewService(store *Store, uploadDir string) *Service {
	return &Service{store: store, uploadDir: uploadDir}
}

func (s *Service) Open(ctx context.Context, conn Connection, database string) (*sql.DB, error) {
	normalizeConnection(&conn)
	if database == "" {
		database = conn.DefaultDatabase
	}
	driver, dsn := conn.driverAndDSN(database)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, sanitizeDBError(err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, time.Duration(conn.TimeoutSeconds)*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, sanitizeDBError(err)
	}
	return db, nil
}

func (s *Service) TestConnection(ctx context.Context, id string, input *Connection) error {
	var conn Connection
	var err error
	if input != nil {
		conn = *input
	} else {
		conn, err = s.store.GetConnection(id)
		if err != nil {
			return err
		}
	}
	db, err := s.Open(ctx, conn, conn.DefaultDatabase)
	if err != nil {
		return err
	}
	defer db.Close()
	return nil
}

func (s *Service) Databases(ctx context.Context, id string) ([]string, error) {
	conn, err := s.store.GetConnection(id)
	if err != nil {
		return nil, err
	}
	db, err := s.Open(ctx, conn, conn.DefaultDatabase)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var query string
	if conn.Type == DBTypeMySQL {
		query = "select schema_name from information_schema.schemata where schema_name not in ('information_schema','mysql','performance_schema','sys') order by schema_name"
	} else {
		query = "select datname from pg_database where datallowconn = true and datistemplate = false order by datname"
	}
	return scanStrings(ctx, db, query)
}

func (s *Service) Schemas(ctx context.Context, id, database string, showSystem bool) ([]string, error) {
	conn, err := s.store.GetConnection(id)
	if err != nil {
		return nil, err
	}
	if conn.Type == DBTypeMySQL {
		if database == "" {
			database = conn.DefaultDatabase
		}
		if database == "" {
			items, err := s.Databases(ctx, id)
			if err != nil || len(items) == 0 {
				return nil, err
			}
			database = items[0]
		}
		return []string{database}, nil
	}
	db, err := s.Open(ctx, conn, database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query := "select schema_name from information_schema.schemata"
	if !showSystem {
		query += " where schema_name not in ('pg_catalog','information_schema') and schema_name not like 'pg_toast%'"
	}
	query += " order by schema_name"
	return scanStrings(ctx, db, query)
}

func (s *Service) Tables(ctx context.Context, ref TableRef, showSystem bool) ([]map[string]interface{}, error) {
	conn, err := s.store.GetConnection(ref.ConnectionID)
	if err != nil {
		return nil, err
	}
	db, err := s.Open(ctx, conn, ref.Database)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var rows *sql.Rows
	if conn.Type == DBTypeMySQL {
		query := "select table_name, table_type from information_schema.tables where table_schema = ?"
		args := []interface{}{ref.Database}
		if !showSystem {
			query += " and table_schema not in ('information_schema','mysql','performance_schema','sys')"
		}
		query += " order by table_name"
		rows, err = db.QueryContext(ctx, query, args...)
	} else {
		schema := ref.Schema
		if schema == "" {
			schema = "public"
		}
		query := "select table_name, table_type from information_schema.tables where table_schema = $1"
		args := []interface{}{schema}
		if !showSystem {
			query += " and table_schema not in ('pg_catalog','information_schema')"
		}
		query += " order by table_name"
		rows, err = db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		return nil, sanitizeDBError(err)
	}
	defer rows.Close()
	items := []map[string]interface{}{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err == nil {
			items = append(items, map[string]interface{}{"name": name, "type": kind})
		}
	}
	return items, rows.Err()
}

func (s *Service) Columns(ctx context.Context, ref TableRef) ([]ColumnInfo, error) {
	conn, err := s.store.GetConnection(ref.ConnectionID)
	if err != nil {
		return nil, err
	}
	db, err := s.Open(ctx, conn, ref.Database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return loadColumns(ctx, db, conn.Type, ref)
}

func (s *Service) Preview(ctx context.Context, req TableDataRequest) (TableDataResponse, error) {
	return s.tableRows(ctx, req, false)
}

func (s *Service) Search(ctx context.Context, req TableDataRequest) (TableDataResponse, error) {
	return s.tableRows(ctx, req, true)
}

func (s *Service) Query(ctx context.Context, req QueryRequest) (TableDataResponse, error) {
	start := time.Now()
	req.Page, req.PageSize = normalizePage(req.Page, req.PageSize)
	if !req.AllowWrite && !isSelectSQL(req.SQL) {
		return TableDataResponse{}, fmt.Errorf("默认只允许 SELECT 查询")
	}
	if strings.TrimSpace(req.SQL) == "" {
		return TableDataResponse{}, fmt.Errorf("SQL 不能为空")
	}
	conn, err := s.store.GetConnection(req.ConnectionID)
	if err != nil {
		return TableDataResponse{}, err
	}
	db, err := s.Open(ctx, conn, req.Database)
	if err != nil {
		return TableDataResponse{}, err
	}
	defer db.Close()
	if !isSelectSQL(req.SQL) {
		if isDangerousWrite(req.SQL) {
			return TableDataResponse{}, fmt.Errorf("禁止无条件 UPDATE / DELETE")
		}
		result, err := db.ExecContext(ctx, req.SQL)
		if err != nil {
			return TableDataResponse{}, sanitizeDBError(err)
		}
		affected, _ := result.RowsAffected()
		return TableDataResponse{Rows: []map[string]interface{}{{"affected_rows": affected}}, Page: 1, PageSize: 1, ReturnedRows: 1, ElapsedMs: time.Since(start).Milliseconds()}, nil
	}
	limit := req.PageSize
	offset := (req.Page - 1) * req.PageSize
	query := fmt.Sprintf("select * from (%s) as q limit %d offset %d", strings.TrimRight(strings.TrimSpace(req.SQL), ";"), limit, offset)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return TableDataResponse{}, sanitizeDBError(err)
	}
	defer rows.Close()
	cols, resultRows, err := scanRows(rows)
	if err != nil {
		return TableDataResponse{}, err
	}
	return TableDataResponse{Columns: cols, Rows: resultRows, Page: req.Page, PageSize: req.PageSize, ReturnedRows: len(resultRows), Truncated: len(resultRows) == req.PageSize, ElapsedMs: time.Since(start).Milliseconds()}, nil
}

func (s *Service) InsertRow(ctx context.Context, req TableEditRequest) (TableEditResponse, error) {
	if len(req.Values) == 0 {
		return TableEditResponse{}, fmt.Errorf("新增数据不能为空")
	}
	conn, db, columns, err := s.openForEdit(ctx, req.TableRef)
	if err != nil {
		return TableEditResponse{}, err
	}
	defer db.Close()
	names := []string{}
	args := []interface{}{}
	for name, value := range req.Values {
		if !hasColumn(columns, name) {
			return TableEditResponse{}, fmt.Errorf("字段不存在：%s", name)
		}
		names = append(names, name)
		args = append(args, value)
	}
	holders := make([]string, len(names))
	for i := range names {
		holders[i] = placeholder(conn.Type, i+1)
		names[i] = quoteIdent(conn.Type, names[i])
	}
	query := fmt.Sprintf("insert into %s (%s) values (%s)", qualifiedTable(conn.Type, req.TableRef), strings.Join(names, ","), strings.Join(holders, ","))
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return TableEditResponse{}, sanitizeDBError(err)
	}
	affected, _ := result.RowsAffected()
	return TableEditResponse{AffectedRows: affected}, nil
}

func (s *Service) UpdateRow(ctx context.Context, req TableEditRequest) (TableEditResponse, error) {
	if len(req.Values) == 0 {
		return TableEditResponse{}, fmt.Errorf("修改数据不能为空")
	}
	if len(req.Keys) == 0 {
		return TableEditResponse{}, fmt.Errorf("修改必须提供主键或唯一条件")
	}
	conn, db, columns, err := s.openForEdit(ctx, req.TableRef)
	if err != nil {
		return TableEditResponse{}, err
	}
	defer db.Close()
	args := []interface{}{}
	sets := []string{}
	for name, value := range req.Values {
		if !hasColumn(columns, name) {
			return TableEditResponse{}, fmt.Errorf("字段不存在：%s", name)
		}
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = %s", quoteIdent(conn.Type, name), placeholder(conn.Type, len(args))))
	}
	where, keyArgs, err := buildKeyWhere(conn.Type, columns, req.Keys, len(args)+1)
	if err != nil {
		return TableEditResponse{}, err
	}
	args = append(args, keyArgs...)
	query := fmt.Sprintf("update %s set %s where %s", qualifiedTable(conn.Type, req.TableRef), strings.Join(sets, ","), where)
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return TableEditResponse{}, sanitizeDBError(err)
	}
	affected, _ := result.RowsAffected()
	return TableEditResponse{AffectedRows: affected}, nil
}

func (s *Service) DeleteRow(ctx context.Context, req TableEditRequest) (TableEditResponse, error) {
	if len(req.Keys) == 0 {
		return TableEditResponse{}, fmt.Errorf("删除必须提供主键或唯一条件")
	}
	conn, db, columns, err := s.openForEdit(ctx, req.TableRef)
	if err != nil {
		return TableEditResponse{}, err
	}
	defer db.Close()
	where, args, err := buildKeyWhere(conn.Type, columns, req.Keys, 1)
	if err != nil {
		return TableEditResponse{}, err
	}
	query := fmt.Sprintf("delete from %s where %s", qualifiedTable(conn.Type, req.TableRef), where)
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return TableEditResponse{}, sanitizeDBError(err)
	}
	affected, _ := result.RowsAffected()
	return TableEditResponse{AffectedRows: affected}, nil
}

func (s *Service) AutoMapping(ctx context.Context, ref TableRef) (MappingRule, bool, error) {
	cols, err := s.Columns(ctx, ref)
	if err != nil {
		return MappingRule{}, false, err
	}
	hash := ColumnsHash(cols)
	if rule, ok, err := s.store.FindMapping(ref, hash); err != nil || ok {
		return rule, ok, err
	}
	conn, err := s.store.GetConnection(ref.ConnectionID)
	if err != nil {
		return MappingRule{}, false, err
	}
	rule := MappingRule{
		ID:                uuid.NewString(),
		ConnectionType:    conn.Type,
		ConnectionID:      ref.ConnectionID,
		Database:          ref.Database,
		Schema:            ref.Schema,
		Table:             ref.Table,
		SourceColumnsHash: hash,
		TargetVersion:     "flow-v1",
		Mappings:          BuildAutoMappings(cols),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	return rule, false, nil
}

func (s *Service) CreateTask(req ImportTaskRequest) (ImportTask, error) {
	if len(req.Tables) == 0 {
		return ImportTask{}, fmt.Errorf("请至少选择一张表")
	}
	now := time.Now()
	task := ImportTask{
		ID:        uuid.NewString(),
		Name:      strings.TrimSpace(req.Name),
		Status:    "pending",
		Tables:    req.Tables,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if task.Name == "" {
		task.Name = "数据库导入任务"
	}
	if err := s.store.SaveTask(task); err != nil {
		return ImportTask{}, err
	}
	return task, nil
}

func (s *Service) StartTask(ctx context.Context, id string) (ImportTask, error) {
	task, err := s.store.GetTask(id)
	if err != nil {
		return ImportTask{}, err
	}
	task.Status = "running"
	task.UpdatedAt = time.Now()
	start := time.Now()
	sessionID := "db-" + uuid.NewString()[:12]
	task.SessionID = sessionID
	_ = s.store.SaveTask(task)
	sessionDir := filepath.Join(s.uploadDir, "flow_sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		task.Status = "failed"
		task.Errors = append(task.Errors, ImportError{Table: "", Reason: fmt.Sprintf("创建会话目录失败: %v", err), CreatedAt: time.Now()})
		task.UpdatedAt = time.Now()
		_ = s.store.SaveTask(task)
		return task, err
	}
	outputPath := filepath.Join(sessionDir, "database_import.csv")
	file, err := os.Create(outputPath)
	if err != nil {
		task.Status = "failed"
		task.Errors = append(task.Errors, ImportError{Table: "", Reason: fmt.Sprintf("创建CSV文件失败: %v", err), CreatedAt: time.Now()})
		task.UpdatedAt = time.Now()
		_ = s.store.SaveTask(task)
		return task, err
	}
	defer file.Close()
	bufferedWriter := bufio.NewWriterSize(file, importCSVBufferSize)
	writer := csv.NewWriter(bufferedWriter)
	defer writer.Flush()
	headers := flowCSVHeaders()
	if err := writer.Write(headers); err != nil {
		task.Status = "failed"
		task.Errors = append(task.Errors, ImportError{Table: "", Reason: fmt.Sprintf("写入CSV表头失败: %v", err), CreatedAt: time.Now()})
		task.UpdatedAt = time.Now()
		_ = s.store.SaveTask(task)
		return task, err
	}
	task.Progress.TotalRows = 0
	sample := []map[string]interface{}{}
	var rowNumber int64
	headerIndex := buildHeaderIndex(headers)
	lastSaveRows := task.Progress.ProcessedRows
	lastSaveTime := time.Now()
	saveProgress := func(force bool) {
		if !force &&
			task.Progress.ProcessedRows-lastSaveRows < importProgressSaveRows &&
			time.Since(lastSaveTime) < importProgressSaveInterval {
			return
		}
		elapsed := math.Max(time.Since(start).Seconds(), 0.001)
		task.Progress.SpeedRowsPerSecond = float64(task.Progress.ProcessedRows) / elapsed
		task.UpdatedAt = time.Now()
		_ = s.store.SaveTask(task)
		if force {
			writer.Flush()
			_ = bufferedWriter.Flush()
		}
		lastSaveRows = task.Progress.ProcessedRows
		lastSaveTime = time.Now()
	}
	for _, item := range task.Tables {
		if err := validateMappings(item.Mappings); err != nil {
			task.Status = "failed"
			appendImportError(&task, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
			_ = s.store.SaveTask(task)
			return task, err
		}
		limit := item.Limit
		if limit <= 0 || limit > MaxImportRows {
			limit = MaxImportRows
		}
		conn, err := s.store.GetConnection(item.ConnectionID)
		if err != nil {
			task.Progress.FailedRows++
			appendImportError(&task, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
			saveProgress(true)
			continue
		}
		db, err := s.Open(ctx, conn, item.Database)
		if err != nil {
			task.Progress.FailedRows++
			appendImportError(&task, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
			saveProgress(true)
			continue
		}
		estimatedRows, err := estimateImportRows(ctx, db, conn.Type, item.TableRef)
		if err == nil && estimatedRows > 0 {
			if estimatedRows > int64(limit) {
				estimatedRows = int64(limit)
			}
			task.Progress.TotalRows += estimatedRows
		} else {
			task.Progress.TotalRows += int64(limit)
		}
		saveProgress(true)

		query, err := buildImportSelect(conn.Type, item.TableRef, item.Mappings, limit)
		if err != nil {
			db.Close()
			task.Progress.FailedRows++
			appendImportError(&task, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
			saveProgress(true)
			continue
		}
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			db.Close()
			task.Progress.FailedRows++
			appendImportError(&task, ImportError{Table: item.Table, Reason: sanitizeDBError(err).Error(), CreatedAt: time.Now()})
			saveProgress(true)
			continue
		}
		sourceCols := extractSelectColumns(query)
		mapper, err := newImportRowMapper(sourceCols, item.Mappings, headers, headerIndex)
		if err != nil {
			rows.Close()
			db.Close()
			task.Progress.FailedRows++
			appendImportError(&task, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
			saveProgress(true)
			continue
		}
		values, scanTargets := newScanBuffers(len(sourceCols))
		record := make([]string, len(headers))
		var tableProcessed int64
		var lastCancelCheckRows int64
		for rows.Next() {
			if ctx.Err() != nil {
				rows.Close()
				db.Close()
				task.Status = "canceled"
				task.UpdatedAt = time.Now()
				_ = s.store.SaveTask(task)
				return task, ctx.Err()
			}
			err := scanCurrentValues(rows, values, scanTargets)
			rowNumber++
			tableProcessed++
			task.Progress.ProcessedRows++
			if err != nil {
				task.Progress.FailedRows++
				appendImportError(&task, ImportError{Table: item.Table, Row: rowNumber, Reason: err.Error(), CreatedAt: time.Now()})
				saveProgress(false)
				continue
			}
			snapshot, err := mapper.mapValuesInto(values, record)
			if err != nil {
				task.Progress.FailedRows++
				appendImportError(&task, ImportError{Table: item.Table, Row: rowNumber, Reason: err.Error(), Snapshot: snapshot, CreatedAt: time.Now()})
				saveProgress(false)
				continue
			}
			if err := writer.Write(record); err != nil {
				task.Progress.FailedRows++
				appendImportError(&task, ImportError{Table: item.Table, Row: rowNumber, Reason: err.Error(), CreatedAt: time.Now()})
				saveProgress(false)
				continue
			}
			task.Progress.SuccessRows++
			if len(sample) < 20 {
				sample = append(sample, recordToMap(headers, record))
			}
			if task.Progress.ProcessedRows-lastCancelCheckRows >= importCancelCheckRows {
				if latest, err := s.store.GetTask(id); err == nil && latest.Status == "canceled" {
					rows.Close()
					db.Close()
					task.Status = "canceled"
					task.UpdatedAt = time.Now()
					_ = s.store.SaveTask(task)
					return task, nil
				}
				lastCancelCheckRows = task.Progress.ProcessedRows
			}
			saveProgress(false)
			if tableProcessed >= int64(limit) {
				break
			}
		}
		if err := rows.Err(); err != nil {
			task.Progress.FailedRows++
			appendImportError(&task, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
		}
		rows.Close()
		db.Close()
		saveProgress(true)
	}
	task.Columns = headers
	task.Files = []string{"database_import.csv"}
	task.Sample = sample
	if task.Progress.TotalRows != task.Progress.ProcessedRows {
		task.Progress.TotalRows = task.Progress.ProcessedRows
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		task.Status = "failed"
		appendImportError(&task, ImportError{Table: "", Reason: fmt.Sprintf("写入CSV失败: %v", err), CreatedAt: time.Now()})
		task.UpdatedAt = time.Now()
		_ = s.store.SaveTask(task)
		return task, err
	}
	task.Status = "completed"
	if task.Progress.FailedRows > 0 {
		task.Status = "completed_with_errors"
	}
	task.UpdatedAt = time.Now()
	if err := bufferedWriter.Flush(); err != nil {
		task.Status = "failed"
		appendImportError(&task, ImportError{Table: "", Reason: fmt.Sprintf("写入CSV失败: %v", err), CreatedAt: time.Now()})
		task.UpdatedAt = time.Now()
		_ = s.store.SaveTask(task)
		return task, err
	}
	if err := s.store.SaveTask(task); err != nil {
		return task, err
	}
	return task, nil
}

func (s *Service) tableRows(ctx context.Context, req TableDataRequest, includeSearch bool) (TableDataResponse, error) {
	start := time.Now()
	req.Page, req.PageSize = normalizePage(req.Page, req.PageSize)
	conn, err := s.store.GetConnection(req.ConnectionID)
	if err != nil {
		return TableDataResponse{}, err
	}
	db, err := s.Open(ctx, conn, req.Database)
	if err != nil {
		return TableDataResponse{}, err
	}
	defer db.Close()
	columns, err := loadColumns(ctx, db, conn.Type, req.TableRef)
	if err != nil {
		return TableDataResponse{}, err
	}
	query, args := buildSelect(conn.Type, req, columns, includeSearch)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return TableDataResponse{}, sanitizeDBError(err)
	}
	defer rows.Close()
	_, resultRows, err := scanRows(rows)
	if err != nil {
		return TableDataResponse{}, err
	}
	return TableDataResponse{Columns: columns, Rows: resultRows, Page: req.Page, PageSize: req.PageSize, ReturnedRows: len(resultRows), Truncated: len(resultRows) == req.PageSize, ElapsedMs: time.Since(start).Milliseconds()}, nil
}

func (s *Service) openForEdit(ctx context.Context, ref TableRef) (Connection, *sql.DB, []ColumnInfo, error) {
	conn, err := s.store.GetConnection(ref.ConnectionID)
	if err != nil {
		return Connection{}, nil, nil, err
	}
	db, err := s.Open(ctx, conn, ref.Database)
	if err != nil {
		return Connection{}, nil, nil, err
	}
	columns, err := loadColumns(ctx, db, conn.Type, ref)
	if err != nil {
		db.Close()
		return Connection{}, nil, nil, err
	}
	return conn, db, columns, nil
}

func buildKeyWhere(dbType DBType, columns []ColumnInfo, keys map[string]interface{}, start int) (string, []interface{}, error) {
	if len(keys) == 0 {
		return "", nil, fmt.Errorf("必须提供主键或唯一条件")
	}
	parts := []string{}
	args := []interface{}{}
	for name, value := range keys {
		if !hasColumn(columns, name) {
			return "", nil, fmt.Errorf("字段不存在：%s", name)
		}
		args = append(args, value)
		parts = append(parts, fmt.Sprintf("%s = %s", quoteIdent(dbType, name), placeholder(dbType, start+len(args)-1)))
	}
	return strings.Join(parts, " and "), args, nil
}

func (conn Connection) driverAndDSN(database string) (string, string) {
	if conn.Type == DBTypeMySQL {
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&timeout=%ds&readTimeout=%ds&writeTimeout=%ds",
			conn.Username, conn.Password, conn.Host, conn.Port, database, conn.TimeoutSeconds, conn.TimeoutSeconds, conn.TimeoutSeconds)
		return "mysql", dsn
	}
	sslMode := "disable"
	if conn.SSL {
		sslMode = "require"
	}
	if database == "" {
		database = "postgres"
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d fallback_application_name=etl_import",
		conn.Host, conn.Port, conn.Username, conn.Password, database, sslMode, conn.TimeoutSeconds)
	return "postgres", dsn
}

func loadColumns(ctx context.Context, db *sql.DB, dbType DBType, ref TableRef) ([]ColumnInfo, error) {
	if dbType == DBTypeMySQL {
		query := `select c.column_name, c.data_type, c.is_nullable, coalesce(c.column_default,''), coalesce(c.character_maximum_length,''), coalesce(c.numeric_precision,''), coalesce(c.column_comment,''), case when k.column_name is null then 0 else 1 end
from information_schema.columns c
left join information_schema.key_column_usage k on k.table_schema=c.table_schema and k.table_name=c.table_name and k.column_name=c.column_name and k.constraint_name='PRIMARY'
where c.table_schema=? and c.table_name=? order by c.ordinal_position`
		rows, err := db.QueryContext(ctx, query, ref.Database, ref.Table)
		if err != nil {
			return nil, sanitizeDBError(err)
		}
		defer rows.Close()
		return scanColumnInfo(rows)
	}
	schema := ref.Schema
	if schema == "" {
		schema = "public"
	}
	query := `select c.column_name, c.data_type, c.is_nullable, coalesce(c.column_default,''), coalesce(c.character_maximum_length::text,''), coalesce(c.numeric_precision::text,''), '', case when tc.constraint_type='PRIMARY KEY' then 1 else 0 end
from information_schema.columns c
left join information_schema.key_column_usage k on k.table_schema=c.table_schema and k.table_name=c.table_name and k.column_name=c.column_name
left join information_schema.table_constraints tc on tc.constraint_name=k.constraint_name and tc.table_schema=k.table_schema and tc.table_name=k.table_name
where c.table_schema=$1 and c.table_name=$2 order by c.ordinal_position`
	rows, err := db.QueryContext(ctx, query, schema, ref.Table)
	if err != nil {
		return nil, sanitizeDBError(err)
	}
	defer rows.Close()
	return scanColumnInfo(rows)
}

func scanColumnInfo(rows *sql.Rows) ([]ColumnInfo, error) {
	items := []ColumnInfo{}
	for rows.Next() {
		var item ColumnInfo
		var nullable, pk int
		if err := rows.Scan(&item.Name, &item.DataType, &nullable, &item.Default, &item.Length, &item.Precision, &item.Comment, &pk); err != nil {
			var nullableText string
			if err2 := rows.Scan(&item.Name, &item.DataType, &nullableText, &item.Default, &item.Length, &item.Precision, &item.Comment, &pk); err2 != nil {
				return nil, err
			}
			item.Nullable = strings.EqualFold(nullableText, "YES")
		} else {
			item.Nullable = nullable != 0
		}
		item.PrimaryKey = pk != 0
		item.Indexed = item.PrimaryKey
		items = append(items, item)
	}
	return items, rows.Err()
}

func buildSelect(dbType DBType, req TableDataRequest, columns []ColumnInfo, includeSearch bool) (string, []interface{}) {
	args := []interface{}{}
	table := qualifiedTable(dbType, req.TableRef)
	limit := req.PageSize
	offset := (req.Page - 1) * req.PageSize
	query := "select * from " + table
	where := []string{}
	if includeSearch && strings.TrimSpace(req.Search) != "" {
		searchCols := req.SearchColumns
		if len(searchCols) == 0 {
			for _, col := range columns {
				searchCols = append(searchCols, col.Name)
			}
		}
		parts := []string{}
		for _, col := range searchCols {
			if !hasColumn(columns, col) {
				continue
			}
			args = append(args, "%"+req.Search+"%")
			if dbType == DBTypePostgres {
				parts = append(parts, fmt.Sprintf("cast(%s as text) ilike %s", quoteIdent(dbType, col), placeholder(dbType, len(args))))
			} else {
				parts = append(parts, fmt.Sprintf("cast(%s as char) like %s", quoteIdent(dbType, col), placeholder(dbType, len(args))))
			}
		}
		if len(parts) > 0 {
			where = append(where, "("+strings.Join(parts, " or ")+")")
		}
	}
	for _, filter := range req.Filters {
		clause, vals := buildFilterClause(dbType, columns, filter, len(args)+1)
		if clause != "" {
			where = append(where, clause)
			args = append(args, vals...)
		}
	}
	if len(where) > 0 {
		query += " where " + strings.Join(where, " and ")
	}
	if req.OrderBy != "" && hasColumn(columns, req.OrderBy) {
		query += " order by " + quoteIdent(dbType, req.OrderBy)
		if req.Descending {
			query += " desc"
		}
	}
	if dbType == DBTypePostgres {
		query += fmt.Sprintf(" limit %d offset %d", limit, offset)
	} else {
		query += fmt.Sprintf(" limit %d offset %d", limit, offset)
	}
	return query, args
}

func buildFilterClause(dbType DBType, columns []ColumnInfo, filter AdvancedFilter, start int) (string, []interface{}) {
	if !hasColumn(columns, filter.Column) {
		return "", nil
	}
	op := strings.ToLower(strings.TrimSpace(filter.Operator))
	column := quoteIdent(dbType, filter.Column)
	switch op {
	case "=", "!=", ">", ">=", "<", "<=", "like", "not like":
		return fmt.Sprintf("%s %s %s", column, op, placeholder(dbType, start)), []interface{}{filter.Value}
	case "is null", "is not null":
		return fmt.Sprintf("%s %s", column, op), nil
	case "between":
		if len(filter.Values) < 2 {
			return "", nil
		}
		return fmt.Sprintf("%s between %s and %s", column, placeholder(dbType, start), placeholder(dbType, start+1)), []interface{}{filter.Values[0], filter.Values[1]}
	case "in", "not in":
		if len(filter.Values) == 0 {
			return "", nil
		}
		holders := make([]string, len(filter.Values))
		vals := make([]interface{}, len(filter.Values))
		for i, v := range filter.Values {
			holders[i] = placeholder(dbType, start+i)
			vals[i] = v
		}
		return fmt.Sprintf("%s %s (%s)", column, op, strings.Join(holders, ",")), vals
	default:
		return "", nil
	}
}

func estimateImportRows(ctx context.Context, db *sql.DB, dbType DBType, ref TableRef) (int64, error) {
	if dbType == DBTypeMySQL {
		var rows sql.NullInt64
		err := db.QueryRowContext(ctx, `select table_rows from information_schema.tables where table_schema=? and table_name=?`, ref.Database, ref.Table).Scan(&rows)
		if err != nil || !rows.Valid {
			return 0, err
		}
		return rows.Int64, nil
	}
	schema := ref.Schema
	if schema == "" {
		schema = "public"
	}
	var rows sql.NullFloat64
	err := db.QueryRowContext(ctx, `select c.reltuples from pg_class c join pg_namespace n on n.oid=c.relnamespace where n.nspname=$1 and c.relname=$2`, schema, ref.Table).Scan(&rows)
	if err != nil || !rows.Valid {
		return 0, err
	}
	if rows.Float64 < 0 {
		return 0, nil
	}
	return int64(rows.Float64), nil
}

func buildImportSelect(dbType DBType, ref TableRef, mappings []FieldMapping, limit int) (string, error) {
	seen := map[string]bool{}
	columns := []string{}
	for _, mapping := range mappings {
		source := strings.TrimSpace(mapping.SourceColumn)
		if source == "" || seen[source] {
			continue
		}
		seen[source] = true
		columns = append(columns, quoteIdent(dbType, source))
	}
	if len(columns) == 0 {
		return "", fmt.Errorf("没有可导入的源字段")
	}
	if limit <= 0 || limit > MaxImportRows {
		limit = MaxImportRows
	}
	return fmt.Sprintf("select %s from %s limit %d", strings.Join(columns, ","), qualifiedTable(dbType, ref), limit), nil
}

func scanCurrentRow(rows *sql.Rows, names []string) (map[string]interface{}, error) {
	values := make([]interface{}, len(names))
	ptrs := make([]interface{}, len(names))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	item := make(map[string]interface{}, len(names))
	for i, name := range names {
		switch v := values[i].(type) {
		case nil:
			item[name] = nil
		case []byte:
			item[name] = string(v)
		case time.Time:
			item[name] = v.Format("2006-01-02 15:04:05")
		default:
			item[name] = v
		}
	}
	return item, nil
}

func newScanBuffers(size int) ([]interface{}, []interface{}) {
	values := make([]interface{}, size)
	targets := make([]interface{}, size)
	for i := range values {
		targets[i] = &values[i]
	}
	return values, targets
}

func scanCurrentValues(rows *sql.Rows, values, targets []interface{}) error {
	for i := range values {
		values[i] = nil
	}
	return rows.Scan(targets...)
}

func scanRows(rows *sql.Rows) ([]ColumnInfo, []map[string]interface{}, error) {
	names, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	cols := make([]ColumnInfo, len(names))
	for i, name := range names {
		cols[i] = ColumnInfo{Name: name}
	}
	result := []map[string]interface{}{}
	for rows.Next() {
		values := make([]interface{}, len(names))
		ptrs := make([]interface{}, len(names))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		item := make(map[string]interface{}, len(names))
		for i, name := range names {
			switch v := values[i].(type) {
			case nil:
				item[name] = nil
			case []byte:
				item[name] = string(v)
			case time.Time:
				item[name] = v.Format("2006-01-02 15:04:05")
			default:
				item[name] = v
			}
		}
		result = append(result, item)
	}
	return cols, result, rows.Err()
}

func scanStrings(ctx context.Context, db *sql.DB, query string, args ...interface{}) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, sanitizeDBError(err)
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err == nil {
			items = append(items, item)
		}
	}
	return items, rows.Err()
}

func AutoMapPayload(cols []ColumnInfo) map[string]interface{} {
	hash := ColumnsHash(cols)
	return map[string]interface{}{"sourceColumnsHash": hash, "mappings": BuildAutoMappings(cols), "targetFields": FlowTargetFields}
}

func BuildAutoMappings(cols []ColumnInfo) []FieldMapping {
	mappings := []FieldMapping{}
	used := map[string]bool{}
	for _, target := range FlowTargetFields {
		best := FieldMapping{TargetField: target.TargetField, TargetType: target.TargetType, Required: target.Required}
		bestScore := 0
		for _, col := range cols {
			if used[col.Name] {
				continue
			}
			score := scoreColumn(col.Name, target.TargetField)
			if score > bestScore {
				bestScore = score
				best.SourceColumn = col.Name
				best.SourceType = col.DataType
				best.Confidence = score
			}
		}
		if best.SourceColumn != "" && bestScore >= 45 {
			used[best.SourceColumn] = true
			mappings = append(mappings, best)
		} else if target.Required {
			mappings = append(mappings, best)
		}
	}
	return mappings
}

func ColumnsHash(cols []ColumnInfo) string {
	parts := make([]string, len(cols))
	for i, col := range cols {
		parts[i] = strings.ToLower(col.Name) + ":" + strings.ToLower(col.DataType)
	}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("%x", sum)
}

func scoreColumn(source, target string) int {
	s := normalizeName(source)
	t := normalizeName(target)
	if s == t {
		return 100
	}
	aliases := map[string][]string{
		"交易时间":    {"交易时间", "付款时间", "支付时间", "记账时间", "交易日期", "time", "paytime", "createdat"},
		"交易金额":    {"金额", "交易金额", "发生额", "收入", "支出", "amount", "money"},
		"收付标志":    {"收付", "收支", "收/支", "借贷方向", "方向", "type", "direction"},
		"交易方户名":   {"交易方户名", "交易户名", "本方户名", "户名", "姓名", "name"},
		"交易方账户":   {"交易方账户", "交易账号", "交易卡号", "账号", "账户", "account", "card"},
		"交易对手账卡号": {"交易对手账卡号", "对手账号", "对方账号", "对手卡号", "account", "card"},
		"对手户名":    {"对手户名", "对方户名", "交易对方", "对方名称", "name"},
		"摘要说明":    {"摘要", "备注", "交易说明", "用途", "remark", "memo", "description"},
		"备注":      {"备注", "remark", "memo"},
	}
	for _, alias := range aliases[target] {
		a := normalizeName(alias)
		if s == a {
			return 95
		}
		if strings.Contains(s, a) || strings.Contains(a, s) {
			return 70
		}
	}
	if strings.Contains(s, t) || strings.Contains(t, s) {
		return 60
	}
	return 0
}

func normalizeName(value string) string {
	replacer := strings.NewReplacer(" ", "", "_", "", "-", "", "/", "", "\\", "", "(", "", ")", "", "（", "", "）", "")
	return strings.ToLower(replacer.Replace(strings.TrimSpace(value)))
}

func validateMappings(mappings []FieldMapping) error {
	targets := map[string]string{}
	for _, mapping := range mappings {
		if mapping.SourceColumn == "" || mapping.TargetField == "" {
			continue
		}
		if previous := targets[mapping.TargetField]; previous != "" {
			return fmt.Errorf("目标字段 %s 被多个源字段映射：%s、%s", mapping.TargetField, previous, mapping.SourceColumn)
		}
		targets[mapping.TargetField] = mapping.SourceColumn
	}
	for _, target := range FlowTargetFields {
		if target.Required && targets[target.TargetField] == "" {
			return fmt.Errorf("必填字段未映射：%s", target.TargetField)
		}
	}
	return nil
}

func buildHeaderIndex(headers []string) map[string]int {
	headerIndex := map[string]int{}
	for i, header := range headers {
		headerIndex[header] = i
	}
	return headerIndex
}

func appendImportError(task *ImportTask, item ImportError) {
	if len(task.Errors) < maxStoredImportErrors {
		task.Errors = append(task.Errors, item)
	}
}

type importColumnMapping struct {
	sourceIndex int
	targetIndex int
	targetType  string
}

type importTargetRule struct {
	index       int
	targetField string
	targetType  string
	required    bool
}

type importRowMapper struct {
	sourceColumns   []string
	columnMappings  []importColumnMapping
	targetRules     []importTargetRule
	datetimeTargets []int
	datetimeReady   []bool
	decimalTargets  []int
	decimalReady    []bool
}

func newImportRowMapper(sourceColumns []string, mappings []FieldMapping, headers []string, headerIndex map[string]int) (*importRowMapper, error) {
	sourceIndex := make(map[string]int, len(sourceColumns))
	for i, column := range sourceColumns {
		sourceIndex[column] = i
	}
	targetTypes := make(map[string]string, len(FlowTargetFields))
	for _, target := range FlowTargetFields {
		targetTypes[target.TargetField] = target.TargetType
	}
	mapper := &importRowMapper{
		sourceColumns:  sourceColumns,
		columnMappings: make([]importColumnMapping, 0, len(mappings)),
		targetRules:    make([]importTargetRule, 0, len(FlowTargetFields)),
		datetimeReady:  make([]bool, len(headers)),
		decimalReady:   make([]bool, len(headers)),
	}
	for _, mapping := range mappings {
		if mapping.SourceColumn == "" || mapping.TargetField == "" {
			continue
		}
		targetIndex, ok := headerIndex[mapping.TargetField]
		if !ok {
			continue
		}
		idx, ok := sourceIndex[mapping.SourceColumn]
		if !ok {
			return nil, fmt.Errorf("导入查询未返回映射字段：%s", mapping.SourceColumn)
		}
		targetType := mapping.TargetType
		if targetType == "" {
			targetType = targetTypes[mapping.TargetField]
		}
		mapper.columnMappings = append(mapper.columnMappings, importColumnMapping{
			sourceIndex: idx,
			targetIndex: targetIndex,
			targetType:  targetType,
		})
	}
	for _, target := range FlowTargetFields {
		idx, ok := headerIndex[target.TargetField]
		if !ok {
			continue
		}
		if target.TargetType == "datetime" {
			mapper.datetimeTargets = append(mapper.datetimeTargets, idx)
		}
		if target.TargetType == "decimal" {
			mapper.decimalTargets = append(mapper.decimalTargets, idx)
		}
		mapper.targetRules = append(mapper.targetRules, importTargetRule{
			index:       idx,
			targetField: target.TargetField,
			targetType:  target.TargetType,
			required:    target.Required,
		})
	}
	return mapper, nil
}

func mapImportRow(row map[string]interface{}, mappings []FieldMapping, headers []string) ([]string, string, error) {
	return mapImportRowWithIndex(row, mappings, headers, buildHeaderIndex(headers))
}

func mapImportRowWithIndex(row map[string]interface{}, mappings []FieldMapping, headers []string, headerIndex map[string]int) ([]string, string, error) {
	record := make([]string, len(headers))
	for _, mapping := range mappings {
		idx, ok := headerIndex[mapping.TargetField]
		if !ok || mapping.SourceColumn == "" {
			continue
		}
		if v := row[mapping.SourceColumn]; v != nil {
			record[idx] = fmt.Sprint(v)
		}
	}
	for _, target := range FlowTargetFields {
		idx := headerIndex[target.TargetField]
		if target.Required && strings.TrimSpace(record[idx]) == "" {
			return nil, snapshot(row), fmt.Errorf("必填字段为空：%s", target.TargetField)
		}
		if target.TargetType == "datetime" {
			record[idx] = parser.NormalizeDatetime(record[idx])
			if strings.TrimSpace(record[idx]) == "" {
				return nil, snapshot(row), fmt.Errorf("时间字段无法转换：%s", target.TargetField)
			}
		}
		if target.TargetType == "decimal" {
			record[idx] = strconv.FormatFloat(parser.ToNumber(record[idx]), 'f', 2, 64)
		}
		if target.TargetType == "direction" && record[idx] != "" {
			record[idx] = parser.NormalizeDirection(record[idx])
		}
	}
	return record, "", nil
}

func (m *importRowMapper) mapValuesInto(values []interface{}, record []string) (string, error) {
	for i := range record {
		record[i] = ""
	}
	for _, idx := range m.datetimeTargets {
		m.datetimeReady[idx] = false
	}
	for _, idx := range m.decimalTargets {
		m.decimalReady[idx] = false
	}
	for _, mapping := range m.columnMappings {
		raw := values[mapping.sourceIndex]
		if raw == nil {
			continue
		}
		switch mapping.targetType {
		case "datetime":
			record[mapping.targetIndex] = normalizeImportDatetime(raw)
			m.datetimeReady[mapping.targetIndex] = true
		case "decimal":
			if text, ok := formatImportDecimal(raw); ok {
				record[mapping.targetIndex] = text
				m.decimalReady[mapping.targetIndex] = true
			} else {
				record[mapping.targetIndex] = dbValueToString(raw)
			}
		default:
			record[mapping.targetIndex] = dbValueToString(raw)
		}
	}
	for _, target := range m.targetRules {
		if target.required && strings.TrimSpace(record[target.index]) == "" {
			return snapshotValues(m.sourceColumns, values), fmt.Errorf("必填字段为空：%s", target.targetField)
		}
		if target.targetType == "datetime" && !m.datetimeReady[target.index] {
			record[target.index] = parser.NormalizeDatetime(record[target.index])
			if strings.TrimSpace(record[target.index]) == "" {
				return snapshotValues(m.sourceColumns, values), fmt.Errorf("时间字段无法转换：%s", target.targetField)
			}
		}
		if target.targetType == "decimal" && !m.decimalReady[target.index] {
			record[target.index] = strconv.FormatFloat(parser.ToNumber(record[target.index]), 'f', 2, 64)
		}
		if target.targetType == "direction" && record[target.index] != "" {
			record[target.index] = parser.NormalizeDirection(record[target.index])
		}
	}
	return "", nil
}

// fastSplitCopyCSVLine splits a single CSV row from PostgreSQL COPY TO STDOUT WITH CSV.
// PostgreSQL properly quotes fields containing commas/ quotes; this handles the common cases
// without a full csv.Reader allocation per row.
func fastSplitCopyCSVLine(line []byte) []string {
	if len(line) == 0 {
		return nil
	}
	// strip trailing newline
	if line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	result := make([]string, 0, copySplitFieldsLimit)
	i := 0
	for i < len(line) {
		if line[i] == '"' {
			i++ // skip opening quote
			start := i
			for i < len(line) {
				if line[i] == '"' {
					if i+1 < len(line) && line[i+1] == '"' {
						i += 2
						continue
					}
					result = append(result, strings.ReplaceAll(string(line[start:i]), `""`, `"`))
					i++ // skip closing quote
					break
				}
				i++
			}
			if i < len(line) && line[i] == ',' {
				i++
			}
		} else {
			start := i
			for i < len(line) && line[i] != ',' {
				i++
			}
			result = append(result, string(line[start:i]))
			if i < len(line) {
				i++ // skip ','
			}
		}
	}
	return result
}

// importPGWithCopy uses PostgreSQL COPY protocol to stream source data at maximum throughput,
// bypassing the database/sql row scanning overhead.
func (s *Service) importPGWithCopy(
	ctx context.Context, db *sql.DB, task *ImportTask, item ImportTable,
	selectQuery string, headers []string, headerIndex map[string]int,
	rowNumber *int64, sample *[]map[string]interface{},
	writer *csv.Writer, start *time.Time,
	saveProgress func(bool), limit int,
) error {
	copySQL := fmt.Sprintf("COPY (%s) TO STDOUT WITH (FORMAT CSV, HEADER false, DELIMITER ',')", selectQuery)
	rows, err := db.QueryContext(ctx, copySQL)
	if err != nil {
		return sanitizeDBError(err)
	}
	defer rows.Close()

	// Build column mapper from the SELECT column order
	sourceCols := extractSelectColumns(selectQuery)
	mapper, err := newImportRowMapper(sourceCols, item.Mappings, headers, headerIndex)
	if err != nil {
		return fmt.Errorf("列映射失败: %w", err)
	}

	record := make([]string, len(headers))
	var tableProcessed int64
	var lastCancelCheck int64
	var lineBuf []byte

	for rows.Next() {
		if ctx.Err() != nil {
			task.Status = "canceled"
			task.UpdatedAt = time.Now()
			_ = s.store.SaveTask(*task)
			return ctx.Err()
		}
		if err := rows.Scan(&lineBuf); err != nil {
			*rowNumber++
			task.Progress.ProcessedRows++
			task.Progress.FailedRows++
			appendImportError(task, ImportError{Table: item.Table, Row: *rowNumber, Reason: fmt.Sprintf("COPY读取行失败: %v", err), CreatedAt: time.Now()})
			saveProgress(false)
			continue
		}
		*rowNumber++
		task.Progress.ProcessedRows++
		tableProcessed++

		fields := fastSplitCopyCSVLine(lineBuf)
		if len(fields) == 0 {
			task.Progress.FailedRows++
			appendImportError(task, ImportError{Table: item.Table, Row: *rowNumber, Reason: "COPY行解析为空", CreatedAt: time.Now()})
			saveProgress(false)
			continue
		}
		if snapshot, err := mapper.mapCopyValuesInto(fields, record); err != nil {
			task.Progress.FailedRows++
			appendImportError(task, ImportError{Table: item.Table, Row: *rowNumber, Reason: err.Error(), Snapshot: snapshot, CreatedAt: time.Now()})
			saveProgress(false)
			continue
		}
		if err := writer.Write(record); err != nil {
			task.Progress.FailedRows++
			appendImportError(task, ImportError{Table: item.Table, Row: *rowNumber, Reason: fmt.Sprintf("CSV写入失败: %v", err), CreatedAt: time.Now()})
			saveProgress(false)
			continue
		}
		task.Progress.SuccessRows++
		if len(*sample) < 20 {
			*sample = append(*sample, recordToMap(headers, record))
		}
		if task.Progress.ProcessedRows-lastCancelCheck >= importCancelCheckRows {
			if latest, err := s.store.GetTask(task.ID); err == nil && latest.Status == "canceled" {
				task.Status = "canceled"
				task.UpdatedAt = time.Now()
				_ = s.store.SaveTask(*task)
				return nil
			}
			lastCancelCheck = task.Progress.ProcessedRows
		}
		saveProgress(false)
		if tableProcessed >= int64(limit) {
			break
		}
	}
	return rows.Err()
}

// extractSelectColumns parses column names from a "select col1, col2 from ..." query.
func extractSelectColumns(query string) []string {
	q := strings.TrimSpace(query)
	lower := strings.ToLower(q)
	// Find "select" and "from"
	selIdx := strings.Index(lower, "select")
	if selIdx < 0 {
		return nil
	}
	fromIdx := strings.Index(lower[selIdx:], " from ")
	if fromIdx < 0 {
		return nil
	}
	colsPart := q[selIdx+6 : selIdx+fromIdx]
	colsPart = strings.TrimSpace(colsPart)
	// Remove quoted identifiers and split by comma
	cols := make([]string, 0, 16)
	i := 0
	for i < len(colsPart) {
		// skip leading whitespace
		for i < len(colsPart) && (colsPart[i] == ' ' || colsPart[i] == '\t') {
			i++
		}
		if i >= len(colsPart) {
			break
		}
		var col string
		if colsPart[i] == '"' {
			i++ // skip opening quote
			start := i
			for i < len(colsPart) {
				if colsPart[i] == '"' {
					if i+1 < len(colsPart) && colsPart[i+1] == '"' {
						i += 2
						continue
					}
					col = strings.ReplaceAll(colsPart[start:i], `""`, `"`)
					i++ // skip closing quote
					break
				}
				i++
			}
		} else {
			start := i
			for i < len(colsPart) && colsPart[i] != ',' {
				i++
			}
			col = strings.TrimSpace(colsPart[start:i])
		}
		if col != "" {
			cols = append(cols, col)
		}
		if i < len(colsPart) && colsPart[i] == ',' {
			i++ // skip ','
		}
	}
	return cols
}

func (m *importRowMapper) mapCopyValuesInto(fields []string, record []string) (string, error) {
	for i := range record {
		record[i] = ""
	}
	for _, mapping := range m.columnMappings {
		if mapping.sourceIndex >= len(fields) {
			continue
		}
		raw := fields[mapping.sourceIndex]
		switch mapping.targetType {
		case "datetime":
			record[mapping.targetIndex] = parser.NormalizeDatetime(raw)
		case "decimal":
			if raw == "" {
				record[mapping.targetIndex] = ""
			} else {
				record[mapping.targetIndex] = strconv.FormatFloat(parser.ToNumber(raw), 'f', 2, 64)
			}
		default:
			record[mapping.targetIndex] = raw
		}
	}
	for _, target := range m.targetRules {
		if target.required && strings.TrimSpace(record[target.index]) == "" {
			return snapshotCopyValues(m.sourceColumns, fields), fmt.Errorf("必填字段为空：%s", target.targetField)
		}
		if target.targetType == "datetime" && record[target.index] != "" {
			normalized := parser.NormalizeDatetime(record[target.index])
			if strings.TrimSpace(normalized) == "" {
				return snapshotCopyValues(m.sourceColumns, fields), fmt.Errorf("时间字段无法转换：%s", target.targetField)
			}
			record[target.index] = normalized
		}
		if target.targetType == "decimal" && record[target.index] != "" {
			record[target.index] = strconv.FormatFloat(parser.ToNumber(record[target.index]), 'f', 2, 64)
		}
		if target.targetType == "direction" && record[target.index] != "" {
			record[target.index] = parser.NormalizeDirection(record[target.index])
		}
	}
	return "", nil
}

func snapshotCopyValues(columns []string, fields []string) string {
	parts := make([]string, 0, 8)
	for i, col := range columns {
		if i >= len(fields) {
			break
		}
		if i >= 8 {
			break
		}
		parts = append(parts, col+"="+fields[i])
	}
	return strings.Join(parts, "; ")
}

func normalizeImportDatetime(value interface{}) string {
	if t, ok := value.(time.Time); ok {
		return t.Format("2006-01-02 15:04:05")
	}
	return parser.NormalizeDatetime(dbValueToString(value))
}

func formatImportDecimal(value interface{}) (string, bool) {
	number, ok := dbValueToNumber(value)
	if !ok {
		return "", false
	}
	return strconv.FormatFloat(number, 'f', 2, 64), true
}

func dbValueToNumber(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func dbValueToString(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case time.Time:
		return v.Format("2006-01-02 15:04:05")
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprint(v)
	}
}

func flowCSVHeaders() []string {
	headers := make([]string, len(FlowTargetFields))
	for i, item := range FlowTargetFields {
		headers[i] = item.TargetField
	}
	return headers
}

func recordToMap(headers, record []string) map[string]interface{} {
	item := make(map[string]interface{}, len(headers))
	for i, header := range headers {
		item[header] = record[i]
	}
	return item
}

func snapshot(row map[string]interface{}) string {
	parts := []string{}
	for key, value := range row {
		parts = append(parts, key+"="+fmt.Sprint(value))
	}
	sort.Strings(parts)
	if len(parts) > 8 {
		parts = parts[:8]
	}
	return strings.Join(parts, "; ")
}

func snapshotValues(columns []string, values []interface{}) string {
	parts := make([]string, 0, len(columns))
	for i, column := range columns {
		if i >= len(values) {
			break
		}
		parts = append(parts, column+"="+dbValueToString(values[i]))
	}
	if len(parts) > 8 {
		parts = parts[:8]
	}
	return strings.Join(parts, "; ")
}

func qualifiedTable(dbType DBType, ref TableRef) string {
	if dbType == DBTypePostgres {
		schema := ref.Schema
		if schema == "" {
			schema = "public"
		}
		return quoteIdent(dbType, schema) + "." + quoteIdent(dbType, ref.Table)
	}
	return quoteIdent(dbType, ref.Database) + "." + quoteIdent(dbType, ref.Table)
}

func quoteIdent(dbType DBType, value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if dbType == DBTypeMySQL {
		return "`" + strings.ReplaceAll(value, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func placeholder(dbType DBType, index int) string {
	if dbType == DBTypePostgres {
		return "$" + strconv.Itoa(index)
	}
	return "?"
}

func hasColumn(columns []ColumnInfo, name string) bool {
	for _, col := range columns {
		if col.Name == name {
			return true
		}
	}
	return false
}

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}
	return page, pageSize
}

func isSelectSQL(sqlText string) bool {
	trimmed := strings.TrimSpace(strings.TrimLeft(sqlText, "\ufeff"))
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "select") || strings.HasPrefix(lower, "with")
}

func isDangerousWrite(sqlText string) bool {
	lower := strings.ToLower(sqlText)
	if strings.Contains(lower, "update ") && !strings.Contains(lower, " where ") {
		return true
	}
	if strings.Contains(lower, "delete ") && !strings.Contains(lower, " where ") {
		return true
	}
	return false
}

func sanitizeDBError(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if strings.Contains(strings.ToLower(text), "password") {
		return fmt.Errorf("数据库连接失败，请检查主机、端口、用户名、密码或权限")
	}
	return fmt.Errorf("%s", text)
}

func TransactionRowsFromTask(task ImportTask) []model.TransactionRow {
	rows := make([]model.TransactionRow, 0, len(task.Sample))
	for _, item := range task.Sample {
		row := model.TransactionRow{}
		for key, value := range item {
			if value != nil {
				row[key] = fmt.Sprint(value)
			}
		}
		rows = append(rows, row)
	}
	return rows
}
