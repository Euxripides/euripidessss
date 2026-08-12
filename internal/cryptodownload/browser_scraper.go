package cryptodownload

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/etl/backend/internal/cryptodownload/useragent"
)

const browserPublicAPIKey = "a2c903cc-b31e-4547-9299-b6d07b7631ab"

type BrowserScraperClient struct {
	baseURL        string
	address        string
	httpClient     *http.Client
	timingObserver requestTimingObserver
	limiter        *RateLimiter
	sem            chan struct{}
	retries        int
	rawDir         string
}

type browserAPIResponse struct {
	Code      int             `json:"code"`
	Msg       string          `json:"msg"`
	DetailMsg string          `json:"detailMsg"`
	Data      json.RawMessage `json:"data"`
}

type browserListPage struct {
	Total int64             `json:"total"`
	Hits  []json.RawMessage `json:"hits"`
}

type browserPageResult struct {
	offset         int
	total          int64
	hits           int
	firstBlockTime int64
	rows           []map[string]any
	err            error
}

func NewBrowserScraperClient(cfg Config) *BrowserScraperClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	workers := cfg.Workers
	if workers < 1 {
		workers = 1
	}
	retries := cfg.Retries
	if strings.EqualFold(cfg.Source, "browser") && retries < 6 {
		retries = 6
	}
	return &BrowserScraperClient{
		baseURL:    baseURL,
		address:    strings.ToLower(strings.TrimSpace(cfg.Address)),
		httpClient: newSharedHTTPClient(cfg.Timeout),
		limiter:    NewRateLimiter(cfg.RPS),
		sem:        make(chan struct{}, workers),
		retries:    retries,
		rawDir:     cfg.RawDir,
	}
}

func collectAllFromBrowser(ctx context.Context, cfg Config) ExportData {
	client := NewBrowserScraperClient(cfg)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		data ExportData
	)
	for _, chain := range cfg.Chains {
		chain := strings.ToUpper(strings.TrimSpace(chain))
		if chain == "" {
			continue
		}
		reportProgress(cfg, "浏览器爬取 %s: 已启动", chain)
		wg.Add(1)
		go func() {
			defer wg.Done()
			chainData, err := client.CollectAddressBrowser(ctx, cfg, chain)
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
	addNativeAssets(&data)
	fillSummaryCounters(&data)
	sortExportData(&data)
	return data
}

func (c *BrowserScraperClient) CollectAddress(ctx context.Context, cfg Config, chain string) (ExportData, error) {
	var data ExportData
	chainLower := strings.ToLower(chain)
	nativeSymbol := firstNonEmpty(cfg.NativeSymbol, nativeSymbolForChain(chain), chain)
	reportProgress(cfg, "浏览器爬取 %s: 地址摘要", chain)
	summary, err := c.FetchSummary(ctx, cfg.Address, chainLower, nativeSymbol)
	if err != nil {
		data.Errors = append(data.Errors, fmt.Sprintf("browser summary %s: %v", chain, err))
	} else {
		data.Summaries = append(data.Summaries, summary)
	}

	protocolSet := protocolClassSet(cfg.Protocols)
	if protocolSet["transaction"] {
		reportProgress(cfg, "浏览器爬取 %s: 普通交易", chain)
		txRows, err := c.FetchPagedRows(ctx, chainLower, fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transactionsByClassfy/condition", chainLower, cfg.Address), cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
			return browserTxRow(cfg.Address, chain, nativeSymbol, raw, "transaction")
		})
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("browser transactions %s: %v", chain, err))
		}
		data.Transactions = append(data.Transactions, txRows...)
		data.Funds = append(data.Funds, buildFundRows(txRows)...)
	}

	if protocolSet["internal"] {
		reportProgress(cfg, "浏览器爬取 %s: 内部交易", chain)
		internalRows, err := c.FetchPagedRows(ctx, chainLower, fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/internalTx/condition", chainLower, cfg.Address), cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
			return browserTxRow(cfg.Address, chain, nativeSymbol, raw, "internal")
		})
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("browser internal %s: %v", chain, err))
		}
		data.Internals = append(data.Internals, internalRows...)
		data.Funds = append(data.Funds, buildFundRows(internalRows)...)
	}

	if protocolSet["token"] {
		reportProgress(cfg, "浏览器爬取 %s: 代币转账", chain)
		tokenRows, err := c.FetchPagedRows(ctx, chainLower, fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transfers/condition/token", chainLower, cfg.Address), cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
			return browserTransferRow(cfg.Address, chain, raw, false)
		})
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("browser token transfers %s: %v", chain, err))
		}
		data.TokenTransfers = append(data.TokenTransfers, tokenRows...)
		data.Funds = append(data.Funds, buildFundRows(tokenRows)...)
	}

	if protocolSet["nft"] {
		reportProgress(cfg, "浏览器爬取 %s: NFT 转账", chain)
		nftURL := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transfers/condition/nft", chainLower, cfg.Address)
		nftRows, err := c.FetchPagedRowsWithBaseParams(ctx, chainLower, nftURL, url.Values{"tokenTypes": []string{browserNFTTokenTypes(chain)}}, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
			return browserTransferRow(cfg.Address, chain, raw, true)
		})
		if err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("browser nft transfers %s: %v", chain, err))
		}
		data.NFTTransfers = append(data.NFTTransfers, nftRows...)
		data.Funds = append(data.Funds, buildFundRows(nftRows)...)
	}

	reportProgress(cfg, "浏览器爬取 %s: 资产", chain)
	assetURL := fmt.Sprintf("/api/explorer/v2/%s/addresses/asset/realtime/tokenList", chainLower)
	assetRows, err := c.FetchPagedRowsWithBaseParams(ctx, chainLower, assetURL, url.Values{
		"address": []string{cfg.Address},
		"chain":   []string{chainLower},
	}, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
		return browserAssetRow(cfg.Address, chain, raw)
	})
	if err != nil {
		data.Errors = append(data.Errors, fmt.Sprintf("browser assets %s: %v", chain, err))
	}
	data.Assets = append(data.Assets, assetRows...)

	return data, nil
}

func (c *BrowserScraperClient) CollectAddressConcurrent(ctx context.Context, cfg Config, chain string) (ExportData, error) {
	var data ExportData
	chainLower := strings.ToLower(chain)
	nativeSymbol := firstNonEmpty(cfg.NativeSymbol, nativeSymbolForChain(chain), chain)
	protocolSet := protocolClassSet(cfg.Protocols)

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	addError := func(format string, args ...any) {
		mu.Lock()
		data.Errors = append(data.Errors, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		reportProgress(cfg, "浏览器爬取 %s: 地址摘要", chain)
		summary, err := c.FetchSummary(ctx, cfg.Address, chainLower, nativeSymbol)
		if err != nil {
			addError("browser summary %s: %v", chain, err)
			return
		}
		mu.Lock()
		data.Summaries = append(data.Summaries, summary)
		mu.Unlock()
	}()

	if protocolSet["transaction"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reportProgress(cfg, "浏览器爬取 %s: 普通交易", chain)
			txRows, err := c.FetchPagedRowsConcurrent(ctx, chainLower, fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transactionsByClassfy/condition", chainLower, cfg.Address), cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTxRow(cfg.Address, chain, nativeSymbol, raw, "transaction")
			})
			if err != nil {
				addError("browser transactions %s: %v", chain, err)
			}
			mu.Lock()
			data.Transactions = append(data.Transactions, txRows...)
			data.Funds = append(data.Funds, buildFundRows(txRows)...)
			mu.Unlock()
		}()
	}

	if protocolSet["internal"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reportProgress(cfg, "浏览器爬取 %s: 内部交易", chain)
			internalRows, err := c.FetchInternalByTimeWindow(ctx, cfg, chainLower, cfg.Address, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTxRow(cfg.Address, chain, nativeSymbol, raw, "internal")
			})
			if err != nil {
				addError("browser internal %s: %v", chain, err)
			}
			mu.Lock()
			data.Internals = append(data.Internals, internalRows...)
			data.Funds = append(data.Funds, buildFundRows(internalRows)...)
			mu.Unlock()
		}()
	}

	if protocolSet["token"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reportProgress(cfg, "浏览器爬取 %s: 代币转账", chain)
			tokenRows, err := c.FetchPagedRowsConcurrent(ctx, chainLower, fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transfers/condition/token", chainLower, cfg.Address), cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTransferRow(cfg.Address, chain, raw, false)
			})
			if err != nil {
				addError("browser token transfers %s: %v", chain, err)
			}
			mu.Lock()
			data.TokenTransfers = append(data.TokenTransfers, tokenRows...)
			data.Funds = append(data.Funds, buildFundRows(tokenRows)...)
			mu.Unlock()
		}()
	}

	if protocolSet["nft"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reportProgress(cfg, "浏览器爬取 %s: NFT 转账", chain)
			nftURL := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transfers/condition/nft", chainLower, cfg.Address)
			nftRows, err := c.FetchPagedRowsConcurrentWithBaseParams(ctx, chainLower, nftURL, url.Values{"tokenTypes": []string{browserNFTTokenTypes(chain)}}, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTransferRow(cfg.Address, chain, raw, true)
			})
			if err != nil {
				addError("browser nft transfers %s: %v", chain, err)
			}
			mu.Lock()
			data.NFTTransfers = append(data.NFTTransfers, nftRows...)
			data.Funds = append(data.Funds, buildFundRows(nftRows)...)
			mu.Unlock()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		reportProgress(cfg, "浏览器爬取 %s: 资产", chain)
		assetURL := fmt.Sprintf("/api/explorer/v2/%s/addresses/asset/realtime/tokenList", chainLower)
		assetRows, err := c.FetchPagedRowsConcurrentWithBaseParams(ctx, chainLower, assetURL, url.Values{
			"address": []string{cfg.Address},
			"chain":   []string{chainLower},
		}, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
			return browserAssetRow(cfg.Address, chain, raw)
		})
		if err != nil {
			addError("browser assets %s: %v", chain, err)
		}
		mu.Lock()
		data.Assets = append(data.Assets, assetRows...)
		mu.Unlock()
	}()

	wg.Wait()
	return data, nil
}

func (c *BrowserScraperClient) CollectAddressBrowser(ctx context.Context, cfg Config, chain string) (ExportData, error) {
	var data ExportData
	chainLower := strings.ToLower(chain)
	nativeSymbol := firstNonEmpty(cfg.NativeSymbol, nativeSymbolForChain(chain), chain)
	protocolSet := protocolClassSet(cfg.Protocols)
	fetchMetadata := browserShouldFetchMetadata(cfg)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		sheetSem = make(chan struct{}, browserSheetConcurrency(cfg.Workers))
	)
	addError := func(format string, args ...any) {
		mu.Lock()
		data.Errors = append(data.Errors, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	runSheet := func(name string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				addError("browser %s %s: %v", name, chain, ctx.Err())
				return
			case sheetSem <- struct{}{}:
			}
			defer func() { <-sheetSem }()
			fn()
		}()
	}

	if fetchMetadata {
		runSheet("summary", func() {
			reportProgress(cfg, "浏览器爬取 %s: 地址摘要", chain)
			summary, err := c.FetchSummary(ctx, cfg.Address, chainLower, nativeSymbol)
			if err != nil {
				addError("browser summary %s: %v", chain, err)
				return
			}
			mu.Lock()
			data.Summaries = append(data.Summaries, summary)
			mu.Unlock()
		})
	}

	if protocolSet["transaction"] {
		runSheet("transactions", func() {
			reportProgress(cfg, "浏览器爬取 %s: 普通交易", chain)
			txRows, err := c.FetchTransactionsByTimeWindow(ctx, cfg, chainLower, cfg.Address, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTxRow(cfg.Address, chain, nativeSymbol, raw, "transaction")
			})
			if err != nil {
				addError("browser transactions %s: %v", chain, err)
			}
			txRows = dedupeExactBrowserRows(cfg, chain, "transactions", txRows)
			mu.Lock()
			data.Transactions = append(data.Transactions, txRows...)
			data.Funds = append(data.Funds, buildFundRows(txRows)...)
			mu.Unlock()
		})
	}

	if protocolSet["internal"] {
		runSheet("internal", func() {
			reportProgress(cfg, "浏览器爬取 %s: 内部交易", chain)
			internalRows, err := c.FetchInternalByTimeWindow(ctx, cfg, chainLower, cfg.Address, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTxRow(cfg.Address, chain, nativeSymbol, raw, "internal")
			})
			if err != nil {
				addError("browser internal %s: %v", chain, err)
			}
			internalRows = dedupeExactBrowserRows(cfg, chain, "internal", internalRows)
			mu.Lock()
			data.Internals = append(data.Internals, internalRows...)
			data.Funds = append(data.Funds, buildFundRows(internalRows)...)
			mu.Unlock()
		})
	}

	if protocolSet["token"] {
		runSheet("token transfers", func() {
			reportProgress(cfg, "浏览器爬取 %s: 代币转账", chain)
			tokenRows, err := c.FetchTokenTransfersByTimeWindow(ctx, cfg, chainLower, cfg.Address, csvTokenTypeForChain(chain), cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTransferRow(cfg.Address, chain, raw, false)
			})
			if err != nil {
				addError("browser token transfers %s: %v", chain, err)
			}
			tokenRows = dedupeExactBrowserRows(cfg, chain, "token_transfers", tokenRows)
			mu.Lock()
			data.TokenTransfers = append(data.TokenTransfers, tokenRows...)
			data.Funds = append(data.Funds, buildFundRows(tokenRows)...)
			mu.Unlock()
		})
	}

	if protocolSet["nft"] {
		runSheet("nft transfers", func() {
			reportProgress(cfg, "浏览器爬取 %s: NFT 转账", chain)
			nftURL := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transfers/condition/nft", chainLower, cfg.Address)
			nftRows, err := c.FetchPagedRowsConcurrentWithBaseParams(ctx, chainLower, nftURL, url.Values{"tokenTypes": []string{browserNFTTokenTypes(chain)}}, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserTransferRow(cfg.Address, chain, raw, true)
			})
			if err != nil {
				addError("browser nft transfers %s: %v", chain, err)
			}
			nftRows = dedupeExactBrowserRows(cfg, chain, "nft_transfers", nftRows)
			mu.Lock()
			data.NFTTransfers = append(data.NFTTransfers, nftRows...)
			data.Funds = append(data.Funds, buildFundRows(nftRows)...)
			mu.Unlock()
		})
	}

	if fetchMetadata {
		runSheet("assets", func() {
			reportProgress(cfg, "浏览器爬取 %s: 资产", chain)
			assetURL := fmt.Sprintf("/api/explorer/v2/%s/addresses/asset/realtime/tokenList", chainLower)
			assetRows, err := c.FetchPagedRowsConcurrentWithBaseParams(ctx, chainLower, assetURL, url.Values{
				"address": []string{cfg.Address},
				"chain":   []string{chainLower},
			}, cfg.PageSize, cfg.Progress, func(raw json.RawMessage) map[string]any {
				return browserAssetRow(cfg.Address, chain, raw)
			})
			if err != nil {
				addError("browser assets %s: %v", chain, err)
			}
			mu.Lock()
			data.Assets = append(data.Assets, assetRows...)
			mu.Unlock()
		})
	}

	wg.Wait()
	return data, nil
}

// dedupeExactBrowserRows removes only byte-for-byte-equivalent normalized rows.
// It intentionally keeps rows that share a transaction hash because one
// transaction may contain multiple token, NFT, or internal-transfer events.
func dedupeExactBrowserRows(cfg Config, chain, kind string, rows []map[string]any) []map[string]any {
	if len(rows) < 2 {
		return rows
	}
	seen := make(map[string]struct{}, len(rows))
	unique := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			// A browser row is JSON-derived and should always marshal. Preserve it
			// if a future mapper introduces an unsupported value type.
			unique = append(unique, row)
			continue
		}
		key := string(encoded)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, row)
	}
	if dropped := len(rows) - len(unique); dropped > 0 {
		reportProgress(cfg, "浏览器爬取 %s: %s 原始 %d 行，移除 %d 条完全重复记录，保留 %d 行", strings.ToUpper(chain), kind, len(rows), dropped, len(unique))
	}
	return unique
}

func browserSheetConcurrency(workers int) int {
	if workers <= 1 {
		return 1
	}
	limit := workers / 2
	if limit < 2 {
		limit = 2
	}
	if limit > 3 {
		limit = 3
	}
	return limit
}

func (c *BrowserScraperClient) FetchSummary(ctx context.Context, address, chain, nativeSymbol string) (map[string]any, error) {
	path := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s", chain, address)
	if body, err := c.readRaw(chain, "summary", "address"); err == nil {
		if raw, err := parseBrowserAPIBody(body); err == nil {
			if row, err := browserSummaryRow(raw, address, chain, nativeSymbol); err == nil {
				return row, nil
			}
		}
	}
	raw, body, err := c.getRaw(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	row, err := browserSummaryRow(raw, address, chain, nativeSymbol)
	if err != nil {
		return nil, err
	}
	_ = c.writeRaw(chain, "summary", "address", body)
	return row, nil
}

func browserSummaryRow(raw json.RawMessage, address, chain, nativeSymbol string) (map[string]any, error) {
	row := map[string]any{}
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	row["address"] = firstNonEmpty(toString(row["address"]), address)
	row["chainFullName"] = firstNonEmpty(toString(row["chainFullName"]), strings.ToUpper(chain))
	row["chainShortName"] = strings.ToUpper(chain)
	row["balanceSymbol"] = nativeSymbol
	row["exportedAt"] = time.Now().Format("2006-01-02 15:04:05")
	row["rawJSON"] = compactJSON(raw)
	return row, nil
}

func (c *BrowserScraperClient) FetchPagedRows(ctx context.Context, chain, path string, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	return c.FetchPagedRowsWithBaseParams(ctx, chain, path, nil, limit, progress, mapper)
}

func (c *BrowserScraperClient) FetchPagedRowsWithBaseParams(ctx context.Context, chain, path string, baseParams url.Values, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	rows := []map[string]any{}
	offset := 0
	total := int64(-1)
	for {
		params := cloneValues(baseParams)
		params.Set("offset", strconv.Itoa(offset))
		params.Set("limit", strconv.Itoa(limit))
		raw, body, err := c.getRaw(ctx, path, params)
		if err != nil {
			return rows, err
		}
		page := browserListPage{}
		if err := json.Unmarshal(raw, &page); err != nil {
			return rows, err
		}
		if total < 0 {
			total = page.Total
		}
		for _, hit := range page.Hits {
			row := mapper(hit)
			if row != nil {
				rows = append(rows, row)
			}
		}
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: %s %d/%d", strings.ToUpper(chain), pathBase(path), len(rows), total))
		}
		_ = c.writeRaw(chain, pathBase(path), fmt.Sprintf("offset_%09d", offset), body)
		offset += limit
		if len(page.Hits) == 0 || int64(offset) >= total {
			break
		}
	}
	return rows, nil
}

func (c *BrowserScraperClient) FetchPagedRowsConcurrent(ctx context.Context, chain, path string, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	return c.FetchPagedRowsConcurrentWithBaseParams(ctx, chain, path, nil, limit, progress, mapper)
}

func (c *BrowserScraperClient) FetchPagedRowsConcurrentWithBaseParams(ctx context.Context, chain, path string, baseParams url.Values, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	kind := browserCacheKind(path)

	fetchPage := func(offset int) browserPageResult {
		name := browserPageCacheName(offset, limit)
		if body, err := c.readRaw(chain, kind, name); err == nil {
			if result, err := mapBrowserPage(body, offset, mapper); err == nil {
				return result
			}
		}
		params := cloneValues(baseParams)
		params.Set("offset", strconv.Itoa(offset))
		params.Set("limit", strconv.Itoa(limit))
		raw, body, err := c.getRaw(ctx, path, params)
		if err != nil {
			return browserPageResult{offset: offset, err: err}
		}
		page := browserListPage{}
		if err := json.Unmarshal(raw, &page); err != nil {
			return browserPageResult{offset: offset, err: err}
		}
		rows := make([]map[string]any, 0, len(page.Hits))
		for _, hit := range page.Hits {
			row := mapper(hit)
			if row != nil {
				rows = append(rows, row)
			}
		}
		_ = c.writeRaw(chain, kind, name, body)
		return browserPageResult{offset: offset, total: page.Total, hits: len(page.Hits), rows: rows}
	}

	first := fetchPage(0)
	rows := make([]map[string]any, 0, len(first.rows))
	if first.err != nil {
		return rows, first.err
	}
	total := first.total
	if total <= 0 {
		total = int64(len(first.rows))
	}
	rows = append(rows, first.rows...)
	totalPages := int((total + int64(limit) - 1) / int64(limit))
	if totalPages < 1 {
		totalPages = 1
	}
	completedPages := 1
	fetchedHits := first.hits
	if progress != nil {
		progress(fmt.Sprintf("浏览器爬取 %s: %s %d/%d 页，已取 %d/%d 行", strings.ToUpper(chain), kind, completedPages, totalPages, fetchedHits, total))
	}
	if len(first.rows) == 0 || int64(limit) >= total {
		if int64(fetchedHits) < total {
			return rows, fmt.Errorf("%s incomplete: got %d/%d rows", kind, fetchedHits, total)
		}
		return rows, nil
	}

	offsets := make([]int, 0, totalPages-1)
	for offset := limit; int64(offset) < total; offset += limit {
		offsets = append(offsets, offset)
	}
	workers := cap(c.sem)
	if workers < 1 {
		workers = 1
	}
	if workers > len(offsets) {
		workers = len(offsets)
	}

	pageResults, errs := fetchBrowserPagesInBatches(ctx, offsets, workers, fetchPage, func(result browserPageResult) {
		completedPages++
		if result.err == nil {
			fetchedHits += result.hits
		}
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: %s %d/%d 页，已取 %d/%d 行", strings.ToUpper(chain), kind, completedPages, totalPages, fetchedHits, total))
		}
		// Save checkpoint periodically so interrupted runs can resume.
		if completedPages%5 == 0 || completedPages == totalPages {
			_ = SaveBrowserCheckpoint(c.rawDir, BrowserCheckpoint{
				Chain:      chain,
				Kind:       kind,
				LastOffset: result.offset + limit,
				Total:      total,
			})
		}
	})
	sort.SliceStable(pageResults, func(i, j int) bool {
		return pageResults[i].offset < pageResults[j].offset
	})
	for _, result := range pageResults {
		rows = append(rows, result.rows...)
	}
	if int64(fetchedHits) < total {
		errs = append(errs, fmt.Errorf("%s incomplete: got %d/%d rows", kind, fetchedHits, total))
	}
	if len(errs) > 0 {
		return rows, errors.Join(errs...)
	}
	return rows, nil
}

func mapBrowserPage(body []byte, offset int, mapper func(json.RawMessage) map[string]any) (browserPageResult, error) {
	raw, err := parseBrowserAPIBody(body)
	if err != nil {
		return browserPageResult{}, err
	}
	page := browserListPage{}
	if err := json.Unmarshal(raw, &page); err != nil {
		return browserPageResult{}, err
	}
	rows := make([]map[string]any, 0, len(page.Hits))
	for _, hit := range page.Hits {
		row := mapper(hit)
		if row != nil {
			rows = append(rows, row)
		}
	}
	return browserPageResult{offset: offset, total: page.Total, hits: len(page.Hits), rows: rows}, nil
}

func (c *BrowserScraperClient) getRaw(ctx context.Context, path string, params url.Values) (json.RawMessage, []byte, error) {
	fullURL := c.baseURL + path
	if encoded := params.Encode(); encoded != "" {
		fullURL += "?" + encoded
	}
	parsed, _ := url.Parse(c.baseURL)
	host := ""
	if parsed != nil {
		host = parsed.Hostname()
	}
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			backoff := browserRetryBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		if host != "" {
			if err := sharedCircuitBreaker.Allow(host); err != nil {
				return nil, nil, err
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
		body, status, err := c.doGet(ctx, fullURL)
		<-c.sem
		if err != nil {
			lastErr = err
			if host != "" {
				sharedCircuitBreaker.RecordResult(host, false)
			}
			LogRateLimitEvent(RateLimitEvent{
				Host:        host,
				Path:        path,
				Status:      0,
				Attempt:     attempt + 1,
				Description: err.Error(),
			})
			continue
		}
		if status == http.StatusTooManyRequests || status >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", status, truncate(body, 500))
			if host != "" {
				sharedCircuitBreaker.RecordResult(host, false)
				if status == http.StatusTooManyRequests {
					MarkHostStale(host)
				}
			}
			LogRateLimitEvent(RateLimitEvent{
				Host:       host,
				Path:       path,
				Status:     status,
				Attempt:    attempt + 1,
				Backoff:    browserRetryBackoff(attempt + 1).String(),
				RetryAfter: "",
			})
			continue
		}
		if status < 200 || status >= 300 {
			if host != "" {
				sharedCircuitBreaker.RecordResult(host, false)
			}
			return nil, body, fmt.Errorf("HTTP %d: %s", status, truncate(body, 1000))
		}
		raw, err := parseBrowserAPIBody(body)
		if err != nil {
			if attempt < c.retries {
				lastErr = err
				if host != "" {
					sharedCircuitBreaker.RecordResult(host, false)
				}
				continue
			}
			if host != "" {
				sharedCircuitBreaker.RecordResult(host, false)
			}
			return nil, body, err
		}
		if host != "" {
			sharedCircuitBreaker.RecordResult(host, true)
		}
		return raw, body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("unknown browser scrape error")
	}
	return nil, nil, lastErr
}

func parseBrowserAPIBody(body []byte) (json.RawMessage, error) {
	resp := browserAPIResponse{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		msg := firstNonEmpty(resp.DetailMsg, resp.Msg)
		return nil, fmt.Errorf("browser code=%d msg=%s", resp.Code, msg)
	}
	return resp.Data, nil
}

func browserRetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	seconds := 1 << (attempt - 1)
	if seconds > 16 {
		seconds = 16
	}
	jitter := time.Duration(rand.Intn(900)) * time.Millisecond
	return time.Duration(seconds)*time.Second + jitter
}

func (c *BrowserScraperClient) doGet(ctx context.Context, fullURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("User-Agent", useragent.Get(req.URL.Hostname()))
	req.Header.Set("Referer", c.baseURL+"/")
	req.Header.Set("x-apiKey", browserXAPIKey())
	resp, err := doHTTPRequest(c.httpClient, req, c.timingObserver)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	observeRateLimitHeaders(resp.Header)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (c *BrowserScraperClient) doPost(ctx context.Context, fullURL string, jsonBody []byte) ([]byte, int, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, 0, err
	}
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case c.sem <- struct{}{}:
	}
	defer func() { <-c.sem }()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("User-Agent", useragent.Get(req.URL.Hostname()))
	req.Header.Set("Referer", c.baseURL+"/")
	if c.baseURL != "" {
		req.Header.Set("Origin", c.baseURL)
	}
	req.Header.Set("x-apiKey", browserXAPIKey())
	resp, err := doHTTPRequest(c.httpClient, req, c.timingObserver)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	observeRateLimitHeaders(resp.Header)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (c *BrowserScraperClient) postRaw(ctx context.Context, fullURL string, jsonBody []byte) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			backoff := browserRetryBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(backoff):
			}
		}
		body, status, err := c.doPost(ctx, fullURL, jsonBody)
		if err != nil {
			lastErr = err
			continue
		}
		if status == http.StatusTooManyRequests || status >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", status, truncate(body, 500))
			continue
		}
		return body, status, nil
	}
	if lastErr == nil {
		lastErr = errors.New("unknown browser post error")
	}
	return nil, 0, lastErr
}

func (c *BrowserScraperClient) FetchTokenTransfersByTimeWindow(ctx context.Context, cfg Config, chain, address, tokenType string, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	type tokenCountResponse struct {
		Code int `json:"code"`
		Data struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}

	queryTotal := func(start, end int64) (int64, error) {
		body, _ := json.Marshal(map[string]any{
			"address":      address,
			"offset":       0,
			"limit":        1,
			"nonzeroValue": true,
			"tokenType":    tokenType,
			"start":        start,
			"end":          end,
		})
		for attempt := 0; attempt < 3; attempt++ {
			apiURL := fmt.Sprintf("%s/api/explorer/v2/%s/addresses/%s/transfers/condition/token?t=%d", c.baseURL, chain, address, time.Now().UnixMilli())
			respBody, _, err := c.postRaw(ctx, apiURL, body)
			if err != nil {
				return 0, err
			}
			var r tokenCountResponse
			if err := json.Unmarshal(respBody, &r); err != nil {
				return 0, err
			}
			if r.Code != 0 {
				return 0, fmt.Errorf("token count code=%d", r.Code)
			}
			if r.Data.Total != 0 || attempt == 2 {
				return r.Data.Total, nil
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return 0, ctx.Err()
			case <-timer.C:
			}
		}
		return 0, nil
	}

	fetchWindow := func(start, end int64) ([]map[string]any, int64, error) {
		apiURL := fmt.Sprintf("%s/api/explorer/v2/%s/addresses/%s/transfers/condition/token?t=%d", c.baseURL, chain, address, time.Now().UnixMilli())
		fetchPage := func(offset int) browserPageResult {
			if err := ctx.Err(); err != nil {
				return browserPageResult{offset: offset, err: err}
			}
			body, _ := json.Marshal(map[string]any{
				"address":      address,
				"offset":       offset,
				"limit":        limit,
				"nonzeroValue": true,
				"tokenType":    tokenType,
				"start":        start,
				"end":          end,
			})
			name := browserWindowCacheName(end, offset)
			respBody, readErr := c.readRaw(chain, "token_transfers", name)
			page := browserListPage{}
			pageReady := false
			if readErr == nil {
				parsed, err := parseBrowserAPIBody(respBody)
				if err == nil {
					if err := json.Unmarshal(parsed, &page); err == nil && len(page.Hits) > 0 {
						pageReady = true
					}
				}
			}
			if !pageReady {
				for attempt := 0; attempt < 3; attempt++ {
					fetchedBody, status, err := c.postRaw(ctx, apiURL, body)
					if err != nil {
						return browserPageResult{offset: offset, err: err}
					}
					if status < 200 || status >= 300 {
						return browserPageResult{offset: offset, err: fmt.Errorf("HTTP %d: %s", status, truncate(fetchedBody, 500))}
					}
					parsed, err := parseBrowserAPIBody(fetchedBody)
					if err != nil {
						return browserPageResult{offset: offset, err: err}
					}
					candidate := browserListPage{}
					if err := json.Unmarshal(parsed, &candidate); err != nil {
						return browserPageResult{offset: offset, err: err}
					}
					if len(candidate.Hits) == 0 && attempt < 2 {
						timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
						select {
						case <-ctx.Done():
							timer.Stop()
							return browserPageResult{offset: offset, err: ctx.Err()}
						case <-timer.C:
						}
						continue
					}
					if len(candidate.Hits) == 0 && (offset > 0 || candidate.Total > 0) {
						return browserPageResult{offset: offset, err: fmt.Errorf("empty token page offset=%d total=%d after 3 attempts", offset, candidate.Total)}
					}
					page = candidate
					pageReady = true
					_ = c.writeRaw(chain, "token_transfers", name, fetchedBody)
					break
				}
			}
			rows := make([]map[string]any, 0, len(page.Hits))
			firstBlockTime := int64(0)
			for _, hit := range page.Hits {
				row := mapper(hit)
				if row == nil {
					continue
				}
				rows = append(rows, row)
				if bt, ok := rawBlockTime(hit); ok {
					if firstBlockTime == 0 || bt < firstBlockTime {
						firstBlockTime = bt
					}
				}
			}
			return browserPageResult{offset: offset, total: page.Total, hits: len(page.Hits), firstBlockTime: firstBlockTime, rows: rows}
		}

		first := fetchPage(0)
		if first.err != nil {
			return nil, 0, first.err
		}
		results := []browserPageResult{first}
		fetchTotal := first.total
		if fetchTotal > browserMaxOffset {
			fetchTotal = browserMaxOffset
		}
		offsets := make([]int, 0)
		for offset := limit; int64(offset) < fetchTotal; offset += limit {
			offsets = append(offsets, offset)
		}
		workers := cap(c.sem)
		pageResults, errs := fetchBrowserPagesInBatches(ctx, offsets, workers, fetchPage, nil)
		results = append(results, pageResults...)
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].offset < results[j].offset
		})
		allMapped := make([]map[string]any, 0, int(fetchTotal))
		firstBlockTime := int64(0)
		for _, result := range results {
			allMapped = append(allMapped, result.rows...)
			if result.firstBlockTime > 0 && (firstBlockTime == 0 || result.firstBlockTime < firstBlockTime) {
				firstBlockTime = result.firstBlockTime
			}
		}
		if len(errs) > 0 {
			return allMapped, firstBlockTime, errors.Join(errs...)
		}
		return allMapped, firstBlockTime, nil
	}

	cachedEnd, hasCachedWindow := c.latestRawWindowEnd(chain, "token_transfers")
	total := int64(browserMaxOffset + 1)
	if !hasCachedWindow {
		var err error
		total, err = queryTotal(1262304000, time.Now().Unix()+365*24*3600)
		if err != nil {
			return nil, err
		}
		if total == 0 {
			return nil, nil
		}
	}
	if total <= int64(browserMaxOffset) {
		rows, _, err := fetchWindow(1262304000, time.Now().Unix()+365*24*3600)
		return rows, err
	}

	if hasCachedWindow {
		reportProgress(cfg, "浏览器爬取 %s: 从代币转账断点窗口恢复 end=%d", strings.ToUpper(chain), cachedEnd)
	} else {
		reportProgress(cfg, "浏览器爬取 %s: 代币转账总量=%d，offset上限=%d，启用时间窗拆分", strings.ToUpper(chain), total, browserMaxOffset)
	}

	cursor := time.Now().Unix() + 365*24*3600
	if hasCachedWindow {
		cursor = cachedEnd
	}
	earliest := int64(1262304000)
	var allRows []map[string]any

	for {
		rows, firstBT, err := fetchWindow(earliest, cursor)
		if err != nil {
			return allRows, fmt.Errorf("window [%d,%d]: %w", earliest, cursor, err)
		}
		if len(rows) == 0 {
			break
		}
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: token时间窗 %d行 (up to %s)", strings.ToUpper(chain), len(rows), time.Unix(cursor, 0).Format("2006-01-02")))
		}
		allRows = append(allRows, rows...)
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: token累计 %d 行", strings.ToUpper(chain), len(allRows)))
		}
		if total <= int64(browserMaxOffset) || len(rows) == 0 {
			break
		}
		if firstBT <= earliest || firstBT >= cursor {
			return allRows, fmt.Errorf("token window did not advance: cursor=%d firstBlockTime=%d", cursor, firstBT)
		}
		cursor = firstBT - 1
	}
	return allRows, nil
}

func rawBlockTime(raw json.RawMessage) (int64, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	bt := toString(m["blocktime"])
	if bt == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(bt, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func (c *BrowserScraperClient) writeRaw(chain, kind, name string, body []byte) error {
	if c.rawDir == "" || len(body) == 0 {
		return nil
	}
	path := c.rawFilePath(chain, kind, name)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0644)
}

func (c *BrowserScraperClient) readRaw(chain, kind, name string) ([]byte, error) {
	if c.rawDir == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(c.rawFilePath(chain, kind, name))
}

func (c *BrowserScraperClient) rawFilePath(chain, kind, name string) string {
	parts := []string{c.rawDir, sanitizeFilePart("browser_" + chain)}
	if c.address != "" {
		parts = append(parts, sanitizeFilePart(c.address))
	}
	dirParts := append([]string{}, parts...)
	file := sanitizeFilePart(kind)
	if name != "" {
		file += "_" + sanitizeFilePart(name)
	}
	return filepath.Join(append(dirParts, file+".json")...)
}

func browserCacheKind(path string) string {
	p := strings.ToLower(path)
	switch {
	case strings.Contains(p, "/transactionsbyclassfy/"):
		return "transactions"
	case strings.Contains(p, "/internaltx/"):
		return "internal"
	case strings.Contains(p, "/transfers/condition/token"):
		return "token_transfers"
	case strings.Contains(p, "/transfers/condition/nft"):
		return "nft_transfers"
	case strings.Contains(p, "/asset/realtime/tokenlist"):
		return "assets"
	default:
		return pathBase(path)
	}
}

func browserPageCacheName(offset, limit int) string {
	return fmt.Sprintf("limit_%03d_offset_%09d", limit, offset)
}

func browserXAPIKey() string {
	rotated := browserPublicAPIKey[8:] + browserPublicAPIKey[:8]
	encodedTime := fmt.Sprintf("%d%d%d%d", time.Now().UnixMilli()+1111111111111, rand.Intn(10), rand.Intn(10), rand.Intn(10))
	return base64.StdEncoding.EncodeToString([]byte(rotated + "|" + encodedTime))
}

func browserTxRow(address, chain, nativeSymbol string, raw json.RawMessage, protocol string) map[string]any {
	row := mapFromRaw(raw)
	txID := firstNonEmpty(toString(row["hash"]), toString(row["txhash"]), toString(row["txId"]))
	ts := browserTime(row)
	out := map[string]any{
		"targetAddress":        address,
		"chainFullName":        firstNonEmpty(toString(row["chainName"]), chain),
		"chainShortName":       chain,
		"protocolType":         protocol,
		"direction":            detectDirection(address, toString(row["from"]), toString(row["to"])),
		"txId":                 txID,
		"methodId":             row["methodId"],
		"blockHash":            row["blockHash"],
		"height":               firstNonEmpty(toString(row["blockHeight"]), toString(row["height"])),
		"transactionTime":      ts,
		"transactionTimeLocal": formatUnixMilli(ts),
		"from":                 row["from"],
		"to":                   row["to"],
		"amount":               firstNonEmpty(toString(row["value"]), toString(row["realValue"])),
		"transactionSymbol":    nativeSymbol,
		"txFee":                row["fee"],
		"state":                firstNonEmpty(toString(row["status"]), boolStatus(row["isError"])),
		"inputdate":            "",
		"logs":                 "",
		"rawJSON":              compactJSON(raw),
	}
	return out
}

func browserTransferRow(address, chain string, raw json.RawMessage, isNFT bool) map[string]any {
	row := mapFromRaw(raw)
	ts := browserTime(row)
	tokenType := toString(row["tokenType"])
	protocol := "token_20"
	if isNFT {
		protocol = "token_721"
		if strings.Contains(strings.ToLower(tokenType), "1155") {
			protocol = "token_1155"
		}
	}
	out := map[string]any{
		"targetAddress":        address,
		"chainFullName":        firstNonEmpty(toString(row["chainName"]), chain),
		"chainShortName":       chain,
		"protocolType":         protocol,
		"direction":            detectDirection(address, toString(row["from"]), toString(row["to"])),
		"txId":                 firstNonEmpty(toString(row["txhash"]), toString(row["hash"]), toString(row["txId"])),
		"methodId":             row["methodId"],
		"blockHash":            row["blockHash"],
		"height":               firstNonEmpty(toString(row["blockHeight"]), toString(row["height"])),
		"transactionTime":      ts,
		"transactionTimeLocal": formatUnixMilli(ts),
		"from":                 row["from"],
		"to":                   row["to"],
		"amount":               firstNonEmpty(toString(row["value"]), toString(row["realValue"])),
		"transactionSymbol":    firstNonEmpty(toString(row["symbol"]), tokenType),
		"tokenId":              row["tokenId"],
		"tokenContractAddress": row["tokenContractAddress"],
		"inputdate":            "",
		"logs":                 "",
		"rawJSON":              compactJSON(raw),
	}
	return out
}

func browserAssetRow(address, chain string, raw json.RawMessage) map[string]any {
	row := mapFromRaw(raw)
	tokenAddress := firstNonEmpty(toString(row["tokenContractAddress"]), toString(row["tokenAddress"]))
	out := map[string]any{
		"address":              address,
		"chainFullName":        firstNonEmpty(toString(row["chainName"]), chain),
		"chainShortName":       chain,
		"assetType":            "token",
		"protocolType":         "token_20",
		"symbol":               firstNonEmpty(toString(row["symbol"]), toString(row["tokenSymbol"])),
		"holdingAmount":        firstNonEmpty(toString(row["holdingAmount"]), toString(row["amount"])),
		"priceUsd":             firstNonEmpty(toString(row["priceUsd"]), toString(row["price"])),
		"valueUsd":             firstNonEmpty(toString(row["valueUsd"]), toString(row["usdValue"])),
		"tokenContractAddress": tokenAddress,
		"tokenId":              row["tokenId"],
		"rawJSON":              compactJSON(raw),
	}
	if toString(row["tokenId"]) != "" {
		out["assetType"] = "nft"
		out["protocolType"] = "token_721"
	}
	return out
}

func mapFromRaw(raw json.RawMessage) map[string]any {
	row := map[string]any{}
	_ = json.Unmarshal(raw, &row)
	return row
}

func browserTime(row map[string]any) string {
	ts := firstNonEmpty(toString(row["transactionTime"]), toString(row["blocktime"]), toString(row["blockTime"]))
	if ts == "" {
		return ""
	}
	if n, err := strconv.ParseInt(ts, 10, 64); err == nil && n > 0 && n < 100000000000 {
		return strconv.FormatInt(n*1000, 10)
	}
	return ts
}

func boolStatus(v any) string {
	b, ok := v.(bool)
	if !ok {
		return ""
	}
	if b {
		return "fail"
	}
	return "success"
}

func nativeSymbolForChain(chain string) string {
	switch strings.ToUpper(chain) {
	case "BSC", "OPBNB":
		return "BNB"
	case "POLYGON":
		return "POL"
	case "AVAXC", "AVAX":
		return "AVAX"
	default:
		return "ETH"
	}
}

func browserNFTTokenTypes(chain string) string {
	switch strings.ToUpper(chain) {
	case "BSC", "OPBNB":
		return "BEP721,BEP1155"
	case "KAIA":
		return "KIP17,KIP37"
	default:
		return "ERC721,ERC1155"
	}
}

func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for k, vals := range in {
		for _, v := range vals {
			out.Add(k, v)
		}
	}
	return out
}

func protocolClassSet(protocols []string) map[string]bool {
	out := map[string]bool{}
	for _, protocol := range protocols {
		switch classifyProtocol(protocol) {
		case "transaction":
			out["transaction"] = true
		case "internal":
			out["internal"] = true
		case "nft":
			out["nft"] = true
		default:
			out["token"] = true
		}
	}
	return out
}

func pathBase(path string) string {
	p := strings.Trim(path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		p = "page"
	}
	return p
}

func sortRowsByHeightDesc(rows []map[string]any) {
	sort.SliceStable(rows, func(i, j int) bool {
		return toString(rows[i]["height"]) > toString(rows[j]["height"])
	})
}
