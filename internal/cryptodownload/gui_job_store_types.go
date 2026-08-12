package cryptodownload

const guiJobStoreVersion = 1

type GUIJobPersistedRequest struct {
	Source           string  `json:"source"`
	RPCURL           string  `json:"rpcUrl,omitempty"`
	Chains           string  `json:"chains,omitempty"`
	NativeSymbol     string  `json:"nativeSymbol,omitempty"`
	CSVEmail         string  `json:"csvEmail,omitempty"`
	CSVIMAPHost      string  `json:"csvImapHost,omitempty"`
	CSVIMAPPort      int     `json:"csvImapPort,omitempty"`
	CSVIMAPUser      string  `json:"csvImapUser,omitempty"`
	CSVDeliveryMode  string  `json:"csvDeliveryMode,omitempty"`
	CSVStartTime     int64   `json:"csvStartTime,omitempty"`
	CSVEndTime       int64   `json:"csvEndTime,omitempty"`
	StartBlock       int64   `json:"startBlock,omitempty"`
	EndBlock         int64   `json:"endBlock,omitempty"`
	CutoffBlock      int64   `json:"cutoffBlock,omitempty"`
	TraceMode        string  `json:"traceMode,omitempty"`
	BlockBatch       uint64  `json:"blockBatch,omitempty"`
	LogBatch         uint64  `json:"logBatch,omitempty"`
	Workers          int     `json:"workers,omitempty"`
	RPS              float64 `json:"rps,omitempty"`
	TimeoutSeconds   int     `json:"timeoutSeconds,omitempty"`
	Retries          int     `json:"retries,omitempty"`
	PageSize         int     `json:"pageSize,omitempty"`
	RawDir           string  `json:"rawDir,omitempty"`
	OutputDir        string  `json:"outputDir,omitempty"`
	OutputPrefix     string  `json:"outputPrefix,omitempty"`
	AMLLabels        bool    `json:"amlLabels,omitempty"`
	AMLRPS           float64 `json:"amlRps,omitempty"`
	FilterExchange   bool    `json:"filterExchange,omitempty"`
	Details          bool    `json:"details,omitempty"`
	ScanNative       bool    `json:"scanNative,omitempty"`
	Incremental      bool    `json:"incremental,omitempty"`
	RiskCooldownSecs int     `json:"riskCooldownSecs,omitempty"`
}

type GUIJobCheckpointSummary struct {
	Address  string `json:"address"`
	Chain    string `json:"chain"`
	Complete bool   `json:"complete"`
	Segments int    `json:"segments"`
	Rows     int64  `json:"rows"`
}

type GUIJobRecord struct {
	Version           int                       `json:"version"`
	ID                string                    `json:"id"`
	Status            string                    `json:"status"`
	Message           string                    `json:"message"`
	Progress          int                       `json:"progress"`
	Done              int                       `json:"done"`
	Total             int                       `json:"total"`
	Running           bool                      `json:"running"`
	NeedsCredentials  bool                      `json:"needsCredentials"`
	StartedAt         string                    `json:"startedAt"`
	FinishedAt        string                    `json:"finishedAt"`
	TaskDir           string                    `json:"taskDir,omitempty"`
	Request           GUIJobPersistedRequest    `json:"request"`
	Entries           []GUIAddressChain         `json:"entries"`
	Addresses         []GUIAddressProgress      `json:"addresses"`
	CheckpointSummary []GUIJobCheckpointSummary `json:"checkpointSummaries"`
	CooldownUntil     string                    `json:"cooldownUntil,omitempty"`
}
