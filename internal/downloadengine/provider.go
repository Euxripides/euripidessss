package downloadengine

import "context"

// ── Provider 能力接口（组合优于继承）──
// 不强制所有 Provider 实现同一种 Execute 接口。
// Provider 声明自己支持哪些能力，Router 按需调用。

// StreamingProvider 支持流式批量查询（如 SQD 分页流）
type StreamingProvider interface {
	Provider
	Name() string
	Capabilities() ProviderCapabilities
	Health(ctx context.Context) ProviderHealth
	Estimate(ctx context.Context, req StreamEstimateRequest) (*EstimateResult, error)
	ExecuteStream(ctx context.Context, req StreamRequest) (<-chan StreamRecord, <-chan error)
}

// ObjectProvider 支持对象存储下载（如 AWS S3 Parquet）
type ObjectProvider interface {
	Provider
	Name() string
	Capabilities() ProviderCapabilities
	Health(ctx context.Context) ProviderHealth
	Estimate(ctx context.Context, req ObjectEstimateRequest) (*EstimateResult, error)
	ExecuteObject(ctx context.Context, req ObjectRequest) (*ObjectResult, error)
}

// LookupProvider 支持单点查询（如 RPC 查询区块时间/合约代码）
type LookupProvider interface {
	Provider
	Name() string
	Capabilities() ProviderCapabilities
	Health(ctx context.Context) ProviderHealth
	ExecuteLookup(ctx context.Context, req LookupRequest) (*LookupResult, error)
}

// Provider 基础标识接口
type Provider interface {
	Name() string
	Capabilities() ProviderCapabilities
	Health(ctx context.Context) ProviderHealth
}

// ── 请求/响应 ──

type StreamEstimateRequest struct {
	ChainID     string
	Addresses   []string
	DatasetType string
	StartBlock  uint64
	EndBlock    uint64
}

type ObjectEstimateRequest struct {
	ChainID     string
	StartDate   string
	EndDate     string
	DatasetType string
}

type StreamRequest struct {
	ChainID     string
	Addresses   []string
	DatasetType string
	StartBlock  uint64
	EndBlock    uint64
	ChunkSize   uint64
}

type StreamRecord struct {
	Data map[string]any
}

type ObjectRequest struct {
	SourceURI string
	OutputDir string
}

type ObjectResult struct {
	OutputPath string
	RowCount   int64
	ByteCount  int64
}

type LookupRequest struct {
	ChainID     string
	Address     string
	BlockNumber *uint64
	LookupType  string // "block_time" | "contract_code" | "address_type"
}

type LookupResult struct {
	Found   bool
	Payload map[string]any
}

type EstimateResult struct {
	EstimatedRows   int64 `json:"estimated_rows"`
	EstimatedBytes  int64 `json:"estimated_bytes"`
	EstimatedChunks int   `json:"estimated_chunks"`
	SupportsRequest bool  `json:"supports_request"`
}
