package cryptodownload

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etl/backend/internal/cryptodownload/useragent"
	"golang.org/x/crypto/sha3"
)

var (
	transferTopic       = eventTopic("Transfer(address,address,uint256)")
	transferSingleTopic = eventTopic("TransferSingle(address,address,address,uint256,uint256)")
	transferBatchTopic  = eventTopic("TransferBatch(address,address,address,uint256[],uint256[])")
)

const exactNativeBlockScanLimit uint64 = 200000

type RPCChainConfig struct {
	ChainFullName  string   `json:"chainFullName"`
	ChainShortName string   `json:"chainShortName"`
	RPCURL         string   `json:"rpcUrl"`
	FallbackURLs   []string `json:"fallbackUrls,omitempty"`
	NativeSymbol   string   `json:"nativeSymbol"`
	StartBlock     *int64   `json:"startBlock"`
	EndBlock       *int64   `json:"endBlock"`
}

type rpcConfigFile struct {
	Chains []RPCChainConfig `json:"chains"`
}

type EVMRPCClient struct {
	rpcURLs    []string
	httpClient *http.Client
	limiter    *RateLimiter
	sem        chan struct{}
	retries    int
	rawDir     string
	id         atomic.Uint64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      uint64 `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
	ID      uint64          `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type RPCBlock struct {
	Number       string           `json:"number"`
	Hash         string           `json:"hash"`
	Timestamp    string           `json:"timestamp"`
	Transactions []RPCTransaction `json:"transactions"`
	Raw          json.RawMessage  `json:"-"`
}

type RPCBlockHeader struct {
	Number    string          `json:"number"`
	Hash      string          `json:"hash"`
	Timestamp string          `json:"timestamp"`
	Raw       json.RawMessage `json:"-"`
}

type RPCTransaction struct {
	Hash             string          `json:"hash"`
	BlockHash        string          `json:"blockHash"`
	BlockNumber      string          `json:"blockNumber"`
	TransactionIndex string          `json:"transactionIndex"`
	From             string          `json:"from"`
	To               string          `json:"to"`
	Value            string          `json:"value"`
	Input            string          `json:"input"`
	Gas              string          `json:"gas"`
	GasPrice         string          `json:"gasPrice"`
	MaxFeePerGas     string          `json:"maxFeePerGas"`
	Nonce            string          `json:"nonce"`
	Type             string          `json:"type"`
	Raw              json.RawMessage `json:"-"`
}

type RPCReceipt struct {
	TransactionHash   string          `json:"transactionHash"`
	BlockHash         string          `json:"blockHash"`
	BlockNumber       string          `json:"blockNumber"`
	TransactionIndex  string          `json:"transactionIndex"`
	From              string          `json:"from"`
	To                string          `json:"to"`
	ContractAddress   string          `json:"contractAddress"`
	Status            string          `json:"status"`
	GasUsed           string          `json:"gasUsed"`
	EffectiveGasPrice string          `json:"effectiveGasPrice"`
	Logs              []RPCLog        `json:"logs"`
	Raw               json.RawMessage `json:"-"`
}

type RPCLog struct {
	Address          string          `json:"address"`
	Topics           []string        `json:"topics"`
	Data             string          `json:"data"`
	BlockNumber      string          `json:"blockNumber"`
	TransactionHash  string          `json:"transactionHash"`
	TransactionIndex string          `json:"transactionIndex"`
	BlockHash        string          `json:"blockHash"`
	LogIndex         string          `json:"logIndex"`
	Removed          bool            `json:"removed"`
	Raw              json.RawMessage `json:"-"`
}

type RPCTrace struct {
	Action          map[string]any  `json:"action"`
	Result          map[string]any  `json:"result"`
	Type            string          `json:"type"`
	Error           string          `json:"error"`
	BlockNumber     int64           `json:"blockNumber"`
	TransactionHash string          `json:"transactionHash"`
	TraceAddress    []int           `json:"traceAddress"`
	Raw             json.RawMessage `json:"-"`
}

type callFrame struct {
	Type    string      `json:"type"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Value   string      `json:"value"`
	Input   string      `json:"input"`
	Gas     string      `json:"gas"`
	GasUsed string      `json:"gasUsed"`
	Error   string      `json:"error"`
	Calls   []callFrame `json:"calls"`
}

type traceFilterObject struct {
	FromBlock   string   `json:"fromBlock"`
	ToBlock     string   `json:"toBlock"`
	FromAddress []string `json:"fromAddress,omitempty"`
	ToAddress   []string `json:"toAddress,omitempty"`
}

func collectAllFromRPC(ctx context.Context, cfg Config) ExportData {
	chains, err := loadRPCChains(cfg)
	if err != nil {
		return ExportData{Errors: []string{err.Error()}}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		data ExportData
	)
	for _, chain := range chains {
		chain := chain
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := NewEVMRPCClient(chain.RPCURL, chain.FallbackURLs, cfg)
			chainData, err := client.CollectAddress(ctx, cfg, chain)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				data.Errors = append(data.Errors, err.Error())
			}
			data.Summaries = append(data.Summaries, chainData.Summaries...)
			data.Transactions = append(data.Transactions, chainData.Transactions...)
			data.Internals = append(data.Internals, chainData.Internals...)
			data.TokenTransfers = append(data.TokenTransfers, chainData.TokenTransfers...)
			data.NFTTransfers = append(data.NFTTransfers, chainData.NFTTransfers...)
			data.Funds = append(data.Funds, chainData.Funds...)
			data.Assets = append(data.Assets, chainData.Assets...)
			data.Errors = append(data.Errors, chainData.Errors...)
		}()
	}
	wg.Wait()
	fillSummaryCounters(&data)
	sortExportData(&data)
	return data
}

func loadRPCChains(cfg Config) ([]RPCChainConfig, error) {
	if cfg.RPCConfig != "" {
		body, err := os.ReadFile(cfg.RPCConfig)
		if err != nil {
			return nil, err
		}
		var file rpcConfigFile
		if err := json.Unmarshal(body, &file); err != nil {
			return nil, err
		}
		if len(file.Chains) == 0 {
			return nil, errors.New("rpc-config 中没有 chains")
		}
		for i := range file.Chains {
			if strings.TrimSpace(file.Chains[i].RPCURL) == "" {
				return nil, fmt.Errorf("rpc-config chains[%d].rpcUrl 不能为空", i)
			}
			if file.Chains[i].ChainShortName == "" {
				file.Chains[i].ChainShortName = fmt.Sprintf("CHAIN_%d", i+1)
			}
			if file.Chains[i].ChainFullName == "" {
				file.Chains[i].ChainFullName = file.Chains[i].ChainShortName
			}
		}
		return file.Chains, nil
	}

	chainShort := "RPC"
	if len(cfg.Chains) > 0 {
		chainShort = cfg.Chains[0]
	}
	return []RPCChainConfig{{
		ChainFullName:  chainShort,
		ChainShortName: chainShort,
		RPCURL:         cfg.RPCURL,
		FallbackURLs:   cfg.RPCFallbacks,
		NativeSymbol:   firstNonEmpty(cfg.NativeSymbol, chainShort),
	}}, nil
}

func NewEVMRPCClient(rpcURL string, fallbackURLs []string, cfg Config) *EVMRPCClient {
	urls := make([]string, 0, 1+len(fallbackURLs))
	if rpcURL != "" {
		urls = append(urls, rpcURL)
	}
	urls = append(urls, fallbackURLs...)
	return &EVMRPCClient{
		rpcURLs: urls,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		limiter: NewRateLimiter(cfg.RPS),
		sem:     make(chan struct{}, cfg.Workers),
		retries: cfg.Retries,
		rawDir:  cfg.RawDir,
	}
}

func (c *EVMRPCClient) CollectAddress(ctx context.Context, cfg Config, chain RPCChainConfig) (ExportData, error) {
	var data ExportData
	reportProgress(cfg, "链 %s: 获取最新区块", chain.ChainShortName)
	latest, err := c.BlockNumber(ctx)
	if err != nil {
		return data, fmt.Errorf("rpc %s blockNumber: %w", chain.ChainShortName, err)
	}
	startBlock := uint64(cfg.StartBlock)
	if chain.StartBlock != nil {
		startBlock = uint64(*chain.StartBlock)
	}
	endBlock := latest
	if cfg.EndBlock >= 0 {
		endBlock = uint64(cfg.EndBlock)
	}
	if chain.EndBlock != nil && *chain.EndBlock >= 0 {
		endBlock = uint64(*chain.EndBlock)
	}
	if cfg.CutoffBlock > 0 {
		if cfg.CutoffBlock == 1 {
			endBlock = 0
		} else {
			cutoffEnd := uint64(cfg.CutoffBlock - 1)
			if cutoffEnd < endBlock {
				endBlock = cutoffEnd
			}
		}
	}
	if endBlock > latest {
		endBlock = latest
	}
	if startBlock == 0 {
		reportProgress(cfg, "链 %s: 自动查找第一笔交易区块", chain.ChainShortName)
		if first, err := c.FindFirstActivityBlock(ctx, cfg.Address, endBlock, cfg.Progress); err == nil {
			startBlock = first
		} else {
			const defaultRecentBlocks = 100000
			if endBlock > defaultRecentBlocks {
				startBlock = endBlock - defaultRecentBlocks
			}
		}
	}
	if endBlock < startBlock {
		return data, fmt.Errorf("rpc %s endBlock < startBlock", chain.ChainShortName)
	}
	chain.NativeSymbol = firstNonEmpty(chain.NativeSymbol, cfg.NativeSymbol, chain.ChainShortName)
	reportProgress(cfg, "链 %s: 扫描区块 %d - %d（共 %d 个区块）", chain.ChainShortName, startBlock, endBlock, endBlock-startBlock+1)

	summary, err := c.BuildSummary(ctx, cfg.Address, chain, startBlock, endBlock, latest)
	if err != nil {
		data.Errors = append(data.Errors, fmt.Sprintf("summary %s: %v", chain.ChainShortName, err))
	}
	data.Summaries = append(data.Summaries, summary)

	if cfg.ScanNative {
		reportProgress(cfg, "链 %s: 扫描普通交易", chain.ChainShortName)
		forceExactNative := cfg.CutoffBlock > 0
		if isContract, err := c.IsContract(ctx, cfg.Address, endBlock); err == nil && isContract {
			forceExactNative = true
			reportProgress(cfg, "链 %s: 输入地址是合约，普通交易使用逐区块精确扫描", chain.ChainShortName)
		} else if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("contract check %s: %v", chain.ChainShortName, err))
		}
		nativeRows, err := c.ScanNativeTransactions(ctx, cfg.Address, chain, startBlock, endBlock, forceExactNative, cfg.Progress)
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("native scan %s: %v", chain.ChainShortName, err))
		}
		data.Transactions = append(data.Transactions, nativeRows...)
		data.Funds = append(data.Funds, buildFundRows(nativeRows)...)
	} else {
		reportProgress(cfg, "链 %s: 已跳过普通交易逐区块扫描", chain.ChainShortName)
	}

	reportProgress(cfg, "链 %s: 扫描代币和 NFT 日志", chain.ChainShortName)
	tokenRows, nftRows, tokenContracts, err := c.ScanTokenLogs(ctx, cfg.Address, chain, startBlock, endBlock, cfg.LogBatch, cfg.Progress)
	if err != nil {
		data.Errors = append(data.Errors, fmt.Sprintf("token logs %s: %v", chain.ChainShortName, err))
	}
	data.TokenTransfers = append(data.TokenTransfers, tokenRows...)
	data.NFTTransfers = append(data.NFTTransfers, nftRows...)
	data.Funds = append(data.Funds, buildFundRows(tokenRows)...)
	data.Funds = append(data.Funds, buildFundRows(nftRows)...)

	reportProgress(cfg, "链 %s: 查询资产余额", chain.ChainShortName)
	assetRows, err := c.BuildRPCAssets(ctx, cfg.Address, chain, tokenContracts)
	if err != nil {
		data.Errors = append(data.Errors, fmt.Sprintf("assets %s: %v", chain.ChainShortName, err))
	}
	data.Assets = append(data.Assets, assetRows...)

	reportProgress(cfg, "链 %s: 扫描内部交易", chain.ChainShortName)
	internalRows, err := c.ScanInternalTransactions(ctx, cfg.Address, chain, startBlock, endBlock, cfg)
	if err != nil {
		data.Errors = append(data.Errors, fmt.Sprintf("internal %s: %v", chain.ChainShortName, err))
	}
	data.Internals = append(data.Internals, internalRows...)
	data.Funds = append(data.Funds, buildFundRows(internalRows)...)

	return data, nil
}

func (c *EVMRPCClient) BlockNumber(ctx context.Context) (uint64, error) {
	var hexBlock string
	if err := c.call(ctx, "eth_blockNumber", nil, &hexBlock); err != nil {
		return 0, err
	}
	return hexToUint64(hexBlock)
}

func (c *EVMRPCClient) GetCode(ctx context.Context, address string, blockNum uint64) (string, error) {
	var code string
	if err := c.call(ctx, "eth_getCode", []any{address, uint64ToHex(blockNum)}, &code); err != nil {
		return "", err
	}
	return strings.TrimSpace(code), nil
}

func (c *EVMRPCClient) IsContract(ctx context.Context, address string, blockNum uint64) (bool, error) {
	code, err := c.GetCode(ctx, address, blockNum)
	if err != nil {
		return false, err
	}
	code = strings.ToLower(strings.TrimSpace(code))
	return code != "" && code != "0x" && code != "0x0", nil
}

func (c *EVMRPCClient) GetTransactionCount(ctx context.Context, address string, blockNum uint64) (uint64, error) {
	var hexCount string
	if err := c.call(ctx, "eth_getTransactionCount", []any{address, uint64ToHex(blockNum)}, &hexCount); err != nil {
		return 0, err
	}
	return hexToUint64(hexCount)
}

func (c *EVMRPCClient) FindFirstActivityBlock(ctx context.Context, address string, latest uint64, progress func(string)) (uint64, error) {
	first, err := c.findFirstByTransactionCount(ctx, address, latest)
	if err != nil {
		if isArchiveError(err) {
			return 0, err
		}
	}
	if err == nil && first < latest {
		return first, nil
	}
	return c.findFirstByLogs(ctx, address, latest, progress)
}

func (c *EVMRPCClient) findFirstByTransactionCount(ctx context.Context, address string, latest uint64) (uint64, error) {
	count, err := c.GetTransactionCount(ctx, address, latest)
	if err != nil {
		if isArchiveError(err) {
			return 0, err
		}
		return 0, err
	}
	if count == 0 {
		return latest, nil
	}
	lo, hi := uint64(0), latest
	for lo < hi {
		mid := (lo + hi) / 2
		cnt, err := c.GetTransactionCount(ctx, address, mid)
		if err != nil {
			if isArchiveError(err) {
				return 0, err
			}
			return 0, err
		}
		if cnt > 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, nil
}

func (c *EVMRPCClient) findFirstByLogs(ctx context.Context, address string, latest uint64, progress func(string)) (uint64, error) {
	addrTopic := addressToTopic(address)
	allTopics := []any{transferTopic, transferSingleTopic, transferBatchTopic}

	hasActivity := func(lo, hi uint64) (bool, error) {
		for _, q := range [][]any{
			{allTopics, addrTopic},
			{allTopics, nil, addrTopic},
			{allTopics, nil, nil, addrTopic},
		} {
			logs, err := c.GetLogs(ctx, lo, hi, q)
			if err != nil {
				if isArchiveError(err) {
					return false, err
				}
				return false, err
			}
			if len(logs) > 0 {
				return true, nil
			}
		}
		return false, nil
	}

	const maxRange uint64 = 100000
	lo := uint64(0)
	hi := latest

	for {
		if hi > maxRange {
			lo = hi - maxRange
		} else {
			lo = 0
		}
		active, err := hasActivity(lo, hi)
		if err != nil {
			return 0, fmt.Errorf("查找第一笔交易 %s [%d-%d]: %w", address, lo, hi, err)
		}
		if active {
			break
		}
		if lo == 0 {
			return latest, nil
		}
		hi = lo - 1
	}

	for lo < hi {
		mid := (lo + hi) / 2
		active, err := hasActivity(lo, mid)
		if err != nil {
			return 0, err
		}
		if active {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, nil
}

func (c *EVMRPCClient) BuildSummary(ctx context.Context, address string, chain RPCChainConfig, start, end, latest uint64) (map[string]any, error) {
	var balanceHex string
	balanceBlock := uint64ToHex(end)
	err := c.call(ctx, "eth_getBalance", []any{address, balanceBlock}, &balanceHex)
	row := map[string]any{
		"address":        address,
		"chainFullName":  chain.ChainFullName,
		"chainShortName": chain.ChainShortName,
		"balance":        weiHexToDecimal(balanceHex),
		"balanceSymbol":  chain.NativeSymbol,
		"exportedAt":     time.Now().Format("2006-01-02 15:04:05"),
		"rawJSON":        jsonCompactAny(map[string]any{"source": "rpc", "rpcUrl": chain.RPCURL, "startBlock": start, "endBlock": end, "latestBlock": latest, "balanceBlock": balanceBlock, "balanceHex": balanceHex}),
	}
	return row, err
}

func (c *EVMRPCClient) ScanNativeTransactions(ctx context.Context, address string, chain RPCChainConfig, start, end uint64, forceExact bool, progress func(string)) ([]map[string]any, error) {
	if !forceExact && end >= start && end-start+1 > exactNativeBlockScanLimit {
		return c.ScanOutgoingNativeTransactionsByNonce(ctx, address, chain, start, end, progress)
	}

	type result struct {
		rows []map[string]any
		err  error
	}
	jobs := make(chan uint64)
	results := make(chan result)
	workers := cap(c.sem)
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				block, err := c.GetBlockByNumber(ctx, n)
				if err != nil {
					results <- result{err: fmt.Errorf("block %d: %w", n, err)}
					continue
				}
				rows := make([]map[string]any, 0)
				for _, tx := range block.Transactions {
					if !addressInTx(address, tx) {
						continue
					}
					receipt, err := c.GetTransactionReceipt(ctx, tx.Hash)
					if err != nil {
						results <- result{err: fmt.Errorf("receipt %s: %w", tx.Hash, err)}
						continue
					}
					rows = append(rows, nativeTxRow(address, chain, block, tx, receipt))
				}
				results <- result{rows: rows}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for n := start; n <= end; n++ {
			select {
			case <-ctx.Done():
				return
			case jobs <- n:
			}
			if n == ^uint64(0) {
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		rows       []map[string]any
		errs       []error
		done       uint64
		total      = end - start + 1
		lastReport = time.Now()
	)
	for res := range results {
		done++
		if res.err != nil {
			errs = append(errs, res.err)
		}
		rows = append(rows, res.rows...)
		if progress != nil && (done == total || done%100 == 0 || time.Since(lastReport) >= 3*time.Second) {
			progress(fmt.Sprintf("链 %s: 普通交易区块进度 %d/%d，命中 %d 行", chain.ChainShortName, done, total, len(rows)))
			lastReport = time.Now()
		}
		if done%50000 == 0 || done == total {
			_ = SaveRPCCheckpoint(c.rawDir, RPCCheckpoint{
				Chain:     chain.ChainShortName,
				Address:   address,
				LastBlock: start + done - 1,
			})
		}
	}
	return rows, errors.Join(errs...)
}

func (c *EVMRPCClient) ScanOutgoingNativeTransactionsByNonce(ctx context.Context, address string, chain RPCChainConfig, start, end uint64, progress func(string)) ([]map[string]any, error) {
	startNonce := uint64(0)
	if start > 0 {
		count, err := c.GetTransactionCount(ctx, address, start-1)
		if err != nil {
			return nil, fmt.Errorf("native nonce at block %d: %w", start-1, err)
		}
		startNonce = count
	}
	endNonce, err := c.GetTransactionCount(ctx, address, end)
	if err != nil {
		return nil, fmt.Errorf("native nonce at block %d: %w", end, err)
	}
	if endNonce <= startNonce {
		if progress != nil {
			progress(fmt.Sprintf("链 %s: 大范围普通交易快扫未发现主动发出的交易", chain.ChainShortName))
		}
		return nil, nil
	}

	countCache := map[uint64]uint64{}
	getCount := func(block uint64) (uint64, error) {
		if v, ok := countCache[block]; ok {
			return v, nil
		}
		v, err := c.GetTransactionCount(ctx, address, block)
		if err != nil {
			return 0, err
		}
		countCache[block] = v
		return v, nil
	}
	findBlockForNonce := func(nonce uint64) (uint64, error) {
		lo, hi := start, end
		for lo < hi {
			mid := lo + (hi-lo)/2
			cnt, err := getCount(mid)
			if err != nil {
				return 0, err
			}
			if cnt > nonce {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return lo, nil
	}

	total := endNonce - startNonce
	rows := make([]map[string]any, 0, total)
	seenTx := map[string]bool{}
	seenNonce := map[uint64]bool{}
	var errs []error
	lastReport := time.Now()

	for nonce := startNonce; nonce < endNonce; nonce++ {
		if seenNonce[nonce] {
			continue
		}
		blockNo, err := findBlockForNonce(nonce)
		if err != nil {
			errs = append(errs, fmt.Errorf("native nonce %d: %w", nonce, err))
			continue
		}
		block, err := c.GetBlockByNumber(ctx, blockNo)
		if err != nil {
			errs = append(errs, fmt.Errorf("native block %d: %w", blockNo, err))
			continue
		}
		for _, tx := range block.Transactions {
			if !sameAddress(address, tx.From) {
				continue
			}
			txNonce, err := hexToUint64(tx.Nonce)
			if err != nil || txNonce < startNonce || txNonce >= endNonce {
				continue
			}
			seenNonce[txNonce] = true
			key := strings.ToLower(tx.Hash)
			if seenTx[key] {
				continue
			}
			receipt, err := c.GetTransactionReceipt(ctx, tx.Hash)
			if err != nil {
				errs = append(errs, fmt.Errorf("native receipt %s: %w", tx.Hash, err))
				continue
			}
			seenTx[key] = true
			rows = append(rows, nativeTxRow(address, chain, block, tx, receipt))
		}
		if progress != nil && (len(rows) == int(total) || (len(rows) > 0 && len(rows)%10 == 0) || time.Since(lastReport) >= 3*time.Second) {
			progress(fmt.Sprintf("链 %s: 普通交易快扫进度 %d/%d，已命中 %d 行", chain.ChainShortName, len(seenNonce), total, len(rows)))
			lastReport = time.Now()
		}
	}
	return rows, errors.Join(errs...)
}

func (c *EVMRPCClient) GetBlockByNumber(ctx context.Context, number uint64) (RPCBlock, error) {
	var raw json.RawMessage
	if err := c.callRaw(ctx, "eth_getBlockByNumber", []any{uint64ToHex(number), true}, &raw); err != nil {
		return RPCBlock{}, err
	}
	var block RPCBlock
	if err := json.Unmarshal(raw, &block); err != nil {
		return RPCBlock{}, err
	}
	block.Raw = raw
	for i := range block.Transactions {
		txRaw, _ := json.Marshal(block.Transactions[i])
		block.Transactions[i].Raw = txRaw
	}
	return block, nil
}

func (c *EVMRPCClient) GetTransactionReceipt(ctx context.Context, txHash string) (RPCReceipt, error) {
	var raw json.RawMessage
	if err := c.callRaw(ctx, "eth_getTransactionReceipt", []any{txHash}, &raw); err != nil {
		return RPCReceipt{}, err
	}
	if string(raw) == "null" || len(raw) == 0 {
		return RPCReceipt{}, fmt.Errorf("receipt is null")
	}
	var receipt RPCReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return RPCReceipt{}, err
	}
	receipt.Raw = raw
	for i := range receipt.Logs {
		logRaw, _ := json.Marshal(receipt.Logs[i])
		receipt.Logs[i].Raw = logRaw
	}
	return receipt, nil
}

func nativeTxRow(address string, chain RPCChainConfig, block RPCBlock, tx RPCTransaction, receipt RPCReceipt) map[string]any {
	ts := blockTimestampLocal(block.Timestamp)
	raw := jsonCompactAny(map[string]any{"transaction": tx, "receipt": receipt, "block": map[string]any{"number": block.Number, "hash": block.Hash, "timestamp": block.Timestamp}})
	logs := jsonCompactAny(receipt.Logs)
	return map[string]any{
		"targetAddress":        address,
		"chainFullName":        chain.ChainFullName,
		"chainShortName":       chain.ChainShortName,
		"protocolType":         "transaction",
		"direction":            detectDirection(address, tx.From, tx.To),
		"txId":                 tx.Hash,
		"methodId":             methodID(tx.Input),
		"blockHash":            tx.BlockHash,
		"height":               hexToDecimalString(tx.BlockNumber),
		"transactionTime":      hexToDecimalString(block.Timestamp),
		"transactionTimeLocal": ts,
		"from":                 tx.From,
		"to":                   tx.To,
		"amount":               weiHexToDecimal(tx.Value),
		"transactionSymbol":    chain.NativeSymbol,
		"txFee":                weiBigToDecimal(txFeeWei(receipt, tx)),
		"state":                receiptStatus(receipt.Status),
		"inputdate":            tx.Input,
		"logs":                 logs,
		"rawJSON":              raw,
	}
}

func (c *EVMRPCClient) ScanTokenLogs(ctx context.Context, address string, chain RPCChainConfig, start, end, batch uint64, progress func(string)) ([]map[string]any, []map[string]any, map[string]map[string]bool, error) {
	type query struct {
		name   string
		topics []any
	}
	addrTopic := addressToTopic(address)
	queries := []query{
		{"transfer_from", []any{transferTopic, addrTopic}},
		{"transfer_to", []any{transferTopic, nil, addrTopic}},
		{"erc1155_from", []any{[]string{transferSingleTopic, transferBatchTopic}, nil, addrTopic}},
		{"erc1155_to", []any{[]string{transferSingleTopic, transferBatchTopic}, nil, nil, addrTopic}},
	}

	type job struct {
		from, to uint64
		q        query
	}
	type result struct {
		logs []RPCLog
		err  error
	}
	jobs := make(chan job)
	results := make(chan result)
	workers := cap(c.sem)
	if workers < 1 {
		workers = 1
	}
	rangeCount := uint64(0)
	for from := start; from <= end; {
		to := from + batch - 1
		if to > end || to < from {
			to = end
		}
		rangeCount++
		if to == end {
			break
		}
		from = to + 1
	}
	totalJobs := rangeCount * uint64(len(queries))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				logs, err := c.GetLogs(ctx, item.from, item.to, item.q.topics)
				if err != nil {
					results <- result{err: fmt.Errorf("%s %d-%d: %w", item.q.name, item.from, item.to, err)}
					continue
				}
				results <- result{logs: logs}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for from := start; from <= end; {
			to := from + batch - 1
			if to > end || to < from {
				to = end
			}
			for _, q := range queries {
				select {
				case <-ctx.Done():
					return
				case jobs <- job{from: from, to: to, q: q}:
				}
			}
			if to == end {
				return
			}
			from = to + 1
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	seen := map[string]bool{}
	contractTokenIDs := map[string]map[string]bool{}
	blockTimes := map[string]string{}
	var (
		tokenRows  []map[string]any
		nftRows    []map[string]any
		errs       []error
		done       uint64
		lastReport = time.Now()
	)
	for res := range results {
		done++
		if res.err != nil {
			errs = append(errs, res.err)
			continue
		}
		for _, lg := range res.logs {
			key := strings.ToLower(lg.TransactionHash + ":" + lg.LogIndex)
			if seen[key] {
				continue
			}
			seen[key] = true
			tsHex := blockTimes[lg.BlockNumber]
			if tsHex == "" {
				blockNo, _ := hexToUint64(lg.BlockNumber)
				header, err := c.GetBlockHeaderByNumber(ctx, blockNo)
				if err != nil {
					errs = append(errs, fmt.Errorf("block header %s: %w", lg.BlockNumber, err))
				} else {
					tsHex = header.Timestamp
					blockTimes[lg.BlockNumber] = tsHex
				}
			}
			row, isNFT := tokenLogRow(address, chain, lg, tsHex)
			if row == nil {
				continue
			}
			contract := strings.ToLower(lg.Address)
			if contractTokenIDs[contract] == nil {
				contractTokenIDs[contract] = map[string]bool{}
			}
			if tokenID := toString(row["tokenId"]); tokenID != "" {
				for _, id := range strings.Split(tokenID, ",") {
					id = strings.TrimSpace(id)
					if id != "" {
						contractTokenIDs[contract][id] = true
					}
				}
			}
			if isNFT {
				nftRows = append(nftRows, row)
			} else {
				tokenRows = append(tokenRows, row)
			}
		}
		if progress != nil && (done == totalJobs || done%20 == 0 || time.Since(lastReport) >= 3*time.Second) {
			progress(fmt.Sprintf("链 %s: 日志扫描进度 %d/%d，代币 %d 行，NFT %d 行", chain.ChainShortName, done, totalJobs, len(tokenRows), len(nftRows)))
			lastReport = time.Now()
		}
	}
	return tokenRows, nftRows, contractTokenIDs, errors.Join(errs...)
}

func (c *EVMRPCClient) GetLogs(ctx context.Context, from, to uint64, topics []any) ([]RPCLog, error) {
	filter := map[string]any{
		"fromBlock": uint64ToHex(from),
		"toBlock":   uint64ToHex(to),
		"topics":    topics,
	}
	var raw json.RawMessage
	if err := c.callRaw(ctx, "eth_getLogs", []any{filter}, &raw); err != nil {
		return nil, err
	}
	var logs []RPCLog
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, err
	}
	for i := range logs {
		logRaw, _ := json.Marshal(logs[i])
		logs[i].Raw = logRaw
	}
	return logs, nil
}

func (c *EVMRPCClient) GetBlockHeaderByNumber(ctx context.Context, number uint64) (RPCBlockHeader, error) {
	var raw json.RawMessage
	if err := c.callRaw(ctx, "eth_getBlockByNumber", []any{uint64ToHex(number), false}, &raw); err != nil {
		return RPCBlockHeader{}, err
	}
	var block RPCBlockHeader
	if err := json.Unmarshal(raw, &block); err != nil {
		return RPCBlockHeader{}, err
	}
	block.Raw = raw
	return block, nil
}

func tokenLogRow(address string, chain RPCChainConfig, lg RPCLog, tsHex string) (map[string]any, bool) {
	if len(lg.Topics) == 0 {
		return nil, false
	}
	topic0 := strings.ToLower(lg.Topics[0])
	switch topic0 {
	case transferTopic:
		if len(lg.Topics) < 3 {
			return nil, false
		}
		from := topicToAddress(lg.Topics[1])
		to := topicToAddress(lg.Topics[2])
		isNFT := len(lg.Topics) >= 4
		tokenID := ""
		amount := uint256HexDataToDecimal(lg.Data)
		protocol := "token_20"
		if isNFT {
			tokenID = hexToDecimalString(lg.Topics[3])
			amount = "1"
			protocol = "token_721"
		}
		return commonTokenRow(address, chain, lg, protocol, from, to, amount, tokenID, tsHex, jsonCompactAny(lg)), isNFT
	case transferSingleTopic:
		if len(lg.Topics) < 4 {
			return nil, false
		}
		from := topicToAddress(lg.Topics[2])
		to := topicToAddress(lg.Topics[3])
		id, value := decodeERC1155Single(lg.Data)
		return commonTokenRow(address, chain, lg, "token_1155", from, to, value, id, tsHex, jsonCompactAny(lg)), true
	case transferBatchTopic:
		if len(lg.Topics) < 4 {
			return nil, false
		}
		from := topicToAddress(lg.Topics[2])
		to := topicToAddress(lg.Topics[3])
		ids, values := decodeERC1155Batch(lg.Data)
		return commonTokenRow(address, chain, lg, "token_1155", from, to, strings.Join(values, ","), strings.Join(ids, ","), tsHex, jsonCompactAny(lg)), true
	default:
		return nil, false
	}
}

func commonTokenRow(address string, chain RPCChainConfig, lg RPCLog, protocol, from, to, amount, tokenID, tsHex, raw string) map[string]any {
	return map[string]any{
		"targetAddress":        address,
		"chainFullName":        chain.ChainFullName,
		"chainShortName":       chain.ChainShortName,
		"protocolType":         protocol,
		"direction":            detectDirection(address, from, to),
		"txId":                 lg.TransactionHash,
		"blockHash":            lg.BlockHash,
		"height":               hexToDecimalString(lg.BlockNumber),
		"transactionTime":      hexToDecimalString(tsHex),
		"transactionTimeLocal": blockTimestampLocal(tsHex),
		"from":                 from,
		"to":                   to,
		"amount":               amount,
		"transactionSymbol":    "",
		"state":                "log",
		"tokenId":              tokenID,
		"tokenContractAddress": lg.Address,
		"logs":                 jsonCompactAny(lg),
		"rawJSON":              raw,
	}
}

func (c *EVMRPCClient) BuildRPCAssets(ctx context.Context, address string, chain RPCChainConfig, contracts map[string]map[string]bool) ([]map[string]any, error) {
	rows := []map[string]any{{
		"address":        address,
		"chainFullName":  chain.ChainFullName,
		"chainShortName": chain.ChainShortName,
		"assetType":      "native",
		"protocolType":   "native",
		"symbol":         chain.NativeSymbol,
		"holdingAmount":  "",
		"rawJSON":        jsonCompactAny(map[string]any{"source": "eth_getBalance"}),
	}}
	var errs []error
	for contract, tokenIDs := range contracts {
		standard, err := c.DetectTokenStandard(ctx, contract)
		if err != nil {
			errs = append(errs, fmt.Errorf("detect %s: %w", contract, err))
		}
		if standard == "token_1155" {
			for id := range tokenIDs {
				balance, err := c.ERC1155BalanceOf(ctx, contract, address, id)
				if err != nil {
					errs = append(errs, fmt.Errorf("erc1155 balance %s #%s: %w", contract, id, err))
				}
				rows = append(rows, map[string]any{
					"address":              address,
					"chainFullName":        chain.ChainFullName,
					"chainShortName":       chain.ChainShortName,
					"assetType":            "nft",
					"protocolType":         "token_1155",
					"holdingAmount":        balance,
					"tokenContractAddress": contract,
					"tokenId":              id,
				})
			}
			continue
		}
		balance, err := c.BalanceOf(ctx, contract, address)
		if err != nil {
			errs = append(errs, fmt.Errorf("balance %s: %w", contract, err))
		}
		assetType := "token"
		if standard == "token_721" {
			assetType = "nft"
		}
		rows = append(rows, map[string]any{
			"address":              address,
			"chainFullName":        chain.ChainFullName,
			"chainShortName":       chain.ChainShortName,
			"assetType":            assetType,
			"protocolType":         standard,
			"holdingAmount":        balance,
			"tokenContractAddress": contract,
		})
	}
	return rows, errors.Join(errs...)
}

func (c *EVMRPCClient) ScanInternalTransactions(ctx context.Context, address string, chain RPCChainConfig, start, end uint64, cfg Config) ([]map[string]any, error) {
	switch cfg.TraceMode {
	case "none":
		return nil, nil
	case "trace-filter", "auto":
		rows, err := c.TraceFilterAddress(ctx, address, chain, start, end, cfg.LogBatch, cfg.Progress)
		if err == nil || cfg.TraceMode == "trace-filter" {
			return rows, err
		}
		return rows, fmt.Errorf("trace_filter 不可用，内部交易未完整下载；如节点支持 debug_traceTransaction，可用 -trace-mode=debug-all，但会非常慢: %w", err)
	case "debug-all":
		return c.DebugTraceAllTransactions(ctx, address, chain, start, end, cfg.Progress)
	default:
		return nil, nil
	}
}

func (c *EVMRPCClient) TraceFilterAddress(ctx context.Context, address string, chain RPCChainConfig, start, end, batch uint64, progress func(string)) ([]map[string]any, error) {
	var (
		rows []map[string]any
		errs []error
		seen = map[string]bool{}
	)
	for from := start; from <= end; {
		to := from + batch - 1
		if to > end || to < from {
			to = end
		}
		for _, filter := range []traceFilterObject{
			{FromBlock: uint64ToHex(from), ToBlock: uint64ToHex(to), FromAddress: []string{address}},
			{FromBlock: uint64ToHex(from), ToBlock: uint64ToHex(to), ToAddress: []string{address}},
		} {
			traces, err := c.TraceFilter(ctx, filter)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, tr := range traces {
				key := traceKey(tr)
				if seen[key] {
					continue
				}
				seen[key] = true
				if row := traceRow(address, chain, tr); row != nil {
					rows = append(rows, row)
				}
			}
		}
		if progress != nil {
			progress(fmt.Sprintf("链 %s: trace_filter 进度 %d-%d，命中 %d 行", chain.ChainShortName, from, to, len(rows)))
		}
		if to == end {
			break
		}
		from = to + 1
	}
	return rows, errors.Join(errs...)
}

func (c *EVMRPCClient) TraceFilter(ctx context.Context, filter traceFilterObject) ([]RPCTrace, error) {
	var raw json.RawMessage
	if err := c.callRaw(ctx, "trace_filter", []any{filter}, &raw); err != nil {
		return nil, err
	}
	var traces []RPCTrace
	if err := json.Unmarshal(raw, &traces); err != nil {
		return nil, err
	}
	for i := range traces {
		trRaw, _ := json.Marshal(traces[i])
		traces[i].Raw = trRaw
	}
	return traces, nil
}

func (c *EVMRPCClient) DebugTraceAllTransactions(ctx context.Context, address string, chain RPCChainConfig, start, end uint64, progress func(string)) ([]map[string]any, error) {
	var (
		rows []map[string]any
		errs []error
	)
	total := end - start + 1
	var done uint64
	lastReport := time.Now()
	for n := start; n <= end; n++ {
		done++
		block, err := c.GetBlockByNumber(ctx, n)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, tx := range block.Transactions {
			frame, err := c.DebugTraceTransaction(ctx, tx.Hash)
			if err != nil {
				errs = append(errs, fmt.Errorf("debug trace %s: %w", tx.Hash, err))
				continue
			}
			rows = append(rows, callFrameRows(address, chain, tx.Hash, hexToDecimalString(tx.BlockNumber), frame)...)
		}
		if progress != nil && (done == total || done%10 == 0 || time.Since(lastReport) >= 3*time.Second) {
			progress(fmt.Sprintf("链 %s: debug trace 区块进度 %d/%d，命中 %d 行", chain.ChainShortName, done, total, len(rows)))
			lastReport = time.Now()
		}
	}
	return rows, errors.Join(errs...)
}

func (c *EVMRPCClient) DebugTraceTransaction(ctx context.Context, txHash string) (callFrame, error) {
	var frame callFrame
	params := []any{txHash, map[string]any{"tracer": "callTracer", "timeout": "30s"}}
	if err := c.call(ctx, "debug_traceTransaction", params, &frame); err != nil {
		return callFrame{}, err
	}
	return frame, nil
}

func traceRow(address string, chain RPCChainConfig, tr RPCTrace) map[string]any {
	from := toString(tr.Action["from"])
	to := toString(tr.Action["to"])
	value := toString(tr.Action["value"])
	if !sameAddress(address, from) && !sameAddress(address, to) {
		return nil
	}
	return map[string]any{
		"targetAddress":     address,
		"chainFullName":     chain.ChainFullName,
		"chainShortName":    chain.ChainShortName,
		"protocolType":      "internal",
		"direction":         detectDirection(address, from, to),
		"txId":              tr.TransactionHash,
		"height":            strconv.FormatInt(tr.BlockNumber, 10),
		"from":              from,
		"to":                to,
		"amount":            weiHexToDecimal(value),
		"transactionSymbol": chain.NativeSymbol,
		"state":             firstNonEmpty(tr.Error, "success"),
		"inputdate":         toString(tr.Action["input"]),
		"logs":              jsonCompactAny(tr),
		"rawJSON":           jsonCompactAny(tr),
	}
}

func callFrameRows(address string, chain RPCChainConfig, txHash, height string, frame callFrame) []map[string]any {
	var rows []map[string]any
	var walk func(callFrame)
	walk = func(cf callFrame) {
		if sameAddress(address, cf.From) || sameAddress(address, cf.To) {
			rows = append(rows, map[string]any{
				"targetAddress":     address,
				"chainFullName":     chain.ChainFullName,
				"chainShortName":    chain.ChainShortName,
				"protocolType":      "internal",
				"direction":         detectDirection(address, cf.From, cf.To),
				"txId":              txHash,
				"height":            height,
				"from":              cf.From,
				"to":                cf.To,
				"amount":            weiHexToDecimal(cf.Value),
				"transactionSymbol": chain.NativeSymbol,
				"state":             firstNonEmpty(cf.Error, "success"),
				"inputdate":         cf.Input,
				"logs":              jsonCompactAny(cf),
				"rawJSON":           jsonCompactAny(cf),
			})
		}
		for _, child := range cf.Calls {
			walk(child)
		}
	}
	walk(frame)
	return rows
}

func (c *EVMRPCClient) DetectTokenStandard(ctx context.Context, contract string) (string, error) {
	ok1155, _ := c.SupportsInterface(ctx, contract, "d9b67a26")
	if ok1155 {
		return "token_1155", nil
	}
	ok721, _ := c.SupportsInterface(ctx, contract, "80ac58cd")
	if ok721 {
		return "token_721", nil
	}
	return "token_20", nil
}

func (c *EVMRPCClient) SupportsInterface(ctx context.Context, contract, interfaceID string) (bool, error) {
	data := "0x01ffc9a7" + strings.Repeat("0", 56) + strings.TrimPrefix(interfaceID, "0x")
	var out string
	err := c.call(ctx, "eth_call", []any{map[string]any{"to": contract, "data": data}, "latest"}, &out)
	if err != nil {
		return false, err
	}
	return hexToBig(out).Sign() > 0, nil
}

func (c *EVMRPCClient) BalanceOf(ctx context.Context, contract, address string) (string, error) {
	data := "0x70a08231" + strings.TrimPrefix(addressToTopic(address), "0x")
	var out string
	if err := c.call(ctx, "eth_call", []any{map[string]any{"to": contract, "data": data}, "latest"}, &out); err != nil {
		return "", err
	}
	return hexToBig(out).String(), nil
}

func (c *EVMRPCClient) ERC1155BalanceOf(ctx context.Context, contract, address, tokenID string) (string, error) {
	data := "0x00fdd58e" + strings.TrimPrefix(addressToTopic(address), "0x") + uint256DecimalToTopic(tokenID)
	var out string
	if err := c.call(ctx, "eth_call", []any{map[string]any{"to": contract, "data": data}, "latest"}, &out); err != nil {
		return "", err
	}
	return hexToBig(out).String(), nil
}

func (c *EVMRPCClient) call(ctx context.Context, method string, params []any, out any) error {
	var raw json.RawMessage
	if err := c.callRaw(ctx, method, params, &raw); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *EVMRPCClient) callRaw(ctx context.Context, method string, params []any, raw *json.RawMessage) error {
	var lastErr error
	for urlIdx, url := range c.rpcURLs {
		for attempt := 0; attempt <= c.retries; attempt++ {
			if attempt > 0 && urlIdx == 0 {
				backoffSeconds := 1 << attempt
				if backoffSeconds > 30 {
					backoffSeconds = 30
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(backoffSeconds) * time.Second):
				}
			}
			if err := c.limiter.Wait(ctx); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case c.sem <- struct{}{}:
			}
			body, status, err := c.doRPCCallURL(ctx, url, method, params)
			<-c.sem
			if err != nil {
				lastErr = err
				continue
			}
			if status == http.StatusTooManyRequests || status >= 500 {
				lastErr = fmt.Errorf("HTTP %d: %s", status, truncate(body, 500))
				if status == http.StatusTooManyRequests && urlIdx+1 < len(c.rpcURLs) {
					break
				}
				continue
			}
			if status < 200 || status >= 300 {
				return fmt.Errorf("HTTP %d: %s", status, truncate(body, 1000))
			}
			var resp rpcResponse
			if err := json.Unmarshal(body, &resp); err != nil {
				return fmt.Errorf("RPC JSON解析失败: %w; body=%s", err, truncate(body, 500))
			}
			if resp.Error != nil {
				lastErr = fmt.Errorf("RPC %s code=%d msg=%s", method, resp.Error.Code, resp.Error.Message)
				if shouldTryNextRPC(resp.Error.Code, resp.Error.Message) && urlIdx+1 < len(c.rpcURLs) {
					break
				}
				if isRPCRetryableError(resp.Error.Code, resp.Error.Message) && attempt < c.retries {
					continue
				}
				return lastErr
			}
			*raw = resp.Result
			return nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("unknown rpc error")
	}
	return lastErr
}

func shouldTryNextRPC(code int, msg string) bool {
	lower := strings.ToLower(msg)
	if isArchiveErrorMsg(lower) {
		return true
	}
	return isRPCLimitError(code, msg) || isRPCProviderCapabilityError(lower)
}

func isRPCLimitError(code int, msg string) bool {
	lower := strings.ToLower(msg)
	if isArchiveErrorMsg(lower) {
		return false
	}
	if code == -32001 || code == -32005 {
		return true
	}
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "reached the limit") ||
		strings.Contains(lower, "too many request") ||
		strings.Contains(lower, "limited to") ||
		strings.Contains(lower, "limit exceeded") ||
		strings.Contains(lower, "block range")
}

func isRPCProviderCapabilityError(lower string) bool {
	return strings.Contains(lower, "please specify an address") ||
		strings.Contains(lower, "method not found") ||
		strings.Contains(lower, "method not available") ||
		strings.Contains(lower, "method is not available") ||
		strings.Contains(lower, "not supported")
}

func isArchiveError(err error) bool {
	if err == nil {
		return false
	}
	return isArchiveErrorMsg(strings.ToLower(err.Error()))
}

func isArchiveErrorMsg(lower string) bool {
	return strings.Contains(lower, "historical state") ||
		strings.Contains(lower, "has been pruned") ||
		strings.Contains(lower, "history has been pruned")
}

func isRPCRetryableError(code int, msg string) bool {
	lower := strings.ToLower(msg)
	if isArchiveErrorMsg(lower) {
		return false
	}
	return code == -32000 || code == -32001 || code == -32005 ||
		code == -32603 || code == -32002 ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "too many")
}

func (c *EVMRPCClient) doRPCCallURL(ctx context.Context, url, method string, params []any) ([]byte, int, error) {
	if params == nil {
		params = []any{}
	}
	reqBody, _ := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.id.Add(1),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", useragent.Random())
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

func (c *EVMRPCClient) doRPCCall(ctx context.Context, method string, params []any) ([]byte, int, error) {
	if len(c.rpcURLs) == 0 {
		return nil, 0, errors.New("no rpc url configured")
	}
	return c.doRPCCallURL(ctx, c.rpcURLs[0], method, params)
}

func (c *EVMRPCClient) writeRaw(chain, kind, name string, body []byte) error {
	if c.rawDir == "" || len(body) == 0 {
		return nil
	}
	dir := filepath.Join(c.rawDir, sanitizeFilePart(chain))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sanitizeFilePart(kind)+"_"+sanitizeFilePart(name)+".json"), body, 0644)
}

func addressInTx(address string, tx RPCTransaction) bool {
	return sameAddress(address, tx.From) || sameAddress(address, tx.To)
}

func sameAddress(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) && strings.TrimSpace(a) != ""
}

func methodID(input string) string {
	if len(input) >= 10 {
		return input[:10]
	}
	return input
}

func receiptStatus(status string) string {
	switch strings.ToLower(status) {
	case "0x1", "1":
		return "success"
	case "0x0", "0":
		return "failed"
	default:
		return status
	}
}

func txFeeWei(receipt RPCReceipt, tx RPCTransaction) *big.Int {
	gasUsed := hexToBig(receipt.GasUsed)
	price := hexToBig(firstNonEmpty(receipt.EffectiveGasPrice, tx.GasPrice))
	return new(big.Int).Mul(gasUsed, price)
}

func hexToUint64(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 16, 64)
}

func hexToDecimalString(s string) string {
	return hexToBig(s).String()
}

func hexToBig(s string) *big.Int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return big.NewInt(0)
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 16); ok {
		return n
	}
	return big.NewInt(0)
}

func uint64ToHex(n uint64) string {
	return fmt.Sprintf("0x%x", n)
}

func weiHexToDecimal(s string) string {
	return weiBigToDecimal(hexToBig(s))
}

func weiBigToDecimal(n *big.Int) string {
	if n == nil {
		return ""
	}
	rat := new(big.Rat).SetFrac(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	return rat.FloatString(18)
}

func addressToTopic(address string) string {
	addr := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	return "0x" + strings.Repeat("0", 64-len(addr)) + addr
}

func topicToAddress(topic string) string {
	t := strings.TrimPrefix(topic, "0x")
	if len(t) < 40 {
		return ""
	}
	return "0x" + t[len(t)-40:]
}

func uint256HexDataToDecimal(data string) string {
	return hexToBig(data).String()
}

func uint256DecimalToTopic(v string) string {
	n := new(big.Int)
	if _, ok := n.SetString(strings.TrimSpace(v), 10); !ok {
		n = big.NewInt(0)
	}
	return fmt.Sprintf("%064x", n)
}

func decodeERC1155Single(data string) (string, string) {
	raw := strings.TrimPrefix(data, "0x")
	if len(raw) < 128 {
		return "", ""
	}
	id := hexToBig("0x" + raw[:64]).String()
	value := hexToBig("0x" + raw[64:128]).String()
	return id, value
}

func decodeERC1155Batch(data string) ([]string, []string) {
	raw := strings.TrimPrefix(data, "0x")
	if len(raw) < 128 {
		return nil, nil
	}
	idOffset := int(hexToBig("0x"+raw[:64]).Int64()) * 2
	valueOffset := int(hexToBig("0x"+raw[64:128]).Int64()) * 2
	ids := decodeUint256Array(raw, idOffset)
	values := decodeUint256Array(raw, valueOffset)
	return ids, values
}

func decodeUint256Array(raw string, offset int) []string {
	if offset < 0 || offset+64 > len(raw) {
		return nil
	}
	length := int(hexToBig("0x" + raw[offset:offset+64]).Int64())
	values := make([]string, 0, length)
	pos := offset + 64
	for i := 0; i < length && pos+64 <= len(raw); i++ {
		values = append(values, hexToBig("0x"+raw[pos:pos+64]).String())
		pos += 64
	}
	return values
}

func blockTimestampLocal(hexTs string) string {
	sec, err := hexToUint64(hexTs)
	if err != nil {
		return ""
	}
	return time.Unix(int64(sec), 0).Local().Format("2006-01-02 15:04:05")
}

func jsonCompactAny(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func traceKey(tr RPCTrace) string {
	parts := make([]string, 0, len(tr.TraceAddress))
	for _, p := range tr.TraceAddress {
		parts = append(parts, strconv.Itoa(p))
	}
	return strings.ToLower(tr.TransactionHash + ":" + strings.Join(parts, "."))
}

func parseHexBytes(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	b, _ := hex.DecodeString(s)
	return b
}

func reportProgress(cfg Config, format string, args ...any) {
	if cfg.Progress == nil {
		return
	}
	cfg.Progress(fmt.Sprintf(format, args...))
}

func eventTopic(signature string) string {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte(signature))
	return "0x" + hex.EncodeToString(h.Sum(nil))
}
