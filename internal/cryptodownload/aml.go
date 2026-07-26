package cryptodownload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type AMLClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *RateLimiter
	sem        chan struct{}
	retries    int
}

type AMLResponse struct {
	Code      int             `json:"code"`
	Msg       string          `json:"msg"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id"`
}

type AMLAddressLabel struct {
	ChainID int      `json:"chain_id"`
	Address string   `json:"address"`
	Type    []string `json:"type"`
	Name    string   `json:"name"`
	RawJSON string   `json:"-"`
}

type amlLookupKey struct {
	ChainShortName string
	ChainID        int
	Address        string
}

type filterStats struct {
	ExchangeRows int
}

var deepAMLChainIDs = map[string]int{
	"ARBITRUM":        1,
	"ARB":             1,
	"AVALANCHE":       3,
	"AVAX":            3,
	"AVAXC":           3,
	"BSC":             4,
	"BNB":             4,
	"BNB_CHAIN":       4,
	"BNB SMART CHAIN": 4,
	"BASE":            6,
	"BLAST":           12,
	"CELO":            15,
	"CRONOS":          17,
	"ETH":             19,
	"ETHEREUM":        19,
	"FTM":             20,
	"FANTOM":          20,
	"GNOSIS":          21,
	"KAVA":            24,
	"LINEA":           25,
	"MANTA":           27,
	"MANTA PACIFIC":   27,
	"OP":              33,
	"OPTIMISM":        33,
	"POLYGON":         34,
	"MATIC":           34,
	"SCROLL":          36,
	"SOLANA":          38,
	"SOL":             38,
	"SUI":             41,
	"TON":             42,
	"TRON":            43,
	"TRX":             43,
	"ZKSYNC":          46,
	"ZK":              46,
}

func NewAMLClient(cfg Config) *AMLClient {
	workers := cfg.Workers
	if workers < 1 {
		workers = 1
	}
	return &AMLClient{
		baseURL: strings.TrimRight(cfg.AMLBaseURL, "/"),
		apiKey:  cfg.AMLAPIKey,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		limiter: NewRateLimiter(cfg.AMLRPS),
		sem:     make(chan struct{}, workers),
		retries: cfg.Retries,
	}
}

func applyAMLLabelsAndFilter(ctx context.Context, cfg Config, data *ExportData) error {
	if cfg.AMLAPIKey == "" {
		return nil
	}
	if cfg.AMLBaseURL == "" {
		return errors.New("DeepAML base URL 不能为空")
	}
	client := NewAMLClient(cfg)
	keys := collectAMLKeys(data)
	labels, err := client.FetchLabels(ctx, keys)
	applyLabelsToRows(data, labels)
	stats := filterStats{}
	if cfg.FilterExchange {
		stats.ExchangeRows += filterExchangeRows(&data.Transactions)
		stats.ExchangeRows += filterExchangeRows(&data.Internals)
		stats.ExchangeRows += filterExchangeRows(&data.TokenTransfers)
		stats.ExchangeRows += filterExchangeRows(&data.NFTTransfers)
		stats.ExchangeRows += filterExchangeRows(&data.Funds)
	}
	for _, row := range data.Summaries {
		row["aml_filtered_exchange_rows"] = stats.ExchangeRows
	}
	return err
}

func (c *AMLClient) FetchLabels(ctx context.Context, keys []amlLookupKey) (map[amlLookupKey]AMLAddressLabel, error) {
	type result struct {
		key   amlLookupKey
		label AMLAddressLabel
		err   error
	}
	jobs := make(chan amlLookupKey)
	results := make(chan result)
	workerCount := cap(c.sem)
	if workerCount > len(keys) && len(keys) > 0 {
		workerCount = len(keys)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range jobs {
				label, err := c.FetchLabel(ctx, key)
				results <- result{key: key, label: label, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, key := range keys {
			select {
			case <-ctx.Done():
				return
			case jobs <- key:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	labels := make(map[amlLookupKey]AMLAddressLabel, len(keys))
	var errs []error
	for res := range results {
		if res.err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", res.key.ChainShortName, res.key.Address, res.err))
			continue
		}
		labels[res.key] = res.label
	}
	return labels, errors.Join(errs...)
}

func (c *AMLClient) FetchLabel(ctx context.Context, key amlLookupKey) (AMLAddressLabel, error) {
	params := url.Values{}
	params.Set("address", key.Address)
	params.Set("chain_id", fmt.Sprintf("%d", key.ChainID))
	raw, err := c.get(ctx, "/v1/address-labels", params)
	if err != nil {
		return AMLAddressLabel{}, err
	}
	var label AMLAddressLabel
	if len(raw) == 0 || string(raw) == "null" {
		return label, nil
	}
	if err := json.Unmarshal(raw, &label); err != nil {
		return AMLAddressLabel{}, err
	}
	label.RawJSON = compactJSON(raw)
	return label, nil
}

func (c *AMLClient) get(ctx context.Context, endpoint string, params url.Values) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			backoffSeconds := 1 << attempt
			if backoffSeconds > 30 {
				backoffSeconds = 30
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(backoffSeconds) * time.Second):
			}
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
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
			return nil, fmt.Errorf("HTTP %d: %s", status, truncate(body, 1000))
		}
		var resp AMLResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("DeepAML JSON解析失败: %w; body=%s", err, truncate(body, 500))
		}
		if resp.Code != 0 && resp.Code != 200 {
			msg := firstNonEmpty(resp.Msg, resp.Message)
			lastErr = fmt.Errorf("DeepAML code=%d msg=%s", resp.Code, msg)
			if attempt < c.retries {
				continue
			}
			return nil, lastErr
		}
		return resp.Data, nil
	}
	return nil, lastErr
}

func (c *AMLClient) doGet(ctx context.Context, endpoint string, params url.Values) ([]byte, int, error) {
	reqURL := c.baseURL + endpoint
	if encoded := params.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "wallet-exporter-aml/1.0")
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

func collectAMLKeys(data *ExportData) []amlLookupKey {
	seen := map[amlLookupKey]bool{}
	add := func(chain, address string) {
		address = strings.TrimSpace(address)
		if address == "" {
			return
		}
		chainID := deepAMLChainID(chain)
		if chainID == 0 {
			return
		}
		key := amlLookupKey{ChainShortName: strings.ToUpper(strings.TrimSpace(chain)), ChainID: chainID, Address: address}
		key.Address = strings.ToLower(key.Address)
		if seen[key] {
			return
		}
		seen[key] = true
	}
	for _, rows := range [][]map[string]any{data.Summaries, data.Transactions, data.Internals, data.TokenTransfers, data.NFTTransfers, data.Funds, data.Assets} {
		for _, row := range rows {
			chain := toString(row["chainShortName"])
			add(chain, toString(row["targetAddress"]))
			add(chain, toString(row["address"]))
			add(chain, toString(row["from"]))
			add(chain, toString(row["to"]))
			add(chain, toString(row["counterparty"]))
		}
	}
	keys := make([]amlLookupKey, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ChainShortName == keys[j].ChainShortName {
			return keys[i].Address < keys[j].Address
		}
		return keys[i].ChainShortName < keys[j].ChainShortName
	})
	return keys
}

func applyLabelsToRows(data *ExportData, labels map[amlLookupKey]AMLAddressLabel) {
	for _, rows := range [][]map[string]any{data.Transactions, data.Internals, data.TokenTransfers, data.NFTTransfers} {
		for _, row := range rows {
			chain := toString(row["chainShortName"])
			target := toString(row["targetAddress"])
			from := toString(row["from"])
			to := toString(row["to"])
			counterparty := firstNonEmpty(toString(row["counterparty"]), counterparty(target, from, to))
			row["counterparty"] = counterparty
			applyLabel(row, "target", lookupLabel(labels, chain, target))
			applyLabel(row, "from", lookupLabel(labels, chain, from))
			applyLabel(row, "to", lookupLabel(labels, chain, to))
			applyLabel(row, "counterparty", lookupLabel(labels, chain, counterparty))
		}
	}
	for _, row := range data.Funds {
		chain := toString(row["chainShortName"])
		target := toString(row["targetAddress"])
		counterpartyAddr := firstNonEmpty(toString(row["counterparty"]), counterparty(target, toString(row["from"]), toString(row["to"])))
		row["counterparty"] = counterpartyAddr
		applyLabel(row, "target", lookupLabel(labels, chain, target))
		applyLabel(row, "counterparty", lookupLabel(labels, chain, counterpartyAddr))
	}
	for _, row := range data.Assets {
		chain := toString(row["chainShortName"])
		applyLabel(row, "address", lookupLabel(labels, chain, toString(row["address"])))
	}
	for _, row := range data.Summaries {
		chain := toString(row["chainShortName"])
		applyLabel(row, "address", lookupLabel(labels, chain, toString(row["address"])))
	}
}

func applyLabel(row map[string]any, prefix string, label AMLAddressLabel) {
	if label.Address == "" && label.Name == "" && len(label.Type) == 0 {
		return
	}
	row[prefix+"LabelName"] = label.Name
	row[prefix+"LabelTypes"] = strings.Join(label.Type, ",")
	row[prefix+"LabelRawJSON"] = label.RawJSON
}

func lookupLabel(labels map[amlLookupKey]AMLAddressLabel, chain, address string) AMLAddressLabel {
	chainID := deepAMLChainID(chain)
	if chainID == 0 || strings.TrimSpace(address) == "" {
		return AMLAddressLabel{}
	}
	key := amlLookupKey{ChainShortName: strings.ToUpper(strings.TrimSpace(chain)), ChainID: chainID, Address: strings.ToLower(strings.TrimSpace(address))}
	return labels[key]
}

func filterExchangeRows(rows *[]map[string]any) int {
	if rows == nil || len(*rows) == 0 {
		return 0
	}
	out := (*rows)[:0]
	filtered := 0
	for _, row := range *rows {
		target := toString(row["targetAddress"])
		counterpartyAddr := toString(row["counterparty"])
		if counterpartyAddr != "" && !sameAddress(target, counterpartyAddr) && isExchangeLabel(toString(row["counterpartyLabelTypes"])) {
			filtered++
			continue
		}
		out = append(out, row)
	}
	*rows = out
	return filtered
}

func isExchangeLabel(types string) bool {
	for _, part := range strings.Split(types, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "EXCHANGE") {
			return true
		}
	}
	return false
}

func deepAMLChainID(chain string) int {
	key := strings.ToUpper(strings.TrimSpace(chain))
	key = strings.ReplaceAll(key, "-", " ")
	key = strings.ReplaceAll(key, "_", " ")
	if id := deepAMLChainIDs[key]; id != 0 {
		return id
	}
	key = strings.ReplaceAll(key, " ", "_")
	return deepAMLChainIDs[key]
}
