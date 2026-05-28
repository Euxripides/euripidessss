package dbimport

import (
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
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
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
	sessionDir := filepath.Join(s.uploadDir, "flow_sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return task, err
	}
	outputPath := filepath.Join(sessionDir, "database_import.csv")
	file, err := os.Create(outputPath)
	if err != nil {
		return task, err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	headers := flowCSVHeaders()
	if err := writer.Write(headers); err != nil {
		return task, err
	}
	sample := []map[string]interface{}{}
	var rowNumber int64
	for _, item := range task.Tables {
		if err := validateMappings(item.Mappings); err != nil {
			task.Status = "failed"
			task.Errors = append(task.Errors, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
			_ = s.store.SaveTask(task)
			return task, err
		}
		req := TableDataRequest{TableRef: item.TableRef, Page: 1, PageSize: MaxPageSize}
		limit := item.Limit
		if limit <= 0 || limit > MaxImportRows {
			limit = MaxImportRows
		}
		for task.Progress.ProcessedRows < int64(MaxImportRows) {
			if ctx.Err() != nil {
				task.Status = "canceled"
				task.UpdatedAt = time.Now()
				_ = s.store.SaveTask(task)
				return task, ctx.Err()
			}
			if latest, err := s.store.GetTask(id); err == nil && latest.Status == "canceled" {
				task.Status = "canceled"
				task.UpdatedAt = time.Now()
				_ = s.store.SaveTask(task)
				return task, nil
			}
			resp, err := s.Preview(ctx, req)
			if err != nil {
				task.Errors = append(task.Errors, ImportError{Table: item.Table, Reason: err.Error(), CreatedAt: time.Now()})
				break
			}
			if len(resp.Rows) == 0 {
				break
			}
			for _, row := range resp.Rows {
				rowNumber++
				task.Progress.ProcessedRows++
				record, snapshot, err := mapImportRow(row, item.Mappings, headers)
				if err != nil {
					task.Progress.FailedRows++
					task.Errors = append(task.Errors, ImportError{Table: item.Table, Row: rowNumber, Reason: err.Error(), Snapshot: snapshot, CreatedAt: time.Now()})
					continue
				}
				if err := writer.Write(record); err != nil {
					task.Progress.FailedRows++
					task.Errors = append(task.Errors, ImportError{Table: item.Table, Row: rowNumber, Reason: err.Error(), CreatedAt: time.Now()})
					continue
				}
				task.Progress.SuccessRows++
				if len(sample) < 20 {
					sample = append(sample, recordToMap(headers, record))
				}
				if task.Progress.ProcessedRows >= int64(limit) {
					break
				}
			}
			if len(resp.Rows) < req.PageSize || task.Progress.ProcessedRows >= int64(limit) {
				break
			}
			req.Page++
			elapsed := math.Max(time.Since(start).Seconds(), 0.001)
			task.Progress.SpeedRowsPerSecond = float64(task.Progress.ProcessedRows) / elapsed
			task.UpdatedAt = time.Now()
			_ = s.store.SaveTask(task)
		}
	}
	task.SessionID = sessionID
	task.Columns = headers
	task.Files = []string{"database_import.csv"}
	task.Sample = sample
	task.Progress.TotalRows = task.Progress.ProcessedRows
	task.Status = "completed"
	if task.Progress.FailedRows > 0 {
		task.Status = "completed_with_errors"
	}
	task.UpdatedAt = time.Now()
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
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d",
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

func mapImportRow(row map[string]interface{}, mappings []FieldMapping, headers []string) ([]string, string, error) {
	record := make([]string, len(headers))
	headerIndex := map[string]int{}
	for i, header := range headers {
		headerIndex[header] = i
	}
	for _, mapping := range mappings {
		idx, ok := headerIndex[mapping.TargetField]
		if !ok || mapping.SourceColumn == "" {
			continue
		}
		record[idx] = fmt.Sprint(row[mapping.SourceColumn])
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
			row[key] = fmt.Sprint(value)
		}
		rows = append(rows, row)
	}
	return rows
}
