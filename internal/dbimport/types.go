package dbimport

import "time"

type DBType string

const (
	DBTypeMySQL     DBType = "mysql"
	DBTypePostgres  DBType = "postgresql"
	DefaultPageSize        = 100
	MaxPageSize            = 1000
	MaxImportRows          = 100000
)

type Connection struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Type            DBType    `json:"type"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	DefaultDatabase string    `json:"defaultDatabase,omitempty"`
	Username        string    `json:"username"`
	Password        string    `json:"password,omitempty"`
	SavePassword    bool      `json:"savePassword"`
	SSL             bool      `json:"ssl"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	Group           string    `json:"group,omitempty"`
	Remark          string    `json:"remark,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type PublicConnection struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Type            DBType    `json:"type"`
	Host            string    `json:"host"`
	Port            int       `json:"port"`
	DefaultDatabase string    `json:"defaultDatabase,omitempty"`
	Username        string    `json:"username"`
	SavePassword    bool      `json:"savePassword"`
	HasPassword     bool      `json:"hasPassword"`
	SSL             bool      `json:"ssl"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	Group           string    `json:"group,omitempty"`
	Remark          string    `json:"remark,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type ColumnInfo struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default,omitempty"`
	PrimaryKey bool   `json:"primaryKey"`
	Indexed    bool   `json:"indexed"`
	Comment    string `json:"comment,omitempty"`
	Length     string `json:"length,omitempty"`
	Precision  string `json:"precision,omitempty"`
}

type TableRef struct {
	ConnectionID string `json:"connectionId"`
	Database     string `json:"database"`
	Schema       string `json:"schema,omitempty"`
	Table        string `json:"table"`
}

type TableDataRequest struct {
	TableRef
	Page          int               `json:"page"`
	PageSize      int               `json:"pageSize"`
	Search        string            `json:"search,omitempty"`
	SearchColumns []string          `json:"searchColumns,omitempty"`
	CaseSensitive bool              `json:"caseSensitive"`
	Filters       []AdvancedFilter  `json:"filters,omitempty"`
	OrderBy       string            `json:"orderBy,omitempty"`
	Descending    bool              `json:"descending"`
	Values        map[string]string `json:"values,omitempty"`
}

type TableEditRequest struct {
	TableRef
	Values map[string]interface{} `json:"values"`
	Keys   map[string]interface{} `json:"keys,omitempty"`
}

type TableEditResponse struct {
	AffectedRows int64 `json:"affectedRows"`
}

type AdvancedFilter struct {
	Column   string        `json:"column"`
	Operator string        `json:"operator"`
	Value    interface{}   `json:"value,omitempty"`
	Values   []interface{} `json:"values,omitempty"`
	Logic    string        `json:"logic,omitempty"`
}

type QueryRequest struct {
	ConnectionID string `json:"connectionId"`
	Database     string `json:"database"`
	Schema       string `json:"schema,omitempty"`
	SQL          string `json:"sql"`
	Page         int    `json:"page"`
	PageSize     int    `json:"pageSize"`
	AllowWrite   bool   `json:"allowWrite"`
}

type TableDataResponse struct {
	Columns       []ColumnInfo             `json:"columns"`
	Rows          []map[string]interface{} `json:"rows"`
	Page          int                      `json:"page"`
	PageSize      int                      `json:"pageSize"`
	ReturnedRows  int                      `json:"returnedRows"`
	EstimatedRows int64                    `json:"estimatedRows,omitempty"`
	Truncated     bool                     `json:"truncated"`
	ElapsedMs     int64                    `json:"elapsedMs"`
}

type MappingRule struct {
	ID                string         `json:"id"`
	ConnectionType    DBType         `json:"connectionType"`
	ConnectionID      string         `json:"connectionId"`
	Database          string         `json:"database"`
	Schema            string         `json:"schema,omitempty"`
	Table             string         `json:"table"`
	SourceColumnsHash string         `json:"sourceColumnsHash"`
	TargetVersion     string         `json:"targetVersion"`
	Mappings          []FieldMapping `json:"mappings"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}

type FieldMapping struct {
	SourceColumn string `json:"sourceColumn"`
	SourceType   string `json:"sourceType,omitempty"`
	TargetField  string `json:"targetField"`
	TargetType   string `json:"targetType,omitempty"`
	Transform    string `json:"transform,omitempty"`
	Required     bool   `json:"required"`
	Confidence   int    `json:"confidence"`
}

type ImportTask struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Tables    []ImportTable     `json:"tables"`
	Progress  ImportProgress    `json:"progress"`
	Errors    []ImportError     `json:"errors"`
	SessionID string            `json:"session_id,omitempty"`
	Columns   []string          `json:"columns,omitempty"`
	Files     []string          `json:"files,omitempty"`
	Sample    []map[string]any  `json:"sample,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Options   map[string]string `json:"options,omitempty"`
}

type ImportTable struct {
	TableRef
	Mappings []FieldMapping `json:"mappings"`
	Limit    int            `json:"limit,omitempty"`
}

type ImportProgress struct {
	TotalRows          int64   `json:"totalRows"`
	ProcessedRows      int64   `json:"processedRows"`
	SuccessRows        int64   `json:"successRows"`
	FailedRows         int64   `json:"failedRows"`
	SkippedRows        int64   `json:"skippedRows"`
	SpeedRowsPerSecond float64 `json:"speedRowsPerSecond"`
}

type ImportError struct {
	Table     string    `json:"table"`
	Row       int64     `json:"row"`
	Field     string    `json:"field,omitempty"`
	Reason    string    `json:"reason"`
	Snapshot  string    `json:"snapshot,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type ImportTaskRequest struct {
	Name   string        `json:"name"`
	Tables []ImportTable `json:"tables"`
}
