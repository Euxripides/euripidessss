package cryptodownload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

const (
	defaultBaseURL        = "https://www.oklink.com"
	defaultChains         = "ETH,BSC,POLYGON,ARBITRUM,BASE,OP,AVAXC,FTM,LINEA,SCROLL,OPBNB,XLAYER"
	defaultProtocols      = "transaction,internal,token_20,token_721,token_1155,token_10"
	defaultTokenProtocols = "token_20,token_721,token_1155,token_10"
)

var (
	transactionHeaders = []Column{
		{"目标地址", "targetAddress"},
		{"链全称", "chainFullName"},
		{"链简称", "chainShortName"},
		{"协议类型", "protocolType"},
		{"方向", "direction"},
		{"交易哈希", "txId"},
		{"方法ID", "methodId"},
		{"区块哈希", "blockHash"},
		{"区块高度", "height"},
		{"交易时间(ms)", "transactionTime"},
		{"交易时间", "transactionTimeLocal"},
		{"From", "from"},
		{"From标签", "fromLabelName"},
		{"From标签类型", "fromLabelTypes"},
		{"To", "to"},
		{"To标签", "toLabelName"},
		{"To标签类型", "toLabelTypes"},
		{"对手方", "counterparty"},
		{"对手方标签", "counterpartyLabelName"},
		{"对手方标签类型", "counterpartyLabelTypes"},
		{"From是否合约", "isFromContract"},
		{"To是否合约", "isToContract"},
		{"金额", "amount"},
		{"币种", "transactionSymbol"},
		{"手续费", "txFee"},
		{"状态", "state"},
		{"Token ID", "tokenId"},
		{"代币合约", "tokenContractAddress"},
		{"Challenge状态", "challengeStatus"},
		{"L1来源哈希", "l1OriginHash"},
		{"inputdate", "inputdate"},
		{"logs", "logs"},
		{"原始JSON", "rawJSON"},
	}

	fundHeaders = []Column{
		{"目标地址", "targetAddress"},
		{"链简称", "chainShortName"},
		{"交易类型", "protocolType"},
		{"方向", "direction"},
		{"交易时间", "transactionTimeLocal"},
		{"交易哈希", "txId"},
		{"资产", "asset"},
		{"金额", "amount"},
		{"From", "from"},
		{"To", "to"},
		{"对手方", "counterparty"},
		{"对手方标签", "counterpartyLabelName"},
		{"对手方标签类型", "counterpartyLabelTypes"},
		{"手续费", "txFee"},
		{"状态", "state"},
		{"区块高度", "height"},
		{"代币合约", "tokenContractAddress"},
		{"Token ID", "tokenId"},
		{"inputdate", "inputdate"},
		{"logs", "logs"},
	}

	summaryHeaders = []Column{
		{"目标地址", "address"},
		{"地址标签", "addressLabelName"},
		{"地址标签类型", "addressLabelTypes"},
		{"链全称", "chainFullName"},
		{"链简称", "chainShortName"},
		{"原生余额", "balance"},
		{"原生币种", "balanceSymbol"},
		{"交易数", "transactionCount"},
		{"转出总额", "sendAmount"},
		{"转入总额", "receiveAmount"},
		{"代币数量", "tokenAmount"},
		{"代币总价值USD", "totalTokenValue"},
		{"首次交易时间(ms)", "firstTransactionTime"},
		{"首次交易时间", "firstTransactionTimeLocal"},
		{"最后交易时间(ms)", "lastTransactionTime"},
		{"最后交易时间", "lastTransactionTimeLocal"},
		{"合约地址", "contractAddress"},
		{"创建合约地址", "createContractAddress"},
		{"创建合约交易", "createContractTransactionHash"},
		{"TRON带宽", "bandwidth"},
		{"TRON能量", "energy"},
		{"投票权", "votingRights"},
		{"待领取投票奖励", "unclaimedVotingRewards"},
		{"AA地址", "isAaAddress"},
		{"普通交易下载数", "count_transaction"},
		{"内部交易下载数", "count_internal"},
		{"代币转账下载数", "count_token"},
		{"NFT转账下载数", "count_nft"},
		{"资产下载数", "count_assets"},
		{"下载状态", "downloadStatus"},
		{"下载错误数", "downloadErrorCount"},
		{"DeepAML过滤交易所行数", "aml_filtered_exchange_rows"},
		{"导出时间", "exportedAt"},
		{"原始JSON", "rawJSON"},
	}

	assetHeaders = []Column{
		{"目标地址", "address"},
		{"地址标签", "addressLabelName"},
		{"地址标签类型", "addressLabelTypes"},
		{"链全称", "chainFullName"},
		{"链简称", "chainShortName"},
		{"资产类型", "assetType"},
		{"协议类型", "protocolType"},
		{"币种", "symbol"},
		{"持仓数量", "holdingAmount"},
		{"价格USD", "priceUsd"},
		{"价值USD", "valueUsd"},
		{"代币合约", "tokenContractAddress"},
		{"Token ID", "tokenId"},
		{"原始JSON", "rawJSON"},
	}
)

type Config struct {
	Address         string
	Chains          []string
	Protocols       []string
	APIKey          string
	BaseURL         string
	Source          string
	CSVEmail        string
	CSVIMAPHost     string
	CSVIMAPPort     int
	CSVIMAPUser     string
	CSVIMAPPassword string
	CSVDeliveryMode string
	CSVStartTime    int64
	CSVEndTime      int64
	CSVRequestHAR   string
	AMLAPIKey       string
	AMLBaseURL      string
	AMLLabels       bool
	AMLRPS          float64
	FilterExchange  bool
	RPCURL          string
	RPCConfig       string
	RPCFallbacks    []string
	Out             string
	RawDir          string
	Workers         int
	RPS             float64
	PageSize        int
	Timeout         time.Duration
	MaxDuration     time.Duration
	Retries         int
	Details         bool
	ScanNative      bool
	StartBlock      int64
	EndBlock        int64
	CutoffBlock     int64
	BlockBatch      uint64
	LogBatch        uint64
	TraceMode       string
	NativeSymbol    string
	GUI             bool
	GUIPort         int
	Progress        func(string)
}

type Column struct {
	Title string
	Key   string
}

type APIResponse struct {
	Code string            `json:"code"`
	Msg  string            `json:"msg"`
	Data []json.RawMessage `json:"data"`
}

type PageData struct {
	Page             string           `json:"page"`
	Limit            string           `json:"limit"`
	TotalPage        string           `json:"totalPage"`
	ChainFullName    string           `json:"chainFullName"`
	ChainShortName   string           `json:"chainShortName"`
	TransactionLists []map[string]any `json:"transactionLists"`
	TokenList        []map[string]any `json:"tokenList"`
}

type ExportData struct {
	Summaries         []map[string]any
	Transactions      []map[string]any
	Internals         []map[string]any
	TokenTransfers    []map[string]any
	NFTTransfers      []map[string]any
	Funds             []map[string]any
	Assets            []map[string]any
	Errors            []string
	RawTransactions   []map[string]string
	RawTokenTransfers []map[string]string
	RawTxHeaders      []string
	RawTokenHeaders   []string
	CSVDownloadChecks []CSVDownloadCheck
}

type CSVDownloadCheck struct {
	Address          string
	Chain            string
	Kind             string
	ExpectedTotal    int64
	Downloaded       int
	DirectDownloaded int
	EmailDownloaded  int
	Status           string
	Note             string
}

type OKLinkClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *RateLimiter
	sem        chan struct{}
	retries    int
	rawDir     string
}

type RateLimiter struct {
	ch chan struct{}
}

func NewRateLimiter(rps float64) *RateLimiter {
	if rps <= 0 {
		return nil
	}
	capacity := int(math.Ceil(rps))
	if capacity < 1 {
		capacity = 1
	}
	interval := time.Duration(float64(time.Second) / rps)
	if interval <= 0 {
		interval = time.Millisecond
	}
	rl := &RateLimiter{ch: make(chan struct{}, capacity)}
	for i := 0; i < capacity; i++ {
		rl.ch <- struct{}{}
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case rl.ch <- struct{}{}:
			default:
			}
		}
	}()
	return rl
}

func (r *RateLimiter) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ch:
		return nil
	}
}

func main() {
	if len(os.Args) == 1 {
		if err := startGUI(8787); err != nil {
			fmt.Fprintln(os.Stderr, "启动界面失败:", err)
			os.Exit(1)
		}
		return
	}
	cfg := parseFlags()
	if cfg.GUI {
		if err := startGUI(cfg.GUIPort); err != nil {
			fmt.Fprintln(os.Stderr, "启动界面失败:", err)
			os.Exit(1)
		}
		return
	}
	if err := validateConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(2)
	}

	ctx := context.Background()
	if cfg.MaxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.MaxDuration)
		defer cancel()
	}
	start := time.Now()
	fmt.Printf("开始下载 source=%s address=%s chains=%s protocols=%s workers=%d rps=%.2f\n",
		cfg.Source, cfg.Address, strings.Join(cfg.Chains, ","), strings.Join(cfg.Protocols, ","), cfg.Workers, cfg.RPS)
	if cfg.Progress == nil {
		cfg.Progress = func(msg string) {
			fmt.Println(time.Now().Format("15:04:05"), msg)
		}
	}

	data := collectForSource(ctx, cfg)
	if cfg.AMLLabels && cfg.AMLAPIKey != "" {
		if err := applyAMLLabelsAndFilter(ctx, cfg, &data); err != nil {
			data.Errors = append(data.Errors, err.Error())
		}
		fillSummaryCounters(&data)
		sortExportData(&data)
	}
	source := strings.ToLower(strings.TrimSpace(cfg.Source))
	if source == "csv" && len(data.RawTransactions) == 0 && len(data.RawTokenTransfers) == 0 &&
		(len(data.Transactions) > 0 || len(data.TokenTransfers) > 0) {
		reportProgress(cfg, "CSV download blocked by OKLink, switching to browser API output format.")
		source = "browser-fallback"
	}
	if source == "csv" {
		if err := writeCSVWorkbook(cfg, data); err != nil {
			fmt.Fprintln(os.Stderr, "写入 Excel 失败:", err)
			os.Exit(1)
		}
	} else {
		if err := writeWorkbook(cfg, data); err != nil {
			fmt.Fprintln(os.Stderr, "写入 Excel 失败:", err)
			os.Exit(1)
		}
	}

	if err := ctx.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 下载超时（%v），数据可能不完整。已导出已下载的部分数据。\n", cfg.MaxDuration)
	}

	fmt.Printf("完成: %s\n", cfg.Out)
	fmt.Printf("统计: 交易=%d 内部交易=%d 代币转账=%d NFT转账=%d 资产=%d 错误=%d 耗时=%s\n",
		len(data.Transactions), len(data.Internals), len(data.TokenTransfers), len(data.NFTTransfers), len(data.Assets), len(data.Errors), time.Since(start).Round(time.Second))
	if len(data.Errors) > 0 {
		if strings.ToLower(strings.TrimSpace(cfg.Source)) == "csv" {
			fmt.Println("部分任务失败，详情已写入“交易”sheet末尾。")
		} else {
			fmt.Println("部分任务失败，详情已写入“统计数据”sheet末尾。")
		}
	}
}

func parseFlags() Config {
	var (
		address        = flag.String("address", "", "要导出的地址")
		chains         = flag.String("chains", defaultChains, "链简称，逗号分隔，例如 ETH,BSC,POLYGON,BASE")
		protocols      = flag.String("protocols", defaultProtocols, "交易协议类型，逗号分隔：transaction,internal,token_20,token_721,token_1155,token_10")
		apiKey         = flag.String("api-key", os.Getenv("OKLINK_API_KEY"), "OKLink API Key，也可使用环境变量 OKLINK_API_KEY")
		baseURL        = flag.String("base-url", defaultBaseURL, "OKLink API base URL")
		csvEmail       = flag.String("csv-email", os.Getenv("CSV_EMAIL"), "CSV 下载接收邮箱")
		csvIMAPHost    = flag.String("csv-imap-host", os.Getenv("CSV_IMAP_HOST"), "CSV 下载邮箱 IMAP 服务器")
		csvIMAPPort    = flag.Int("csv-imap-port", envInt("CSV_IMAP_PORT", 993), "CSV 下载邮箱 IMAP 端口")
		csvIMAPUser    = flag.String("csv-imap-user", os.Getenv("CSV_IMAP_USER"), "CSV 下载邮箱 IMAP 用户名")
		csvIMAPPass    = flag.String("csv-imap-password", os.Getenv("CSV_IMAP_PASSWORD"), "CSV 下载邮箱 IMAP 密码或授权码")
		csvStartTime   = flag.Int64("csv-start-time", 0, "CSV 模式开始时间 Unix 秒，0 表示尽可能早")
		csvEndTime     = flag.Int64("csv-end-time", 0, "CSV 模式结束时间 Unix 秒，0 表示当前时间")
		csvRequestHAR  = flag.String("csv-request-har", os.Getenv("OKLINK_CSV_REQUEST_HAR"), "CSV 邮箱下载浏览器 HAR 请求文件，用于复用 OKLink 签名头")
		source         = flag.String("source", "rpc", "数据源：rpc、csv 或 browser")
		amlKey         = flag.String("aml-key", os.Getenv("DEEPAML_API_KEY"), "DeepAML API Key，也可使用环境变量 DEEPAML_API_KEY")
		amlBaseURL     = flag.String("aml-base-url", "https://openapi.deepaml.io", "DeepAML API base URL")
		amlLabels      = flag.Bool("aml-labels", true, "使用 DeepAML 为地址添加标签")
		amlRPS         = flag.Float64("aml-rps", 2, "DeepAML 标签接口请求速率限制")
		filterExchange = flag.Bool("filter-exchange", true, "过滤对手方标签为 EXCHANGE 的交易所大地址交易")
		rpcURL         = flag.String("rpc-url", os.Getenv("RPC_URL"), "EVM JSON-RPC 节点地址，也可使用环境变量 RPC_URL")
		rpcConfig      = flag.String("rpc-config", "", "多链 RPC 配置 JSON 文件")
		out            = flag.String("out", "wallet_export.xlsx", "输出 Excel 文件")
		rawDir         = flag.String("raw-dir", "raw", "保存接口原始 JSON 的目录；设为空字符串可关闭")
		workers        = flag.Int("workers", 16, "并发 HTTP 请求数")
		rps            = flag.Float64("rps", 3, "全局请求速率限制，0 表示不限速；免费 API 建议 3")
		pageSize       = flag.Int("page-size", 50, "分页大小，OKLink 通常最大 50")
		timeout        = flag.Duration("timeout", 30*time.Second, "单次 HTTP 请求超时")
		retries        = flag.Int("retries", 4, "失败重试次数")
		details        = flag.Bool("details", true, "下载交易详情以补充 inputdate/logs")
		scanNative     = flag.Bool("scan-native", true, "RPC 模式扫描普通原生交易；公共 RPC 全量扫描会很慢")
		startBlock     = flag.Int64("start-block", 0, "RPC 模式扫描起始区块")
		endBlock       = flag.Int64("end-block", -1, "RPC 模式扫描结束区块，-1 表示最新区块")
		cutoffBlock    = flag.Int64("cutoff-block", 0, "RPC 模式截止区块（不包含），例如 100 表示只下载小于 100 的数据")
		blockBatch     = flag.Uint64("block-batch", 100, "RPC 模式区块扫描批次大小")
		logBatch       = flag.Uint64("log-batch", 50, "RPC 模式 eth_getLogs 区块批次大小")
		traceMode      = flag.String("trace-mode", "auto", "RPC 内部交易模式：auto、trace-filter、debug-all、none")
		nativeSymbol   = flag.String("native-symbol", "", "RPC 单链模式原生币符号，例如 ETH、BNB、MATIC")
		gui            = flag.Bool("gui", false, "启动本地可视化界面")
		guiPort        = flag.Int("gui-port", 8787, "本地可视化界面端口")
		maxDuration    = flag.Duration("max-duration", 0, "最大运行时长；默认不限时，设为正值可启用受控截止")
	)
	flag.Parse()
	if *csvStartTime <= 0 {
		*csvStartTime = defaultCSVStartTime
	}

	return normalizeCSVMailConfig(Config{
		Address:         strings.TrimSpace(*address),
		Chains:          splitCSV(*chains),
		Protocols:       splitCSV(*protocols),
		APIKey:          strings.TrimSpace(*apiKey),
		BaseURL:         strings.TrimRight(strings.TrimSpace(*baseURL), "/"),
		Source:          strings.ToLower(strings.TrimSpace(*source)),
		CSVEmail:        strings.TrimSpace(*csvEmail),
		CSVIMAPHost:     strings.TrimSpace(*csvIMAPHost),
		CSVIMAPPort:     *csvIMAPPort,
		CSVIMAPUser:     strings.TrimSpace(*csvIMAPUser),
		CSVIMAPPassword: strings.TrimSpace(*csvIMAPPass),
		CSVStartTime:    *csvStartTime,
		CSVEndTime:      *csvEndTime,
		CSVRequestHAR:   strings.TrimSpace(*csvRequestHAR),
		AMLAPIKey:       strings.TrimSpace(*amlKey),
		AMLBaseURL:      strings.TrimRight(strings.TrimSpace(*amlBaseURL), "/"),
		AMLLabels:       *amlLabels,
		AMLRPS:          *amlRPS,
		FilterExchange:  *filterExchange,
		RPCURL:          strings.TrimSpace(*rpcURL),
		RPCConfig:       strings.TrimSpace(*rpcConfig),
		Out:             strings.TrimSpace(*out),
		RawDir:          strings.TrimSpace(*rawDir),
		Workers:         *workers,
		RPS:             *rps,
		PageSize:        *pageSize,
		Timeout:         *timeout,
		MaxDuration:     *maxDuration,
		Retries:         *retries,
		Details:         *details,
		ScanNative:      *scanNative,
		StartBlock:      *startBlock,
		EndBlock:        *endBlock,
		CutoffBlock:     *cutoffBlock,
		BlockBatch:      *blockBatch,
		LogBatch:        *logBatch,
		TraceMode:       strings.ToLower(strings.TrimSpace(*traceMode)),
		NativeSymbol:    strings.TrimSpace(*nativeSymbol),
		GUI:             *gui,
		GUIPort:         *guiPort,
	})
}

func normalizeCSVMailConfig(cfg Config) Config {
	email := strings.ToLower(strings.TrimSpace(cfg.CSVEmail))
	if !strings.HasSuffix(email, "@gmail.com") && !strings.HasSuffix(email, "@googlemail.com") {
		return cfg
	}
	host := strings.TrimSpace(cfg.CSVIMAPHost)
	if host == "" || strings.Contains(host, "@") {
		cfg.CSVIMAPHost = "imap.gmail.com"
	}
	if cfg.CSVIMAPPort <= 0 {
		cfg.CSVIMAPPort = 993
	}
	if strings.TrimSpace(cfg.CSVIMAPUser) == "" {
		cfg.CSVIMAPUser = strings.TrimSpace(cfg.CSVEmail)
	}
	return cfg
}

func validateConfig(cfg Config) error {
	if cfg.Address == "" {
		return errors.New("必须传 -address")
	}
	if cfg.Source == "" {
		return errors.New("source 不能为空")
	}
	if !supportedSource(cfg.Source) {
		return errors.New("source 只能是 rpc、csv 或 browser")
	}
	if cfg.Source == "rpc" && cfg.RPCURL == "" && cfg.RPCConfig == "" {
		return errors.New("RPC 模式必须传 -rpc-url、-rpc-config 或设置 RPC_URL")
	}
	if cfg.Source == "csv" {
		if cfg.CSVStartTime < 0 || cfg.CSVEndTime < 0 {
			return errors.New("CSV 时间必须 >= 0")
		}
		if cfg.CSVEndTime > 0 && cfg.CSVStartTime > cfg.CSVEndTime {
			return errors.New("CSV 结束时间必须 >= 开始时间")
		}
	}
	if cfg.BaseURL == "" {
		return errors.New("base-url 不能为空")
	}
	if len(cfg.Chains) == 0 {
		return errors.New("chains 不能为空")
	}
	if len(cfg.Protocols) == 0 {
		return errors.New("protocols 不能为空")
	}
	if cfg.Workers < 1 {
		return errors.New("workers 必须 >= 1")
	}
	if cfg.PageSize < 1 || cfg.PageSize > 100 {
		return errors.New("page-size 建议在 1-100 之间")
	}
	if cfg.Retries < 0 {
		return errors.New("retries 必须 >= 0")
	}
	if cfg.StartBlock < 0 {
		return errors.New("start-block 必须 >= 0")
	}
	if cfg.EndBlock >= 0 && cfg.EndBlock < cfg.StartBlock {
		return errors.New("end-block 必须 >= start-block")
	}
	if cfg.CutoffBlock < 0 {
		return errors.New("cutoff-block 必须 >= 0")
	}
	if cfg.BlockBatch < 1 {
		return errors.New("block-batch 必须 >= 1")
	}
	if cfg.LogBatch < 1 {
		return errors.New("log-batch 必须 >= 1")
	}
	switch cfg.TraceMode {
	case "auto", "trace-filter", "debug-all", "none":
	default:
		return errors.New("trace-mode 只能是 auto、trace-filter、debug-all、none")
	}
	return nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func tokenBalanceProtocols(protocols []string) []string {
	out := make([]string, 0, len(protocols)+4)
	seen := map[string]bool{}
	add := func(protocol string) {
		protocol = strings.TrimSpace(protocol)
		if protocol == "" {
			return
		}
		key := strings.ToLower(protocol)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, protocol)
	}
	for _, protocol := range splitCSV(defaultTokenProtocols) {
		add(protocol)
	}
	for _, protocol := range protocols {
		class := classifyProtocol(protocol)
		if class == "token" || class == "nft" {
			add(protocol)
		}
	}
	return out
}

func envInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func NewOKLinkClient(cfg Config) *OKLinkClient {
	return &OKLinkClient{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		limiter: NewRateLimiter(cfg.RPS),
		sem:     make(chan struct{}, cfg.Workers),
		retries: cfg.Retries,
		rawDir:  cfg.RawDir,
	}
}

func collectAll(ctx context.Context, client *OKLinkClient, cfg Config) ExportData {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		data ExportData
	)

	appendErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		data.Errors = append(data.Errors, err.Error())
		mu.Unlock()
		fmt.Fprintln(os.Stderr, err)
	}

	for _, chain := range cfg.Chains {
		chain := chain
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := client.FetchAddressSummary(ctx, cfg.Address, chain)
			if err != nil {
				appendErr(fmt.Errorf("summary %s: %w", chain, err))
				return
			}
			mu.Lock()
			data.Summaries = append(data.Summaries, row)
			mu.Unlock()
		}()

		for _, assetProtocol := range tokenBalanceProtocols(cfg.Protocols) {
			assetProtocol := assetProtocol
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, err := client.FetchTokenBalances(ctx, cfg.Address, chain, assetProtocol, cfg.PageSize)
				if err != nil {
					appendErr(fmt.Errorf("token-balance %s %s: %w", chain, assetProtocol, err))
				}
				if len(rows) > 0 {
					mu.Lock()
					data.Assets = append(data.Assets, rows...)
					mu.Unlock()
				}
			}()
		}

		for _, protocol := range cfg.Protocols {
			protocol := protocol
			wg.Add(1)
			go func() {
				defer wg.Done()
				rows, err := client.FetchTransactions(ctx, cfg.Address, chain, protocol, cfg.PageSize)
				if err != nil {
					appendErr(fmt.Errorf("transactions %s %s: %w", chain, protocol, err))
				}
				if len(rows) == 0 {
					return
				}
				for _, row := range rows {
					enrichTransaction(row, cfg.Address, chain, protocol)
				}
				if cfg.Details {
					if err := client.EnrichTransactionDetails(ctx, rows, chain); err != nil {
						appendErr(fmt.Errorf("transaction details %s %s: %w", chain, protocol, err))
					}
				}
				mu.Lock()
				switch classifyProtocol(protocol) {
				case "transaction":
					data.Transactions = append(data.Transactions, rows...)
				case "internal":
					data.Internals = append(data.Internals, rows...)
				case "nft":
					data.NFTTransfers = append(data.NFTTransfers, rows...)
				default:
					data.TokenTransfers = append(data.TokenTransfers, rows...)
				}
				data.Funds = append(data.Funds, buildFundRows(rows)...)
				mu.Unlock()
			}()
		}
	}

	wg.Wait()
	addNativeAssets(&data)
	fillSummaryCounters(&data)
	sortExportData(&data)
	return data
}

func (c *OKLinkClient) FetchAddressSummary(ctx context.Context, address, chain string) (map[string]any, error) {
	params := url.Values{}
	params.Set("chainShortName", chain)
	params.Set("address", address)
	raw, body, err := c.get(ctx, "/api/v5/explorer/address/address-summary", params)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{
			"address":        address,
			"chainShortName": chain,
			"rawJSON":        string(body),
		}, nil
	}
	row := map[string]any{}
	if err := json.Unmarshal(raw[0], &row); err != nil {
		return nil, err
	}
	row["address"] = firstNonEmpty(toString(row["address"]), address)
	row["chainShortName"] = firstNonEmpty(toString(row["chainShortName"]), chain)
	row["firstTransactionTimeLocal"] = formatUnixMilli(toString(row["firstTransactionTime"]))
	row["lastTransactionTimeLocal"] = formatUnixMilli(toString(row["lastTransactionTime"]))
	row["exportedAt"] = time.Now().Format("2006-01-02 15:04:05")
	row["rawJSON"] = compactJSON(raw[0])
	_ = c.writeRaw(chain, "summary", "", 0, body)
	return row, nil
}

func (c *OKLinkClient) FetchTokenBalances(ctx context.Context, address, chain, protocol string, limit int) ([]map[string]any, error) {
	first, totalPages, err := c.fetchPage(ctx, "/api/v5/explorer/address/token-balance", address, chain, protocol, 1, limit)
	if err != nil {
		return nil, err
	}
	rows := assetRowsFromPage(address, protocol, first)
	if totalPages <= 1 {
		return rows, nil
	}

	type result struct {
		rows []map[string]any
		err  error
	}
	resultCh := make(chan result, totalPages-1)
	jobs := make(chan int)

	workerCount := cap(c.sem)
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > totalPages-1 {
		workerCount = totalPages - 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for page := range jobs {
				p, _, err := c.fetchPage(ctx, "/api/v5/explorer/address/token-balance", address, chain, protocol, page, limit)
				if err != nil {
					resultCh <- result{err: err}
					continue
				}
				resultCh <- result{rows: assetRowsFromPage(address, protocol, p)}
			}
		}()
	}

	for page := 2; page <= totalPages; page++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(resultCh)
			for res := range resultCh {
				if res.err == nil {
					rows = append(rows, res.rows...)
				}
			}
			return rows, ctx.Err()
		case jobs <- page:
		}
	}
	close(jobs)
	wg.Wait()
	close(resultCh)

	var firstErr error
	for res := range resultCh {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
		} else {
			rows = append(rows, res.rows...)
		}
	}
	if firstErr != nil {
		return rows, firstErr
	}
	return rows, nil
}

func (c *OKLinkClient) FetchTransactions(ctx context.Context, address, chain, protocol string, limit int) ([]map[string]any, error) {
	first, totalPages, err := c.fetchPage(ctx, "/api/v5/explorer/address/transaction-list", address, chain, protocol, 1, limit)
	if err != nil {
		return nil, err
	}
	rows := copyRows(first.TransactionLists)
	if totalPages <= 1 {
		return rows, nil
	}

	type result struct {
		rows []map[string]any
		err  error
	}
	resultCh := make(chan result, totalPages-1)
	jobs := make(chan int)

	workerCount := cap(c.sem)
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > totalPages-1 {
		workerCount = totalPages - 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for page := range jobs {
				p, _, err := c.fetchPage(ctx, "/api/v5/explorer/address/transaction-list", address, chain, protocol, page, limit)
				if err != nil {
					resultCh <- result{err: err}
					continue
				}
				resultCh <- result{rows: copyRows(p.TransactionLists)}
			}
		}()
	}

	for page := 2; page <= totalPages; page++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(resultCh)
			for res := range resultCh {
				if res.err == nil {
					rows = append(rows, res.rows...)
				}
			}
			return rows, ctx.Err()
		case jobs <- page:
		}
	}
	close(jobs)
	wg.Wait()
	close(resultCh)

	var firstErr error
	for res := range resultCh {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
		} else {
			rows = append(rows, res.rows...)
		}
	}
	if firstErr != nil {
		return rows, firstErr
	}
	return rows, nil
}

func (c *OKLinkClient) EnrichTransactionDetails(ctx context.Context, rows []map[string]any, chain string) error {
	if len(rows) == 0 {
		return nil
	}

	workerCount := cap(c.sem)
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(rows) {
		workerCount = len(rows)
	}

	jobs := make(chan map[string]any)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for row := range jobs {
				txid := firstNonEmpty(toString(row["txId"]), toString(row["txid"]))
				if txid == "" {
					continue
				}
				detail, err := c.FetchTransactionDetail(ctx, chain, txid)
				if err != nil {
					recordErr(fmt.Errorf("%s: %w", txid, err))
					continue
				}
				enrichTransactionDetail(row, detail)
			}
		}()
	}

	for _, row := range rows {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- row:
		}
	}
	close(jobs)
	wg.Wait()
	return errors.Join(errs...)
}

func (c *OKLinkClient) FetchTransactionDetail(ctx context.Context, chain, txid string) (map[string]any, error) {
	params := url.Values{}
	params.Set("chainShortName", chain)
	params.Set("txid", txid)
	raw, body, err := c.get(ctx, "/api/v5/explorer/transaction/transaction-fills", params)
	if err != nil {
		return nil, err
	}
	_ = c.writeRaw(chain, "transaction-fills", txid, 0, body)
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	detail := map[string]any{}
	if err := json.Unmarshal(raw[0], &detail); err != nil {
		return nil, err
	}
	detail["detailRawJSON"] = compactJSON(raw[0])
	return detail, nil
}

func (c *OKLinkClient) fetchPage(ctx context.Context, endpoint, address, chain, protocol string, page, limit int) (PageData, int, error) {
	params := url.Values{}
	params.Set("chainShortName", chain)
	params.Set("address", address)
	params.Set("page", strconv.Itoa(page))
	params.Set("limit", strconv.Itoa(limit))
	if protocol != "" {
		params.Set("protocolType", protocol)
	}

	raw, body, err := c.get(ctx, endpoint, params)
	if err != nil {
		return PageData{}, 0, err
	}
	kind := strings.TrimPrefix(filepath.Base(endpoint), "address-")
	_ = c.writeRaw(chain, kind, protocol, page, body)
	if len(raw) == 0 {
		return PageData{Page: strconv.Itoa(page), Limit: strconv.Itoa(limit), ChainShortName: chain}, 0, nil
	}

	var p PageData
	if err := json.Unmarshal(raw[0], &p); err != nil {
		return PageData{}, 0, err
	}
	totalPages := parseIntDefault(p.TotalPage, 1)
	if totalPages < 1 {
		totalPages = 1
	}
	return p, totalPages, nil
}

func (c *OKLinkClient) get(ctx context.Context, endpoint string, params url.Values) ([]json.RawMessage, []byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			backoffSeconds := 1 << attempt
			if backoffSeconds > 30 {
				backoffSeconds = 30
			}
			backoff := time.Duration(backoffSeconds) * time.Second
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		if err := c.limiter.Wait(ctx); err != nil {
			return nil, nil, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case c.sem <- struct{}{}:
		}
		body, status, err := c.doGet(ctx, endpoint, params)
		<-c.sem
		if err != nil {
			lastErr = err
			continue
		}
		if status == http.StatusTooManyRequests || status >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", status, truncate(body, 500))
			continue
		}
		if status < 200 || status >= 300 {
			return nil, body, fmt.Errorf("HTTP %d: %s", status, truncate(body, 1000))
		}
		var resp APIResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, body, fmt.Errorf("JSON解析失败: %w; body=%s", err, truncate(body, 500))
		}
		if resp.Code != "0" {
			lastErr = fmt.Errorf("OKLink code=%s msg=%s", resp.Code, resp.Msg)
			if attempt < c.retries {
				continue
			}
			return nil, body, lastErr
		}
		return resp.Data, body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("unknown request error")
	}
	return nil, nil, lastErr
}

func (c *OKLinkClient) doGet(ctx context.Context, endpoint string, params url.Values) ([]byte, int, error) {
	fullURL := c.baseURL + endpoint
	if strings.Contains(c.baseURL, "/api/v5/explorer") && strings.HasPrefix(endpoint, "/api/v5/explorer") {
		fullURL = strings.TrimSuffix(c.baseURL, "/") + strings.TrimPrefix(endpoint, "/api/v5/explorer")
	}
	reqURL := fullURL
	if encoded := params.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Ok-Access-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "wallet-exporter/1.0")

	resp, err := doHTTPRequest(c.httpClient, req, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (c *OKLinkClient) writeRaw(chain, kind, protocol string, page int, body []byte) error {
	if c.rawDir == "" || len(body) == 0 {
		return nil
	}
	dir := filepath.Join(c.rawDir, sanitizeFilePart(chain))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	nameParts := []string{sanitizeFilePart(kind)}
	if protocol != "" {
		nameParts = append(nameParts, sanitizeFilePart(protocol))
	}
	if page > 0 {
		nameParts = append(nameParts, fmt.Sprintf("page_%06d", page))
	}
	name := strings.Join(nameParts, "_") + ".json"
	return os.WriteFile(filepath.Join(dir, name), body, 0644)
}

func copyRows(in []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, row := range in {
		cp := make(map[string]any, len(row)+8)
		for k, v := range row {
			cp[k] = v
		}
		raw, _ := json.Marshal(row)
		cp["rawJSON"] = compactJSON(raw)
		out = append(out, cp)
	}
	return out
}

func enrichTransaction(row map[string]any, address, chain, protocol string) {
	row["targetAddress"] = address
	row["chainShortName"] = firstNonEmpty(toString(row["chainShortName"]), chain)
	row["protocolType"] = protocol
	row["transactionTimeLocal"] = formatUnixMilli(toString(row["transactionTime"]))
	row["direction"] = detectDirection(address, toString(row["from"]), toString(row["to"]))
}

func enrichTransactionDetail(row, detail map[string]any) {
	if len(detail) == 0 {
		return
	}
	inputData := firstNonEmpty(
		toString(row["inputdate"]),
		toString(row["inputData"]),
		toString(detail["inputdate"]),
		toString(detail["inputData"]),
		toString(detail["inputdata"]),
	)
	row["inputdate"] = inputData

	logs := firstNonEmpty(toString(row["logs"]), detailLogsJSON(detail))
	row["logs"] = logs

	for _, key := range []string{"gasLimit", "gasUsed", "gasPrice", "nonce", "errorLog", "index", "confirm", "transactionType"} {
		if toString(row[key]) == "" && hasMeaningfulValue(detail[key]) {
			row[key] = detail[key]
		}
	}
	if toString(row["rawJSON"]) != "" && toString(detail["detailRawJSON"]) != "" {
		row["detailRawJSON"] = detail["detailRawJSON"]
	}
}

func detailLogsJSON(detail map[string]any) string {
	logKeys := []string{"logs", "log", "eventLogs", "contractDetails", "tokenTransferDetails"}
	combined := map[string]any{}
	for _, key := range logKeys {
		if hasMeaningfulValue(detail[key]) {
			combined[key] = detail[key]
		}
	}
	if len(combined) == 0 {
		return ""
	}
	raw, _ := json.Marshal(combined)
	return compactJSON(raw)
}

func hasMeaningfulValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	case []map[string]any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func assetRowsFromPage(address, protocol string, page PageData) []map[string]any {
	rows := make([]map[string]any, 0, len(page.TokenList))
	for _, token := range page.TokenList {
		row := make(map[string]any, len(token)+8)
		for k, v := range token {
			row[k] = v
		}
		row["address"] = address
		row["chainFullName"] = page.ChainFullName
		row["chainShortName"] = page.ChainShortName
		row["protocolType"] = firstNonEmpty(toString(row["protocolType"]), protocol)
		row["assetType"] = assetTypeFromToken(row)
		raw, _ := json.Marshal(token)
		row["rawJSON"] = compactJSON(raw)
		rows = append(rows, row)
	}
	return rows
}

func addNativeAssets(data *ExportData) {
	for _, summary := range data.Summaries {
		balance := toString(summary["balance"])
		symbol := toString(summary["balanceSymbol"])
		if balance == "" && symbol == "" {
			continue
		}
		raw, _ := json.Marshal(map[string]any{
			"balance":       balance,
			"balanceSymbol": symbol,
		})
		data.Assets = append(data.Assets, map[string]any{
			"address":        toString(summary["address"]),
			"chainFullName":  toString(summary["chainFullName"]),
			"chainShortName": toString(summary["chainShortName"]),
			"assetType":      "native",
			"protocolType":   "native",
			"symbol":         symbol,
			"holdingAmount":  balance,
			"rawJSON":        compactJSON(raw),
		})
	}
}

func buildFundRows(rows []map[string]any) []map[string]any {
	funds := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		asset := firstNonEmpty(toString(row["transactionSymbol"]), toString(row["symbol"]))
		fund := map[string]any{
			"targetAddress":        row["targetAddress"],
			"chainShortName":       row["chainShortName"],
			"protocolType":         row["protocolType"],
			"direction":            row["direction"],
			"transactionTimeLocal": row["transactionTimeLocal"],
			"txId":                 row["txId"],
			"asset":                asset,
			"amount":               row["amount"],
			"from":                 row["from"],
			"to":                   row["to"],
			"counterparty":         counterparty(toString(row["targetAddress"]), toString(row["from"]), toString(row["to"])),
			"txFee":                row["txFee"],
			"state":                row["state"],
			"height":               row["height"],
			"tokenContractAddress": row["tokenContractAddress"],
			"tokenId":              row["tokenId"],
			"inputdate":            row["inputdate"],
			"logs":                 row["logs"],
		}
		funds = append(funds, fund)
	}
	return funds
}

func fillSummaryCounters(data *ExportData) {
	type counts struct {
		tx, internal, token, nft, assets int
	}
	byChain := map[string]*counts{}
	ensure := func(chain string) *counts {
		if byChain[chain] == nil {
			byChain[chain] = &counts{}
		}
		return byChain[chain]
	}
	for _, row := range data.Transactions {
		ensure(toString(row["chainShortName"])).tx++
	}
	for _, row := range data.Internals {
		ensure(toString(row["chainShortName"])).internal++
	}
	for _, row := range data.TokenTransfers {
		ensure(toString(row["chainShortName"])).token++
	}
	for _, row := range data.NFTTransfers {
		ensure(toString(row["chainShortName"])).nft++
	}
	for _, row := range data.Assets {
		ensure(toString(row["chainShortName"])).assets++
	}
	for _, row := range data.Summaries {
		c := ensure(toString(row["chainShortName"]))
		row["count_transaction"] = c.tx
		row["count_internal"] = c.internal
		row["count_token"] = c.token
		row["count_nft"] = c.nft
		row["count_assets"] = c.assets
		if len(data.Errors) == 0 {
			row["downloadStatus"] = "完整"
		} else {
			row["downloadStatus"] = "不完整"
		}
		row["downloadErrorCount"] = len(data.Errors)
	}
}

func sortExportData(data *ExportData) {
	sortRows(data.Summaries, "chainShortName", true)
	sortRows(data.Transactions, "transactionTime", false)
	sortRows(data.Internals, "transactionTime", false)
	sortRows(data.TokenTransfers, "transactionTime", false)
	sortRows(data.NFTTransfers, "transactionTime", false)
	sortRows(data.Funds, "transactionTimeLocal", false)
	sortRows(data.Assets, "chainShortName", true)
}

func sortRows(rows []map[string]any, key string, asc bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := toString(rows[i][key]), toString(rows[j][key])
		if ai, err := strconv.ParseInt(a, 10, 64); err == nil {
			if bi, err := strconv.ParseInt(b, 10, 64); err == nil {
				if asc {
					return ai < bi
				}
				return ai > bi
			}
		}
		if asc {
			return a < b
		}
		return a > b
	})
}

func writeCSVWorkbook(cfg Config, data ExportData) error {
	f := excelize.NewFile()
	defaultSheet := f.GetSheetName(0)
	f.SetSheetName(defaultSheet, "交易")

	if len(data.RawTransactions) > 0 {
		if err := writeCSVRawSheet(f, "交易", data.RawTxHeaders, data.RawTransactions); err != nil {
			return err
		}
	}
	if len(data.RawTokenTransfers) > 0 {
		if err := writeCSVRawSheet(f, "代币转账", data.RawTokenHeaders, data.RawTokenTransfers); err != nil {
			return err
		}
	}
	f.SetActiveSheet(0)
	if err := os.MkdirAll(filepath.Dir(absOrClean(cfg.Out)), 0755); err != nil {
		return err
	}
	return f.SaveAs(cfg.Out)
}

func csvDownloadCheckRows(checks []CSVDownloadCheck) []map[string]string {
	rows := make([]map[string]string, 0, len(checks))
	for _, check := range checks {
		expected := ""
		if check.ExpectedTotal >= 0 {
			expected = strconv.FormatInt(check.ExpectedTotal, 10)
		}
		rows = append(rows, map[string]string{
			"address":        check.Address,
			"chain":          check.Chain,
			"kind":           check.Kind,
			"expected_total": expected,
			"downloaded":     strconv.Itoa(check.Downloaded),
			"status":         check.Status,
			"note":           check.Note,
		})
	}
	return rows
}

var guiDownloadSummaryHeaders = []Column{
	{"序号", "index"},
	{"地址", "address"},
	{"链", "chain"},
	{"状态", "status"},
	{"进度", "progress"},
	{"已下载", "downloaded"},
	{"总数量", "total"},
	{"报错数", "errorCount"},
	{"结果文件", "result"},
	{"开始时间", "startedAt"},
	{"结束时间", "finishedAt"},
	{"最后消息", "message"},
}

var guiDownloadPartHeaders = []Column{
	{"序号", "index"},
	{"地址", "address"},
	{"链", "chain"},
	{"数据类型", "kind"},
	{"已下载", "downloaded"},
	{"总数量", "total"},
	{"CSV直接下载", "directDownloaded"},
	{"CSV邮箱下载", "emailDownloaded"},
	{"状态", "status"},
}

var guiDownloadErrorHeaders = []Column{
	{"序号", "index"},
	{"地址", "address"},
	{"链", "chain"},
	{"错误序号", "errorIndex"},
	{"错误信息", "error"},
}

func writeGUIDownloadReport(path string, addresses []GUIAddressProgress) error {
	f := excelize.NewFile()
	defaultSheet := f.GetSheetName(0)
	f.SetSheetName(defaultSheet, "总览")
	if err := writeSheet(f, "总览", guiDownloadSummaryHeaders, guiDownloadSummaryRows(addresses)); err != nil {
		return err
	}
	if err := writeSheet(f, "分项下载", guiDownloadPartHeaders, guiDownloadPartRows(addresses)); err != nil {
		return err
	}
	if err := writeSheet(f, "报错情况", guiDownloadErrorHeaders, guiDownloadErrorRows(addresses)); err != nil {
		return err
	}
	f.SetActiveSheet(0)
	if err := os.MkdirAll(filepath.Dir(absOrClean(path)), 0755); err != nil {
		return err
	}
	return f.SaveAs(path)
}

func guiDownloadSummaryRows(addresses []GUIAddressProgress) []map[string]any {
	rows := make([]map[string]any, 0, len(addresses))
	for _, addr := range addresses {
		rows = append(rows, map[string]any{
			"index":      addr.Index + 1,
			"address":    addr.Address,
			"chain":      addr.Chain,
			"status":     guiAddressStatusText(addr.Status),
			"progress":   fmt.Sprintf("%d%%", clampPercent(addr.Progress)),
			"downloaded": addr.Downloaded,
			"total":      guiDownloadTotalText(addr.Total),
			"errorCount": len(addr.Errors),
			"result":     addr.Result,
			"startedAt":  addr.StartedAt,
			"finishedAt": addr.FinishedAt,
			"message":    addr.Message,
		})
	}
	return rows
}

func guiDownloadPartRows(addresses []GUIAddressProgress) []map[string]any {
	var rows []map[string]any
	for _, addr := range addresses {
		if len(addr.Parts) == 0 {
			rows = append(rows, map[string]any{
				"index":            addr.Index + 1,
				"address":          addr.Address,
				"chain":            addr.Chain,
				"kind":             "全部",
				"downloaded":       addr.Downloaded,
				"total":            guiDownloadTotalText(addr.Total),
				"directDownloaded": "",
				"emailDownloaded":  "",
				"status":           guiAddressStatusText(addr.Status),
			})
			continue
		}
		for _, part := range addr.Parts {
			rows = append(rows, map[string]any{
				"index":            addr.Index + 1,
				"address":          addr.Address,
				"chain":            firstNonEmpty(part.Chain, addr.Chain),
				"kind":             part.Kind,
				"downloaded":       part.Downloaded,
				"total":            guiDownloadTotalText(part.Total),
				"directDownloaded": guiDownloadOptionalCount(part.DirectDownloaded),
				"emailDownloaded":  guiDownloadOptionalCount(part.EmailDownloaded),
				"status":           guiAddressStatusText(part.Status),
			})
		}
	}
	return rows
}

func guiDownloadOptionalCount(count int) any {
	if count <= 0 {
		return ""
	}
	return count
}

func guiDownloadErrorRows(addresses []GUIAddressProgress) []map[string]any {
	var rows []map[string]any
	for _, addr := range addresses {
		for i, msg := range addr.Errors {
			rows = append(rows, map[string]any{
				"index":      addr.Index + 1,
				"address":    addr.Address,
				"chain":      addr.Chain,
				"errorIndex": i + 1,
				"error":      msg,
			})
		}
	}
	return rows
}

func guiDownloadTotalText(total int64) any {
	if total < 0 {
		return "待统计"
	}
	return total
}

func guiAddressStatusText(status string) string {
	switch strings.TrimSpace(status) {
	case "pending":
		return "等待"
	case "running":
		return "下载中"
	case "done", "complete":
		return "完成"
	case "failed":
		return "失败"
	case "paused":
		return "已暂停"
	case "cancelled":
		return "已取消"
	case "":
		return ""
	default:
		return status
	}
}

func writeCSVRawSheet(f *excelize.File, name string, headers []string, records []map[string]string) error {
	if _, err := f.NewSheet(name); err != nil {
		return err
	}
	if len(records) == 0 || len(headers) == 0 {
		return nil
	}

	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(name, cell, h); err != nil {
			return err
		}
	}

	for r, record := range records {
		for c, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			_ = f.SetCellValue(name, cell, csvRawCellValue(h, record[h]))
		}
	}

	if len(headers) > 0 {
		_ = f.SetSheetDimension(name, "A1:"+mustCoord(len(headers), len(records)+1))
		_ = f.SetPanes(name, &excelize.Panes{
			Freeze:      true,
			Split:       false,
			XSplit:      0,
			YSplit:      1,
			TopLeftCell: "A2",
			ActivePane:  "bottomLeft",
		})
		for i := range headers {
			colName, _ := excelize.ColumnNumberToName(i + 1)
			_ = f.SetColWidth(name, colName, colName, 18)
		}
	}
	return nil
}

func csvRawCellValue(header, value string) any {
	value = sanitizeExcelString(strings.TrimSpace(value))
	if value == "" || !isCSVRawNumericHeader(header) {
		return value
	}
	normalized := strings.ReplaceAll(value, ",", "")
	if strings.HasPrefix(normalized, "+") {
		normalized = normalized[1:]
	}
	if strings.ContainsAny(normalized, "xX") {
		return value
	}
	n, err := strconv.ParseFloat(normalized, 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return value
	}
	return n
}

func isCSVRawNumericHeader(header string) bool {
	h := strings.ToLower(strings.TrimSpace(header))
	switch h {
	case "amount", "value", "fee", "txfee", "tx fee", "数量", "金额", "手续费", "价值":
		return true
	default:
		return false
	}
}

func mustCoord(col, row int) string {
	c, _ := excelize.CoordinatesToCellName(col, row)
	return c
}

func writeWorkbook(cfg Config, data ExportData) error {
	f := excelize.NewFile()
	defaultSheet := f.GetSheetName(0)
	f.SetSheetName(defaultSheet, "统计数据")

	if err := writeSheet(f, "统计数据", summaryHeaders, data.Summaries); err != nil {
		return err
	}
	if err := writeSheet(f, "交易", transactionHeaders, data.Transactions); err != nil {
		return err
	}
	if err := writeSheet(f, "内部交易", transactionHeaders, data.Internals); err != nil {
		return err
	}
	if err := writeSheet(f, "代币转账", transactionHeaders, data.TokenTransfers); err != nil {
		return err
	}
	if err := writeSheet(f, "NFT转账", transactionHeaders, data.NFTTransfers); err != nil {
		return err
	}
	if err := writeSheet(f, "资金", fundHeaders, data.Funds); err != nil {
		return err
	}
	if err := writeSheet(f, "多链资产", assetHeaders, data.Assets); err != nil {
		return err
	}
	f.SetActiveSheet(0)

	if err := os.MkdirAll(filepath.Dir(absOrClean(cfg.Out)), 0755); err != nil {
		return err
	}
	return f.SaveAs(cfg.Out)
}

func writeSheet(f *excelize.File, name string, cols []Column, rows []map[string]any) error {
	if idx, err := f.GetSheetIndex(name); err != nil || idx == -1 {
		if _, err := f.NewSheet(name); err != nil {
			return err
		}
	}

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"366092"}, Pattern: 1},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
	})
	for i, col := range cols {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(name, cell, col.Title); err != nil {
			return err
		}
	}
	endCell, _ := excelize.CoordinatesToCellName(len(cols), 1)
	_ = f.SetCellStyle(name, "A1", endCell, headerStyle)

	for r, row := range rows {
		for c, col := range cols {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := f.SetCellValue(name, cell, cleanCellValue(row[col.Key])); err != nil {
				return err
			}
		}
	}

	if len(cols) > 0 {
		if err := setSheetDimension(f, name, len(cols), len(rows)+1); err != nil {
			return err
		}
		lastCol, _ := excelize.ColumnNumberToName(len(cols))
		_ = f.AutoFilter(name, fmt.Sprintf("A1:%s1", lastCol), []excelize.AutoFilterOptions{})
		_ = f.SetPanes(name, &excelize.Panes{
			Freeze:      true,
			Split:       false,
			XSplit:      0,
			YSplit:      1,
			TopLeftCell: "A2",
			ActivePane:  "bottomLeft",
		})
		for i := range cols {
			colName, _ := excelize.ColumnNumberToName(i + 1)
			width := 16.0
			switch cols[i].Key {
			case "rawJSON", "logs":
				width = 45
			case "inputdate":
				width = 28
			case "txId", "blockHash", "tokenContractAddress", "createContractTransactionHash", "l1OriginHash":
				width = 34
			case "from", "to", "address", "targetAddress", "counterparty", "contractAddress", "createContractAddress":
				width = 30
			case "transactionTimeLocal", "firstTransactionTimeLocal", "lastTransactionTimeLocal", "exportedAt":
				width = 20
			}
			_ = f.SetColWidth(name, colName, colName, width)
		}
	}
	return nil
}

func setSheetDimension(f *excelize.File, name string, cols, rows int) error {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	endCell, err := excelize.CoordinatesToCellName(cols, rows)
	if err != nil {
		return err
	}
	return f.SetSheetDimension(name, "A1:"+endCell)
}

func cleanCellValue(v any) any {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return sanitizeExcelString(x)
	case fmt.Stringer:
		return sanitizeExcelString(x.String())
	case bool:
		return x
	case float64:
		return x
	case float32:
		return x
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return x
	default:
		b, _ := json.Marshal(x)
		return sanitizeExcelString(string(b))
	}
}

func sanitizeExcelString(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) > 32760 {
		return s[:32760] + "...(truncated)"
	}
	return s
}

func detectDirection(address, from, to string) string {
	addr := strings.ToLower(address)
	from = strings.ToLower(from)
	to = strings.ToLower(to)
	switch {
	case from == addr && to == addr:
		return "SELF"
	case from == addr:
		return "OUT"
	case to == addr:
		return "IN"
	default:
		return ""
	}
}

func counterparty(address, from, to string) string {
	addr := strings.ToLower(address)
	if strings.ToLower(from) == addr {
		return to
	}
	if strings.ToLower(to) == addr {
		return from
	}
	return ""
}

func classifyProtocol(protocol string) string {
	p := strings.ToLower(protocol)
	switch {
	case p == "transaction":
		return "transaction"
	case p == "internal":
		return "internal"
	case strings.Contains(p, "721") || strings.Contains(p, "1155") || strings.Contains(p, "nft"):
		return "nft"
	default:
		return "token"
	}
}

func assetTypeFromToken(row map[string]any) string {
	if classifyProtocol(toString(row["protocolType"])) == "nft" {
		return "nft"
	}
	if toString(row["tokenId"]) != "" {
		return "nft"
	}
	return "token"
}

func formatUnixMilli(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return ""
	}
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
}

func parseIntDefault(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if math.Trunc(x) == x {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func compactJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func truncate(body []byte, n int) string {
	s := string(body)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func sanitizeFilePart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	return replacer.Replace(s)
}

func absOrClean(path string) string {
	if path == "" {
		return "."
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.Clean(path)
}
