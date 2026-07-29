package dbimport

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	exportCSVBufferSize  = 16 * 1024 * 1024
	mysqlExportBatchSize = 500
)

type ExportRequest struct {
	JobID         string `json:"jobId"`
	ConnectionID  string `json:"connectionId"`
	Database      string `json:"database"`
	Schema        string `json:"schema,omitempty"`
	Table         string `json:"table"`
	Mode          string `json:"mode"`
	ColumnNaming  string `json:"columnNaming"`
	DuplicateMode string `json:"duplicateMode"`
}

type ExportProgress struct {
	TotalRows          int64   `json:"totalRows"`
	ProcessedRows      int64   `json:"processedRows"`
	InsertedRows       int64   `json:"insertedRows"`
	SkippedRows        int64   `json:"skippedRows"`
	SpeedRowsPerSecond float64 `json:"speedRowsPerSecond"`
	ElapsedSeconds     float64 `json:"elapsedSeconds"`
	ETASeconds         float64 `json:"etaSeconds"`
}

type ExportTask struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	Request    ExportRequest  `json:"request"`
	Progress   ExportProgress `json:"progress"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
	StartedAt  *time.Time     `json:"startedAt,omitempty"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty"`
	sourcePath string
}

type ExportManager struct {
	service *Service
	mu      sync.RWMutex
	tasks   map[string]*ExportTask
	cancels map[string]context.CancelFunc
}

func NewExportManager(service *Service) *ExportManager {
	return &ExportManager{
		service: service,
		tasks:   make(map[string]*ExportTask),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (m *ExportManager) CreateAndStart(request ExportRequest, sourcePath string, totalRows int64) (ExportTask, error) {
	if err := validateExportRequest(&request); err != nil {
		return ExportTask{}, err
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return ExportTask{}, fmt.Errorf("清洗结果 CSV 不存在: %w", err)
	}
	if _, err := m.service.store.GetConnection(request.ConnectionID); err != nil {
		return ExportTask{}, err
	}
	now := time.Now()
	task := &ExportTask{
		ID:         uuid.NewString(),
		Status:     "pending",
		Request:    request,
		Progress:   ExportProgress{TotalRows: totalRows},
		CreatedAt:  now,
		sourcePath: sourcePath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.tasks[task.ID] = task
	m.cancels[task.ID] = cancel
	m.mu.Unlock()
	go m.run(ctx, task.ID)
	return m.Get(task.ID)
}

func (m *ExportManager) Get(id string) (ExportTask, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task := m.tasks[id]
	if task == nil {
		return ExportTask{}, fmt.Errorf("数据库导入任务不存在")
	}
	return cloneExportTask(task), nil
}

func (m *ExportManager) Cancel(id string) (ExportTask, error) {
	m.mu.Lock()
	cancel := m.cancels[id]
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return ExportTask{}, fmt.Errorf("数据库导入任务不存在")
	}
	if cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	return m.Get(id)
}

func (m *ExportManager) run(ctx context.Context, id string) {
	startedAt := time.Now()
	m.mu.Lock()
	task := m.tasks[id]
	task.Status = "running"
	task.StartedAt = &startedAt
	m.mu.Unlock()

	inserted, processed, err := m.exportCSV(ctx, id)
	finishedAt := time.Now()
	m.mu.Lock()
	task = m.tasks[id]
	task.Progress.ProcessedRows = processed
	task.Progress.InsertedRows = inserted
	task.Progress.SkippedRows = maxInt64(0, processed-inserted)
	task.Progress.ElapsedSeconds = finishedAt.Sub(startedAt).Seconds()
	if task.Progress.ElapsedSeconds > 0 {
		task.Progress.SpeedRowsPerSecond = float64(processed) / task.Progress.ElapsedSeconds
	}
	task.Progress.ETASeconds = 0
	task.FinishedAt = &finishedAt
	if err == nil {
		task.Status = "done"
	} else if ctx.Err() != nil {
		task.Status = "cancelled"
		task.Error = "用户已取消数据库导入"
	} else {
		task.Status = "failed"
		task.Error = sanitizeDBError(err).Error()
	}
	delete(m.cancels, id)
	m.mu.Unlock()
}

func (m *ExportManager) exportCSV(ctx context.Context, id string) (int64, int64, error) {
	task, err := m.Get(id)
	if err != nil {
		return 0, 0, err
	}
	conn, err := m.service.store.GetConnection(task.Request.ConnectionID)
	if err != nil {
		return 0, 0, err
	}
	db, err := m.service.Open(ctx, conn, task.Request.Database)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()

	file, err := os.Open(task.sourcePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	reader := csv.NewReader(bufio.NewReaderSize(file, exportCSVBufferSize))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	headers, err := reader.Read()
	if err != nil {
		return 0, 0, fmt.Errorf("读取 CSV 表头失败: %w", err)
	}
	if len(headers) == 0 {
		return 0, 0, fmt.Errorf("CSV 没有字段")
	}
	dbColumns := exportColumnNames(headers, task.Request.ColumnNaming)
	ref := TableRef{
		ConnectionID: task.Request.ConnectionID,
		Database:     task.Request.Database,
		Schema:       task.Request.Schema,
		Table:        task.Request.Table,
	}
	if conn.Type == DBTypePostgres {
		return m.exportPostgres(ctx, id, db, ref, headers, dbColumns, reader)
	}
	return m.exportMySQL(ctx, id, db, ref, headers, dbColumns, reader)
}

func (m *ExportManager) exportPostgres(
	ctx context.Context,
	id string,
	db *sql.DB,
	ref TableRef,
	headers []string,
	dbColumns []string,
	reader *csv.Reader,
) (int64, int64, error) {
	task, _ := m.Get(id)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	target := qualifiedTable(DBTypePostgres, ref)
	if task.Request.Mode == "replace" {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+target); err != nil {
			return 0, 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, createExportTableSQL(DBTypePostgres, target, headers, dbColumns)); err != nil {
		return 0, 0, err
	}
	if err := ensureExportTableColumns(ctx, tx, DBTypePostgres, ref, headers, dbColumns); err != nil {
		return 0, 0, err
	}
	tempName := "etl_stage_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	tempColumns := append(append([]string{}, dbColumns...), "source_job_id", "source_row_hash")
	definitions := exportColumnDefinitions(DBTypePostgres, headers, dbColumns)
	definitions = append(definitions, quoteIdent(DBTypePostgres, "source_job_id")+" text NOT NULL")
	definitions = append(definitions, quoteIdent(DBTypePostgres, "source_row_hash")+" char(64) NOT NULL")
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE "+quoteIdent(DBTypePostgres, tempName)+" ("+strings.Join(definitions, ", ")+") ON COMMIT DROP"); err != nil {
		return 0, 0, err
	}
	copyStatement, err := tx.PrepareContext(ctx, pq.CopyIn(tempName, tempColumns...))
	if err != nil {
		return 0, 0, err
	}
	started := time.Now()
	var processed int64
	for {
		if err := ctx.Err(); err != nil {
			copyStatement.Close()
			return 0, processed, err
		}
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			copyStatement.Close()
			return 0, processed, fmt.Errorf("读取 CSV 第 %d 行失败: %w", processed+2, readErr)
		}
		record = normalizeCSVRecord(record, len(headers))
		processed++
		values := exportDBValues(headers, record)
		values = append(values, task.Request.JobID, exportRowHash(record, task.Request.DuplicateMode, id, processed))
		if _, err := copyStatement.ExecContext(ctx, values...); err != nil {
			copyStatement.Close()
			return 0, processed, fmt.Errorf("写入 PostgreSQL 暂存表第 %d 行失败: %w", processed, err)
		}
		m.updateExportProgress(id, started, processed, 0, false)
	}
	if _, err := copyStatement.ExecContext(ctx); err != nil {
		copyStatement.Close()
		return 0, processed, err
	}
	if err := copyStatement.Close(); err != nil {
		return 0, processed, err
	}
	quotedColumns := quoteIdentifiers(DBTypePostgres, tempColumns)
	insertSQL := "INSERT INTO " + target + " (" + strings.Join(quotedColumns, ", ") + ") SELECT " +
		strings.Join(quotedColumns, ", ") + " FROM " + quoteIdent(DBTypePostgres, tempName) +
		" ON CONFLICT (" + quoteIdent(DBTypePostgres, "source_row_hash") + ") DO NOTHING"
	result, err := tx.ExecContext(ctx, insertSQL)
	if err != nil {
		return 0, processed, err
	}
	inserted, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, processed, err
	}
	m.updateExportProgress(id, started, processed, inserted, true)
	return inserted, processed, nil
}

func (m *ExportManager) exportMySQL(
	ctx context.Context,
	id string,
	db *sql.DB,
	ref TableRef,
	headers []string,
	dbColumns []string,
	reader *csv.Reader,
) (int64, int64, error) {
	task, _ := m.Get(id)
	target := qualifiedTable(DBTypeMySQL, ref)
	if task.Request.Mode == "replace" {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+target); err != nil {
			return 0, 0, err
		}
	}
	if _, err := db.ExecContext(ctx, createExportTableSQL(DBTypeMySQL, target, headers, dbColumns)); err != nil {
		return 0, 0, err
	}
	if err := ensureExportTableColumns(ctx, db, DBTypeMySQL, ref, headers, dbColumns); err != nil {
		return 0, 0, err
	}
	allColumns := append(append([]string{}, dbColumns...), "source_job_id", "source_row_hash")
	started := time.Now()
	var processed int64
	var inserted int64
	batch := make([][]any, 0, mysqlExportBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		prefix := "INSERT"
		if task.Request.DuplicateMode == "skip" {
			prefix = "INSERT IGNORE"
		}
		rowPlaceholders := "(" + strings.TrimSuffix(strings.Repeat("?,", len(allColumns)), ",") + ")"
		groups := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch)*len(allColumns))
		for _, row := range batch {
			groups = append(groups, rowPlaceholders)
			args = append(args, row...)
		}
		query := prefix + " INTO " + target + " (" + strings.Join(quoteIdentifiers(DBTypeMySQL, allColumns), ", ") + ") VALUES " + strings.Join(groups, ",")
		result, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		inserted += count
		batch = batch[:0]
		m.updateExportProgress(id, started, processed, inserted, true)
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return inserted, processed, err
		}
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return inserted, processed, fmt.Errorf("读取 CSV 第 %d 行失败: %w", processed+2, readErr)
		}
		record = normalizeCSVRecord(record, len(headers))
		processed++
		values := exportDBValues(headers, record)
		values = append(values, task.Request.JobID, exportRowHash(record, task.Request.DuplicateMode, id, processed))
		batch = append(batch, values)
		if len(batch) >= mysqlExportBatchSize {
			if err := flush(); err != nil {
				return inserted, processed, fmt.Errorf("批量写入 MySQL 失败: %w", err)
			}
		}
	}
	if err := flush(); err != nil {
		return inserted, processed, fmt.Errorf("批量写入 MySQL 失败: %w", err)
	}
	return inserted, processed, nil
}

func (m *ExportManager) updateExportProgress(id string, started time.Time, processed, inserted int64, committed bool) {
	if processed > 1 && processed%2000 != 0 && inserted == 0 {
		return
	}
	elapsed := time.Since(started).Seconds()
	speed := 0.0
	if elapsed > 0 {
		speed = float64(processed) / elapsed
	}
	m.mu.Lock()
	task := m.tasks[id]
	if task != nil {
		task.Progress.ProcessedRows = processed
		task.Progress.InsertedRows = inserted
		if committed {
			task.Progress.SkippedRows = maxInt64(0, processed-inserted)
		}
		task.Progress.ElapsedSeconds = elapsed
		task.Progress.SpeedRowsPerSecond = speed
		if speed > 0 && task.Progress.TotalRows > processed {
			task.Progress.ETASeconds = float64(task.Progress.TotalRows-processed) / speed
		} else {
			task.Progress.ETASeconds = 0
		}
	}
	m.mu.Unlock()
}

func validateExportRequest(request *ExportRequest) error {
	request.JobID = strings.TrimSpace(request.JobID)
	request.ConnectionID = strings.TrimSpace(request.ConnectionID)
	request.Database = strings.TrimSpace(request.Database)
	request.Schema = strings.TrimSpace(request.Schema)
	request.Table = strings.TrimSpace(request.Table)
	if request.JobID == "" || request.ConnectionID == "" || request.Database == "" || request.Table == "" {
		return fmt.Errorf("任务、数据库连接、数据库和目标表均不能为空")
	}
	for label, value := range map[string]string{"数据库": request.Database, "Schema": request.Schema, "数据表": request.Table} {
		if value != "" && (len(value) > 128 || strings.ContainsAny(value, "\x00\r\n")) {
			return fmt.Errorf("%s名称无效", label)
		}
	}
	if request.Mode == "" {
		request.Mode = "append"
	}
	if request.Mode != "append" && request.Mode != "replace" {
		return fmt.Errorf("建表模式仅支持 append 或 replace")
	}
	if request.ColumnNaming == "" {
		request.ColumnNaming = "snake_case"
	}
	if request.ColumnNaming != "snake_case" && request.ColumnNaming != "original" {
		return fmt.Errorf("字段命名仅支持 snake_case 或 original")
	}
	if request.DuplicateMode == "" {
		request.DuplicateMode = "skip"
	}
	if request.DuplicateMode != "skip" && request.DuplicateMode != "allow" {
		return fmt.Errorf("重复处理仅支持 skip 或 allow")
	}
	if request.Schema == "" {
		request.Schema = "public"
	}
	return nil
}

func createExportTableSQL(dbType DBType, target string, headers, columns []string) string {
	definitions := []string{}
	if dbType == DBTypePostgres {
		definitions = append(definitions, quoteIdent(dbType, "id")+" bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY")
	} else {
		definitions = append(definitions, quoteIdent(dbType, "id")+" bigint NOT NULL AUTO_INCREMENT PRIMARY KEY")
	}
	definitions = append(definitions, exportColumnDefinitions(dbType, headers, columns)...)
	if dbType == DBTypePostgres {
		definitions = append(definitions,
			quoteIdent(dbType, "source_job_id")+" text NOT NULL",
			quoteIdent(dbType, "source_row_hash")+" char(64) NOT NULL UNIQUE",
			quoteIdent(dbType, "imported_at")+" timestamptz NOT NULL DEFAULT now()",
		)
	} else {
		definitions = append(definitions,
			quoteIdent(dbType, "source_job_id")+" varchar(191) NOT NULL",
			quoteIdent(dbType, "source_row_hash")+" char(64) NOT NULL UNIQUE",
			quoteIdent(dbType, "imported_at")+" timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)",
		)
	}
	return "CREATE TABLE IF NOT EXISTS " + target + " (" + strings.Join(definitions, ", ") + ")"
}

type exportSchemaExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ensureExportTableColumns(
	ctx context.Context,
	executor exportSchemaExecutor,
	dbType DBType,
	ref TableRef,
	headers, columns []string,
) error {
	namespace := ref.Database
	query := "SELECT column_name FROM information_schema.columns WHERE table_schema = ? AND table_name = ?"
	args := []any{namespace, ref.Table}
	if dbType == DBTypePostgres {
		namespace = ref.Schema
		if namespace == "" {
			namespace = "public"
		}
		query = "SELECT column_name FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2"
		args = []any{namespace, ref.Table}
	}
	rows, err := executor.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("读取目标表字段失败: %w", err)
	}
	existing := make(map[string]bool)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			rows.Close()
			return fmt.Errorf("读取目标表字段失败: %w", err)
		}
		existing[column] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭目标表字段查询失败: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("读取目标表字段失败: %w", err)
	}

	definitions := exportColumnDefinitions(dbType, headers, columns)
	target := qualifiedTable(dbType, ref)
	for index, column := range columns {
		if existing[column] {
			continue
		}
		if _, err := executor.ExecContext(ctx, "ALTER TABLE "+target+" ADD COLUMN "+definitions[index]); err != nil {
			return fmt.Errorf("新增目标表字段 %s 失败: %w", column, err)
		}
	}
	return nil
}

func exportColumnDefinitions(dbType DBType, headers, columns []string) []string {
	definitions := make([]string, 0, len(columns))
	for index, column := range columns {
		header := ""
		if index < len(headers) {
			header = headers[index]
		}
		dataType := exportColumnType(dbType, header)
		definitions = append(definitions, quoteIdent(dbType, column)+" "+dataType)
	}
	return definitions
}

func exportColumnType(dbType DBType, header string) string {
	switch header {
	case "交易金额", "交易余额", "对手交易余额":
		return "decimal(20,2)"
	case "交易时间":
		if dbType == DBTypePostgres {
			return "timestamp"
		}
		return "datetime(6)"
	default:
		if dbType == DBTypePostgres {
			return "text"
		}
		return "longtext"
	}
}

var snakeCaseExportColumns = map[string]string{
	"交易卡号": "transaction_card_no", "交易账号": "transaction_account", "交易户名": "transaction_name",
	"交易证件号码": "transaction_id_no", "交易方开户行": "transaction_bank", "账户性质": "account_type",
	"交易时间": "transaction_time", "交易金额": "transaction_amount", "交易余额": "transaction_balance",
	"收付标志": "direction", "交易对手账卡号": "counterparty_account", "对手账户性质": "counterparty_account_type",
	"现金标志": "cash_flag", "对手户名": "counterparty_name", "对手身份证号": "counterparty_id_no",
	"对手开户银行": "counterparty_bank", "摘要说明": "summary", "交易币种": "currency",
	"交易网点名称": "branch_name", "交易发生地": "location", "交易是否成功": "success_status",
	"传票号": "voucher_ticket_no", "IP地址": "ip_address", "MAC地址": "mac_address",
	"对手交易余额": "counterparty_balance", "交易流水号": "transaction_serial_no",
	"商户流水号": "merchant_serial_no", "日志号": "log_no",
	"凭证种类": "credential_type", "凭证号": "credential_no", "交易柜员号": "teller_no",
	"备注": "remark", "查询反馈结果原因": "feedback_reason", "数据来源": "data_source",
}

func exportColumnNames(headers []string, mode string) []string {
	columns := make([]string, len(headers))
	used := make(map[string]int, len(headers))
	for index, header := range headers {
		name := strings.TrimSpace(strings.TrimPrefix(header, "\ufeff"))
		if mode == "snake_case" {
			if mapped := snakeCaseExportColumns[name]; mapped != "" {
				name = mapped
			} else {
				name = "field_" + strconv.Itoa(index+1)
			}
		}
		used[name]++
		if used[name] > 1 {
			name += "_" + strconv.Itoa(used[name])
		}
		columns[index] = name
	}
	return columns
}

func exportDBValues(headers, record []string) []any {
	values := make([]any, len(record))
	for index, value := range record {
		value = strings.TrimSpace(value)
		if value == "" {
			values[index] = nil
			continue
		}
		header := ""
		if index < len(headers) {
			header = strings.TrimSpace(strings.TrimPrefix(headers[index], "\ufeff"))
		}
		if header == "交易金额" || header == "交易余额" || header == "对手交易余额" {
			values[index] = strings.ReplaceAll(value, ",", "")
		} else {
			values[index] = value
		}
	}
	return values
}

func normalizeCSVRecord(record []string, size int) []string {
	values := make([]string, size)
	copy(values, record)
	return values
}

func exportRowHash(record []string, duplicateMode, taskID string, row int64) string {
	hash := sha256.New()
	for _, value := range record {
		hash.Write([]byte(strconv.Itoa(len(value))))
		hash.Write([]byte{':'})
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	if duplicateMode == "allow" {
		hash.Write([]byte(taskID))
		hash.Write([]byte(strconv.FormatInt(row, 10)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func quoteIdentifiers(dbType DBType, values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = quoteIdent(dbType, value)
	}
	return result
}

func cloneExportTask(task *ExportTask) ExportTask {
	return *task
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
