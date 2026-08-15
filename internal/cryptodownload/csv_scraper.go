package cryptodownload

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etl/backend/internal/cryptodownload/useragent"
)

const (
	csvMaxRowsPerExport             = 20000
	defaultCSVStartTime       int64 = 1262304000
	csvEmailWaitTimeout             = 15 * time.Minute
	csvTokenEmailWaitTimeout        = 45 * time.Minute
	csvEmailWaitProgressEvery       = 15 * time.Second
	csvEmailRequestCooldown         = 3 * time.Minute
	csvEmailTimeoutBackoffBase      = 3 * time.Minute
	csvIMAPCommandTimeout           = 20 * time.Second
	csvMailTimestampTolerance       = 5 * time.Second
	csvDirectDownloadAttempts       = 3
	csvDirectSegmentAttempts        = 2
	csvMaxSegmentRetries            = 1
	csvCompletenessTolerance  int64 = 100
	csvTokenWindowSeconds      = int64(0)
)

var (
	csvTokenSegmentCooldownMin    = 5 * time.Second
	csvTokenSegmentCooldownJitter = 10 * time.Second
	// csvForwardDomains holds lower-cased custom domains that route inbound
	// mail to the configured IMAP mailbox (e.g. Cloudflare Email Routing,
	// which requires a catch-all rule for arbitrary prefixes).
	// Destinations on these domains rotate a fresh prefix per request, so a
	// single IMAP identity yields an unbounded recipient pool.
	csvForwardDomains = map[string]bool{}
)

func init() {
	// OKLINK_CSV_TOKEN_COOLDOWN (seconds) sets the base Token segment
	// cooldown; jitter equals the base (so delay is base..2×base).
	if raw := strings.TrimSpace(os.Getenv("OKLINK_CSV_TOKEN_COOLDOWN")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			csvTokenSegmentCooldownMin = time.Duration(seconds) * time.Second
			csvTokenSegmentCooldownJitter = csvTokenSegmentCooldownMin
		}
	}
	// OKLINK_CSV_FORWARD_DOMAINS: comma/semicolon/whitespace separated custom
	// domains whose mail is delivered to the configured IMAP mailbox.
	csvForwardDomains = parseCSVForwardDomains(os.Getenv("OKLINK_CSV_FORWARD_DOMAINS"))
}

// parseCSVForwardDomains parses a forward-domain list (comma/semicolon/
// whitespace separated) into a lower-cased set.
func parseCSVForwardDomains(raw string) map[string]bool {
	domains := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	}) {
		domain := strings.ToLower(strings.TrimSpace(part))
		if domain != "" {
			domains[domain] = true
		}
	}
	return domains
}

var (
	errCSVEmailTimeout = errors.New("timeout waiting for csv download email")
	csvEmailChainMu    sync.Mutex
	csvEmailChainLast  = make(map[string]time.Time)
	csvEmailAliasSeq   atomic.Uint64
)

type CSVExportClient struct {
	baseURL               string
	httpClient            *http.Client
	timingObserver        requestTimingObserver
	mail                  CSVMailConfig
	mailPool              []CSVMailConfig
	mailPoolMu            sync.Mutex
	mailPoolIdx           int
	mailFailures          map[int]int
	mailCooldownUntil     map[int]time.Time
	proxyPin              int
	poolErr               error
	rawDir                string
	seenPath              string
	downloadSigner        *csvDownloadSigner
	browserEmailRequester csvBrowserEmailRequester
	newMailWatcher        func(CSVMailConfig) *csvMailWatcher
	signedRequest         *csvSignedRequestTemplate
	signedRequestErr      error
	durableRename         func(string, string) error
}

type CSVMailConfig struct {
	Email            string
	Host             string
	Port             int
	Username         string
	Password         string
	FolderCandidates []string
}

type csvExportKind struct {
	Name      string
	Endpoint  string
	Sheet     string
	TimeRange bool
}

var csvExportKinds = []csvExportKind{
	{Name: "transactions", Endpoint: "normalTransaction", Sheet: "transaction", TimeRange: true},
	{Name: "token_transfers", Endpoint: "tokenTransfer", Sheet: "token", TimeRange: true},
}

type csvAsyncResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	DetailMsg string `json:"detailMsg"`
}

type csvCountResponse struct {
	Code      int             `json:"code"`
	Msg       string          `json:"msg"`
	DetailMsg string          `json:"detailMsg"`
	Data      json.RawMessage `json:"data"`
}

type csvListTotalResponse struct {
	Code      int             `json:"code"`
	Msg       string          `json:"msg"`
	DetailMsg string          `json:"detailMsg"`
	Data      json.RawMessage `json:"data"`
}

var errCSVNoContent = errors.New("csv download returned no content")

var errCSVSignaturesBlocked = errors.New("OKLink CSV download endpoint requires browser session signatures not available in CLI; falling back to browser API")

func csvShouldFallbackToBrowser(data ExportData) bool {
	if len(data.Errors) == 0 {
		return false
	}
	signCount := 0
	emailQueueTimedOut := false
	for _, e := range data.Errors {
		if isCSVDirectPermanentFailure(e) {
			signCount++
		}
		emailQueueTimedOut = emailQueueTimedOut || strings.Contains(e, errCSVEmailTimeout.Error())
	}
	return emailQueueTimedOut || signCount > 0 && len(data.Transactions) == 0 && len(data.TokenTransfers) == 0 &&
		len(data.RawTransactions) == 0 && len(data.RawTokenTransfers) == 0
}

func mergeExportData(browserData ExportData, csvData ExportData) ExportData {
	return browserData
}

func collectAllFromCSV(ctx context.Context, cfg Config) (data ExportData) {
	cfg.CSVDeliveryMode = normalizeCSVDeliveryMode(cfg.CSVDeliveryMode)
	client := NewCSVExportClient(cfg)
	defer func() {
		if err := client.Close(); err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("close CSV signer: %v", err))
		}
	}()
	for _, chain := range cfg.Chains {
		chain = strings.ToUpper(strings.TrimSpace(chain))
		if chain == "" {
			continue
		}
		chainData, err := client.CollectAddress(ctx, cfg, chain)
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
		data.RawTransactions = append(data.RawTransactions, chainData.RawTransactions...)
		data.RawTokenTransfers = append(data.RawTokenTransfers, chainData.RawTokenTransfers...)
		data.CSVDownloadChecks = append(data.CSVDownloadChecks, chainData.CSVDownloadChecks...)
		if len(chainData.RawTxHeaders) > 0 {
			data.RawTxHeaders = chainData.RawTxHeaders
		}
		if len(chainData.RawTokenHeaders) > 0 {
			data.RawTokenHeaders = chainData.RawTokenHeaders
		}
	}
	if cfg.CSVDeliveryMode == "auto" && csvShouldFallbackToBrowser(data) {
		reportProgress(cfg, "CSV download blocked: OKLink now requires browser session signatures. Falling back to browser API mode.")
		data.Errors = append(data.Errors, errCSVSignaturesBlocked.Error())
		browserData := collectAllFromBrowser(ctx, csvBrowserFallbackConfig(cfg))
		return mergeExportData(browserData, data)
	}
	addNativeAssets(&data)
	fillSummaryCounters(&data)
	sortExportData(&data)
	return data
}

func csvBrowserFallbackConfig(cfg Config) Config {
	cfg.Protocols = []string{"transaction", "token_20"}
	return cfg
}

func NewCSVExportClient(cfg Config) *CSVExportClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	port := cfg.CSVIMAPPort
	if port == 0 {
		port = 993
	}
	raw := strings.TrimSpace(cfg.RawDir)
	seenPath := ""
	if raw != "" {
		seenPath = filepath.Join(raw, "csv_seen_links.json")
	}
	signedRequest, signedRequestErr := loadCSVAsyncSignedRequest(strings.TrimSpace(cfg.CSVRequestHAR))
	mailPool, mailPoolErr := ParseCSVMailPool(cfg.CSVMailPoolText)
	// Proxy pools are process-wide (shared transports); only apply a
	// non-empty configuration here so a later task with no pool cannot
	// silently clear the pool of a running task.  Saving settings applies
	// the pools explicitly (see handleSettings).
	proxyPoolErr := error(nil)
	if strings.TrimSpace(cfg.CSVHTTPProxyPool) != "" {
		proxyPoolErr = SetCSVHTTPProxyPool(cfg.CSVHTTPProxyPool)
	}
	if proxyPoolErr == nil && strings.TrimSpace(cfg.CSVIMAPProxyPool) != "" {
		proxyPoolErr = SetCSVIMAPProxyPool(cfg.CSVIMAPProxyPool)
	}
	// A pinned task must fail loudly instead of silently degrading to the
	// real egress IP when the pin is out of range (CLI/automation paths do
	// not go through the API validation).
	pinErr := error(nil)
	if cfg.CSVProxyPin >= 0 {
		entries, _ := parseCSVProxyList(cfg.CSVHTTPProxyPool, "HTTP")
		if len(entries) == 0 || cfg.CSVProxyPin >= len(entries) {
			pinErr = fmt.Errorf("IP 锁定 %d 超出代理池数量（当前 %d 个）：请检查代理池或选择自动轮换", cfg.CSVProxyPin+1, len(entries))
		}
	}
	return &CSVExportClient{
		baseURL:    baseURL,
		httpClient: newSharedHTTPClient(cfg.Timeout),
		mail: CSVMailConfig{
			Email:            strings.TrimSpace(cfg.CSVEmail),
			Host:             strings.TrimSpace(cfg.CSVIMAPHost),
			Port:             port,
			Username:         strings.TrimSpace(cfg.CSVIMAPUser),
			Password:         cfg.CSVIMAPPassword,
			FolderCandidates: csvMailFolderCandidates(os.Getenv("OKLINK_CSV_IMAP_FOLDERS")),
		},
		mailPool:              mailPool,
		mailFailures:          map[int]int{},
		mailCooldownUntil:     map[int]time.Time{},
		proxyPin:              cfg.CSVProxyPin,
		rawDir:                raw,
		seenPath:              seenPath,
		downloadSigner:        newCSVDownloadSigner(baseURL),
		browserEmailRequester: newCSVBrowserEmailRequester(baseURL),
		newMailWatcher:        newCSVMailWatcher,
		signedRequest:         signedRequest,
		signedRequestErr:      signedRequestErr,
		poolErr:               errors.Join(mailPoolErr, proxyPoolErr, pinErr),
		durableRename:         os.Rename,
	}
}

// csvProxyIndexKey carries the consumed proxy pool index on the request
// context so UA selection and client selection share one decision.
type csvProxyIndexKey struct{}

// httpClientFor returns the HTTP client for the proxy pool entry chosen when
// the request headers were built (matching UA + TLS fingerprint); without a
// pool it returns the default shared client (environment proxies).
func (c *CSVExportClient) httpClientFor(request *http.Request) *http.Client {
	if index, ok := request.Context().Value(csvProxyIndexKey{}).(int); ok && index >= 0 {
		if client := csvHTTPClientForIndex(index); client != nil {
			return client
		}
	}
	return c.httpClient
}

func (c *CSVExportClient) CollectAddress(ctx context.Context, cfg Config, chain string) (ExportData, error) {
	if c.poolErr != nil {
		return ExportData{}, c.poolErr
	}
	var data ExportData
	chainLower := strings.ToLower(chain)
	data.Summaries = append(data.Summaries, csvSummaryRow(cfg.Address, chain))

	start := cfg.CSVStartTime
	end := cfg.CSVEndTime
	if end <= 0 {
		end = time.Now().Unix()
	}
	if start <= 0 {
		start = defaultCSVStartTime
	}

	type kindResult struct {
		kind    csvExportKind
		mapped  []map[string]any
		raw     []map[string]string
		headers []string
		err     error
	}
	var enabled []csvExportKind
	for _, kind := range csvExportKinds {
		if csvKindEnabled(kind, cfg.Protocols) {
			enabled = append(enabled, kind)
		}
	}
	var seenMu sync.Mutex
	seenLinks := c.loadSeenLinks()

	for _, kind := range enabled {
		if err := ctx.Err(); err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("csv %s %s stopped: %v", kind.Name, chain, err))
			break
		}
		reportProgress(cfg, "CSV纯下载 %s: %s", chain, kind.Name)
		mapped, raw, headers, check, err := c.collectKind(ctx, cfg, chainLower, kind, start, end, &seenMu, seenLinks)
		data.CSVDownloadChecks = append(data.CSVDownloadChecks, check)
		r := kindResult{kind: kind, mapped: mapped, raw: raw, headers: headers, err: err}
		if r.err != nil {
			data.Errors = append(data.Errors, fmt.Sprintf("csv %s %s: %v", r.kind.Name, chain, r.err))
		}
		switch r.kind.Sheet {
		case "transaction":
			data.Transactions = append(data.Transactions, r.mapped...)
			data.Funds = append(data.Funds, buildFundRows(r.mapped)...)
			data.RawTransactions = append(data.RawTransactions, r.raw...)
			if len(r.headers) > 0 {
				data.RawTxHeaders = r.headers
			}
		case "internal":
			data.Internals = append(data.Internals, r.mapped...)
			data.Funds = append(data.Funds, buildFundRows(r.mapped)...)
		case "token":
			data.TokenTransfers = append(data.TokenTransfers, r.mapped...)
			data.Funds = append(data.Funds, buildFundRows(r.mapped)...)
			data.RawTokenTransfers = append(data.RawTokenTransfers, r.raw...)
			if len(r.headers) > 0 {
				data.RawTokenHeaders = r.headers
			}
		case "nft":
			data.NFTTransfers = append(data.NFTTransfers, r.mapped...)
			data.Funds = append(data.Funds, buildFundRows(r.mapped)...)
		}
		if ctx.Err() != nil {
			break
		}
	}
	c.saveSeenLinks(seenLinks)
	return data, nil
}

func csvRecordFrom(record map[string]string) string {
	return firstCSVValue(record, "from", "From", "发送方", "转出地址", "鍙戦€佹柟", "杞嚭鍦板潃")
}

func csvRecordTo(record map[string]string) string {
	return firstCSVValue(record, "to", "To", "接收方", "转入地址", "鎺ユ敹鏂?", "杞叆鍦板潃")
}

func csvValidateAddress(records []map[string]string, expectedAddress string) bool {
	if len(records) == 0 {
		return false
	}
	addr := strings.ToLower(expectedAddress)
	checkCount := len(records)
	if checkCount > 5 {
		checkCount = 5
	}
	for i := 0; i < checkCount; i++ {
		from := strings.ToLower(firstCSVValue(records[i], "from", "From", "发送方", "转出地址"))
		to := strings.ToLower(firstCSVValue(records[i], "to", "To", "接收方", "转入地址"))
		if from == "" {
			from = strings.ToLower(csvRecordFrom(records[i]))
		}
		if to == "" {
			to = strings.ToLower(csvRecordTo(records[i]))
		}
		if from == addr || to == addr {
			return true
		}
	}
	return false
}

func (c *CSVExportClient) loadSeenLinks() map[string]bool {
	if c.seenPath == "" {
		return map[string]bool{}
	}
	data, err := os.ReadFile(c.seenPath)
	if err != nil {
		return map[string]bool{}
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return map[string]bool{}
	}
	m := make(map[string]bool, len(list))
	for _, link := range list {
		m[link] = true
	}
	return m
}

func (c *CSVExportClient) saveSeenLinks(links map[string]bool) {
	if c.seenPath == "" {
		return
	}
	var list []string
	for link := range links {
		list = append(list, link)
	}
	data, _ := json.Marshal(list)
	_ = os.MkdirAll(filepath.Dir(c.seenPath), 0755)
	_ = os.WriteFile(c.seenPath, data, 0644)
}

func (c *CSVExportClient) collectKind(ctx context.Context, cfg Config, chain string, kind csvExportKind, start, end int64, seenMu *sync.Mutex, seenLinks map[string]bool) ([]map[string]any, []map[string]string, []string, CSVDownloadCheck, error) {
	check := CSVDownloadCheck{
		Address:       cfg.Address,
		Chain:         strings.ToUpper(chain),
		Kind:          kind.Name,
		ExpectedTotal: -1,
		Status:        "unknown",
	}
	hydrated, err := c.hydrateCSVKind(cfg, chain, kind, start, end)
	if err != nil {
		check = failCSVDownloadCheck(check, 0, csvCanStrictlyCheckTotal(cfg, start, end), err)
		return nil, nil, nil, check, err
	}
	allMapped := hydrated.Mapped
	allRaw := hydrated.Raw
	allHeaders := hydrated.Headers
	seenRows := hydrated.SeenRows
	end = hydrated.NextEndExclusive
	var errs []error
	var directKindDisabledReason string
	var mailWatcher *csvMailWatcher
	defer func() {
		if mailWatcher != nil {
			_ = mailWatcher.Close()
		}
	}()
	strictTotal := csvCanStrictlyCheckTotal(cfg, start, end)
	if total, err := c.fetchCSVListTotal(ctx, cfg, chain, kind); err != nil {
		check.Note = fmt.Sprintf("total count failed: %v", err)
		reportProgress(cfg, "CSV total %s: %s failed: %v", strings.ToUpper(chain), kind.Name, err)
	} else {
		check.ExpectedTotal = total
		check.Status = "pending"
		if !strictTotal {
			check.Status = "unknown"
			check.Note = "total is all-time; custom CSV time range prevents strict completeness check"
		}
		reportProgress(cfg, "CSV total %s: %s = %d", strings.ToUpper(chain), kind.Name, total)
		if strictTotal && total == 0 && len(allRaw) == 0 {
			check.Downloaded = 0
			check.Status = "complete"
			if err := c.markCSVKindCheckpointComplete(cfg, chain, kind); err != nil {
				return allMapped, allRaw, allHeaders, check, err
			}
			return allMapped, allRaw, allHeaders, check, nil
		}
	}
	// Token CSV exports support an OKLink count endpoint with the same time
	// window as the download request. Prefer that count over the all-time list
	// total so Smart Download can make a real completeness decision for a block
	// range (which is converted to this exact time window by its adapter).
	if kind.Sheet == "token" && !strictTotal {
		windowTotal, countErr := c.countCSV(ctx, cfg, chain, kind, start, end)
		if countErr != nil {
			check = failCSVDownloadCheck(check, len(allRaw), true, fmt.Errorf("OKLink window count: %w", countErr))
			return allMapped, allRaw, allHeaders, check, countErr
		}
		check.ExpectedTotal = int64(windowTotal)
		check.Status = "pending"
		check.Note = ""
		strictTotal = true
		reportProgress(cfg, "CSV window total %s: %s = %d", strings.ToUpper(chain), kind.Name, windowTotal)
		if windowTotal == 0 && len(allRaw) == 0 {
			check = finalizeCSVDownloadCheck(check, 0, true)
			if err := c.markCSVKindCheckpointComplete(cfg, chain, kind); err != nil {
				return allMapped, allRaw, allHeaders, check, err
			}
			return allMapped, allRaw, allHeaders, check, nil
		}
	}
	for segment := hydrated.NextSegment; end > start; segment++ {
		if kind.Sheet == "token" && segment > 1 {
			delay := csvTokenSegmentCooldownMin + jitterDuration(csvTokenSegmentCooldownJitter)
			reportProgress(cfg, "CSV token cooldown %s: %s segment %d wait %s", strings.ToUpper(chain), kind.Name, segment, delay.Round(time.Second))
			select {
			case <-ctx.Done():
				check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
				return allMapped, allRaw, allHeaders, check, ctx.Err()
			case <-time.After(delay):
			}
		}
		rangeStart := csvSegmentRangeStart(kind, start, end)
		if rangeStart > start {
			reportProgress(cfg, "CSV token window %s: %s segment %d %s - %s", strings.ToUpper(chain), kind.Name, segment, csvRangeTimeText(rangeStart), csvRangeTimeText(end))
		}
		var records []map[string]string
		var headers []string
		var mapped []map[string]any
		var rawNew []map[string]string
		usedDirect := false
		directSkipReason := csvDirectSkipReason(check.ExpectedTotal, len(allRaw), directKindDisabledReason)
		if cfg.CSVDeliveryMode == "email" {
			directSkipReason = "已强制选择邮箱 CSV"
		}
		directDisabled := directSkipReason != ""
		if !directDisabled {
			disableDirect := func(reason string) {
				if directDisabled {
					return
				}
				directDisabled = true
				if isCSVDirectServiceUnavailable(reason) {
					directKindDisabledReason = reason
				}
				reportProgress(cfg, "CSV direct disabled %s: %s after segment %d: %s", strings.ToUpper(chain), kind.Name, segment, reason)
			}
			var directFailure string
			for directAttempt := 1; directAttempt <= csvDirectSegmentAttempts && !usedDirect; directAttempt++ {
				if directAttempt > 1 {
					delay := csvRequestRetryDelay(directAttempt - 2)
					reportProgress(cfg, "CSV direct retry %s: %s segment %d attempt %d/%d after %s", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts, delay)
					select {
					case <-ctx.Done():
						check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
						return allMapped, allRaw, allHeaders, check, ctx.Err()
					case <-time.After(delay):
					}
				}
				reportProgress(cfg, "CSV direct download %s: %s segment %d attempt %d/%d", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts)
				body, filename, err := c.downloadCSVDirect(ctx, cfg, chain, kind, rangeStart, end)
				if err != nil {
					directFailure = err.Error()
					reportProgress(cfg, "CSV direct download failed %s: %s segment %d attempt %d/%d: %v", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts, err)
					if isCSVDirectPermanentFailure(directFailure) {
						break
					}
					continue
				}
				csvBytes, err := extractCSVPayload(body, filename)
				if err != nil {
					directFailure = err.Error()
					reportProgress(cfg, "CSV direct parse failed %s: %s segment %d attempt %d/%d: %v", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts, err)
					continue
				}
				if isNoSuchKeyPayload(csvBytes) {
					directFailure = "no file"
					reportProgress(cfg, "CSV direct no file %s: %s segment %d attempt %d/%d", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts)
					continue
				}
				records, headers, err = parseCSVRecordsForKind(kind, csvBytes, cfg.Address)
				if err != nil {
					directFailure = err.Error()
					reportProgress(cfg, "CSV direct parse failed %s: %s segment %d attempt %d/%d: %v", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts, err)
					continue
				}
				if len(records) == 0 {
					usedDirect = true
					break
				}
				if !csvValidateAddress(records, cfg.Address) {
					directFailure = "address mismatch"
					reportProgress(cfg, "CSV direct address mismatch %s: %s segment %d attempt %d/%d", strings.ToUpper(chain), kind.Name, segment, directAttempt, csvDirectSegmentAttempts)
					continue
				}
				usedDirect = true
				nextCursor, _ := csvNextCheckpointCursor(kind, records, rangeStart, end, start)
				if err := c.writeCSVRaw(cfg, chain, kind.Name, segment, rangeStart, end, nextCursor, csvBytes); err != nil {
					check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
					return allMapped, allRaw, allHeaders, check, err
				}
				if len(allHeaders) == 0 && len(headers) > 0 {
					allHeaders = headers
				}
				mapped, rawNew = mapNewCSVRecords(cfg.Address, strings.ToUpper(chain), kind, records, seenRows)
			}
			if !usedDirect {
				if directFailure == "" {
					directFailure = "direct download failed"
				}
				disableDirect(directFailure)
			}
		} else {
			reportProgress(cfg, "CSV direct skipped %s: %s segment %d: %s", strings.ToUpper(chain), kind.Name, segment, directSkipReason)
		}
		if !usedDirect {
			if cfg.CSVDeliveryMode == "direct" {
				err := fmt.Errorf("CSV 直链模式失败，已禁止回退邮箱或浏览器")
				check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
				return allMapped, allRaw, allHeaders, check, err
			}
			requestEmailCSV := func() (time.Time, error) {
				for attempt := 1; attempt <= 3; attempt++ {
					requestedAt, err := c.prepareCSVEmailRequest(ctx, &mailWatcher)
					if err != nil {
						return time.Time{}, err
					}
					if err := c.requestCSV(ctx, cfg, chain, kind, rangeStart, end); err == nil {
						return requestedAt, nil
					} else if attempt == 3 || !csvEmailRequestNotSentIsTransient(err) {
						return time.Time{}, csvMailRequestNotSentError(err)
					} else {
						delay := csvRequestRetryDelay(attempt - 1)
						reportProgress(cfg, "CSV email request retry %s: %s segment %d attempt %d/3 after %s", strings.ToUpper(chain), kind.Name, segment, attempt+1, delay)
						select {
						case <-ctx.Done():
							return time.Time{}, ctx.Err()
						case <-time.After(delay):
						}
					}
				}
				return time.Time{}, errors.New("CSV email request attempts exhausted")
			}
			requestedAt, err := requestEmailCSV()
			if err != nil {
				reportProgress(cfg, "CSV email request failed %s: %s segment %d: %v", strings.ToUpper(chain), kind.Name, segment, err)
				check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
				return allMapped, allRaw, allHeaders, check, err
			}
			for retry := 0; ; retry++ {
				if retry > csvMaxSegmentRetries {
					check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
					return allMapped, allRaw, allHeaders, check, fmt.Errorf("segment %d: too many retries for valid data", segment)
				}
				reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段等待邮件链接", strings.ToUpper(chain), kind.Name, segment)
				link, err := c.waitForLink(ctx, mailWatcher, requestedAt, seenMu, seenLinks, kind.Name, cfg.Address, csvEmailTimeoutForKind(kind), func(elapsed time.Duration, lastErr error) {
					if lastErr != nil {
						reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段等待邮件 %s，IMAP：%v", strings.ToUpper(chain), kind.Name, segment, elapsed.Round(time.Second), lastErr)
						return
					}
					reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段等待邮件 %s", strings.ToUpper(chain), kind.Name, segment, elapsed.Round(time.Second))
				})
				if err != nil {
					if retry < csvMaxSegmentRetries && isCSVEmailNoLinkTimeout(err) {
						if c.advanceMailOnFailure(err) {
							mailWatcher = nil
							reportProgress(cfg, "CSV mail pool %s: %s 第 %d 段等待超时，切换邮箱后重新申请", strings.ToUpper(chain), kind.Name, segment)
						}
						backoff := csvEmailTimeoutBackoff(retry)
						if backoff > 0 {
							reportProgress(cfg, "CSV email timeout backoff %s: %s segment %d wait %s before re-request", strings.ToUpper(chain), kind.Name, segment, backoff.Round(time.Second))
							select {
							case <-ctx.Done():
								check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
								return allMapped, allRaw, allHeaders, check, ctx.Err()
							case <-time.After(backoff):
							}
						}
						reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段等待邮件超时，重新请求CSV", strings.ToUpper(chain), kind.Name, segment)
						requestedAt, err = requestEmailCSV()
						if err != nil {
							check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
							return allMapped, allRaw, allHeaders, check, err
						}
						continue
					}
					if retry < csvMaxSegmentRetries && c.advanceMailOnFailure(err) {
						mailWatcher = nil
						reportProgress(cfg, "CSV mail pool %s: %s 第 %d 段邮箱失败（%v），切换后重新申请", strings.ToUpper(chain), kind.Name, segment, err)
						requestedAt, err = requestEmailCSV()
						if err != nil {
							check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
							return allMapped, allRaw, allHeaders, check, err
						}
						continue
					}
					check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
					return allMapped, allRaw, allHeaders, check, fmt.Errorf("segment %d: %w", segment, err)
				}
				reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段下载CSV", strings.ToUpper(chain), kind.Name, segment)
				body, filename, err := c.downloadCSVEmailLink(ctx, link, func(reason string, attempt int, delay time.Duration) {
					reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段下载重试 %d，%s，等待 %s", strings.ToUpper(chain), kind.Name, segment, attempt, reason, delay)
				})
				if err != nil {
					if retry < csvMaxSegmentRetries && isRetryableCSVLinkError(err) {
						reportProgress(cfg, "CSV stale link %s: %s segment %d download failed, requesting a new CSV: %v", strings.ToUpper(chain), kind.Name, segment, err)
						markCSVLinkSeen(seenMu, seenLinks, link)
						requestedAt, err = requestEmailCSV()
						if err != nil {
							check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
							return allMapped, allRaw, allHeaders, check, err
						}
						continue
					}
					errs = append(errs, fmt.Errorf("segment %d download: %w", segment, err))
					check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
					return allMapped, allRaw, allHeaders, check, errors.Join(errs...)
				}
				csvBytes, err := extractCSVPayload(body, filename)
				if err != nil {
					errs = append(errs, fmt.Errorf("segment %d csv: %w", segment, err))
					check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
					return allMapped, allRaw, allHeaders, check, errors.Join(errs...)
				}
				if isNoSuchKeyPayload(csvBytes) {
					if retry < csvMaxSegmentRetries {
						reportProgress(cfg, "CSV stale link %s: %s segment %d returned NoSuchKey, requesting a new CSV", strings.ToUpper(chain), kind.Name, segment)
						markCSVLinkSeen(seenMu, seenLinks, link)
						requestedAt, err = requestEmailCSV()
						if err != nil {
							check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
							return allMapped, allRaw, allHeaders, check, err
						}
						continue
					}
					reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段无有效CSV，停止该sheet", strings.ToUpper(chain), kind.Name, segment)
					if segment == 1 && len(allMapped) == 0 && rangeStart <= start {
						check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
						return allMapped, allRaw, allHeaders, check, errCSVNoContent
					}
					records = nil
					break
				}
				records, headers, err = parseCSVRecordsForKind(kind, csvBytes, cfg.Address)
				if err != nil {
					errs = append(errs, fmt.Errorf("segment %d parse: %w", segment, err))
					check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
					return allMapped, allRaw, allHeaders, check, errors.Join(errs...)
				}
				if len(allHeaders) == 0 && len(headers) > 0 {
					allHeaders = headers
				}
				if len(records) == 0 {
					break
				}
				if csvValidateAddress(records, cfg.Address) {
					nextCursor, _ := csvNextCheckpointCursor(kind, records, rangeStart, end, start)
					mappedNext, rawNext, stale, err := c.commitCSVEmailSegment(cfg, chain, kind, segment, rangeStart, end, nextCursor, records, csvBytes, seenRows, len(allMapped) > 0)
					if err != nil {
						check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
						return allMapped, allRaw, allHeaders, check, err
					}
					if stale {
						reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段命中旧链接，重新请求新CSV", strings.ToUpper(chain), kind.Name, segment)
						requestedAt, err = requestEmailCSV()
						if err != nil {
							check = failCSVDownloadCheck(check, len(allRaw), strictTotal, err)
							return allMapped, allRaw, allHeaders, check, err
						}
						continue
					}
					mapped, rawNew = mappedNext, rawNext
					break
				}
				reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段重试 %d：CSV地址不匹配", strings.ToUpper(chain), kind.Name, segment, retry+1)
				markCSVLinkSeen(seenMu, seenLinks, link)
			}
		}
		if len(records) == 0 {
			if rangeStart > start {
				end = rangeStart - 1
				continue
			}
			break
		}

		allMapped = append(allMapped, mapped...)
		allRaw = append(allRaw, rawNew...)
		source := "email"
		sourceDownloaded := check.EmailDownloaded + len(rawNew)
		if usedDirect {
			source = "direct"
			sourceDownloaded = check.DirectDownloaded + len(rawNew)
			check.DirectDownloaded = sourceDownloaded
		} else {
			check.EmailDownloaded = sourceDownloaded
		}

		lastTime, ok := lastCSVTransactionUnix(records)
		reportProgress(cfg, "CSV count %s: %s source %s segment %d added %d rows, %s %d rows, total %s", strings.ToUpper(chain), kind.Name, source, segment, len(rawNew), source, sourceDownloaded, csvDownloadCountText(len(allRaw), check.ExpectedTotal))
		if strictTotal && check.ExpectedTotal >= 0 && int64(len(allRaw)) >= check.ExpectedTotal {
			reportProgress(cfg, "CSV complete %s: %s %d/%d", strings.ToUpper(chain), kind.Name, len(allRaw), check.ExpectedTotal)
			break
		}
		reportProgress(cfg, "CSV纯下载 %s: %s 第 %d 段，新增 %d 行，累计 %d 行", strings.ToUpper(chain), kind.Name, segment, len(mapped), len(allMapped))

		if !kind.TimeRange || !ok {
			break
		}
		if len(records) < csvMaxRowsPerExport {
			if rangeStart > start {
				end = rangeStart - 1
				continue
			}
			break
		}
		nextEnd := lastTime - 1
		if nextEnd >= end {
			nextEnd = end - 1
		}
		end = nextEnd
	}
	check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
	if strictTotal && check.ExpectedTotal >= 0 && csvDownloadedDifference(check.ExpectedTotal, len(allRaw)) > csvCompletenessTolerance {
		errs = append(errs, fmt.Errorf("%s incomplete: downloaded %d/%d rows", kind.Name, len(allRaw), check.ExpectedTotal))
		check = finalizeCSVDownloadCheck(check, len(allRaw), strictTotal)
	}
	if len(errs) == 0 {
		if err := c.markCSVKindCheckpointComplete(cfg, chain, kind); err != nil {
			errs = append(errs, fmt.Errorf("mark %s CSV checkpoint complete: %w", kind.Name, err))
		}
	}
	return allMapped, allRaw, allHeaders, check, errors.Join(errs...)
}

func csvSegmentRangeStart(kind csvExportKind, start, end int64) int64 {
	if kind.Sheet != "token" || csvTokenWindowSeconds <= 0 || end-start <= csvTokenWindowSeconds {
		return start
	}
	return end - csvTokenWindowSeconds
}

func csvRangeTimeText(ts int64) string {
	return time.Unix(ts, 0).Format("2006-01-02")
}

func csvCanStrictlyCheckTotal(cfg Config, start, end int64) bool {
	return cfg.CSVStartTime <= defaultCSVStartTime && cfg.CSVEndTime <= 0
}

func csvDownloadCountText(downloaded int, expected int64) string {
	if expected >= 0 {
		return fmt.Sprintf("%d/%d 行", downloaded, expected)
	}
	return fmt.Sprintf("%d 行", downloaded)
}

func normalizeCSVDeliveryMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "direct":
		return "direct"
	case "email":
		return "email"
	default:
		return "auto"
	}
}

func csvDirectSkipReason(expectedTotal int64, downloaded int, disabledReason string) string {
	if strings.TrimSpace(disabledReason) != "" {
		return disabledReason
	}
	if expectedTotal < 0 {
		return ""
	}
	remaining := expectedTotal - int64(downloaded)
	if remaining > csvMaxRowsPerExport {
		return fmt.Sprintf("remaining %d rows exceeds direct limit %d; use email CSV", remaining, csvMaxRowsPerExport)
	}
	return ""
}

func finalizeCSVDownloadCheck(check CSVDownloadCheck, downloaded int, strict bool) CSVDownloadCheck {
	check.Downloaded = downloaded
	if check.ExpectedTotal < 0 {
		if check.Status == "" {
			check.Status = "unknown"
		}
		return check
	}
	if !strict {
		check.Status = "unknown"
		if check.Note == "" {
			check.Note = "total is all-time; custom CSV time range prevents strict completeness check"
		}
		return check
	}
	if check.ExpectedTotal > 0 && downloaded == 0 {
		check.Status = "incomplete"
		check.Note = fmt.Sprintf("downloaded 0/%d rows; non-empty source cannot pass tolerance", check.ExpectedTotal)
		return check
	}
	difference := csvDownloadedDifference(check.ExpectedTotal, downloaded)
	if difference <= csvCompletenessTolerance {
		check.Status = "complete"
		if difference > 0 && check.Note == "" {
			check.Note = fmt.Sprintf("downloaded %d/%d rows; within tolerance %d", downloaded, check.ExpectedTotal, csvCompletenessTolerance)
		}
		return check
	}
	check.Status = "incomplete"
	if check.Note == "" {
		check.Note = fmt.Sprintf("downloaded %d/%d rows", downloaded, check.ExpectedTotal)
	}
	return check
}

func csvEmailRequestNotSentIsTransient(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "winerror 10055") ||
		strings.Contains(lower, "socket buffer") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection closed") ||
		strings.Contains(lower, "i/o timeout")
}

func failCSVDownloadCheck(check CSVDownloadCheck, downloaded int, strict bool, err error) CSVDownloadCheck {
	check = finalizeCSVDownloadCheck(check, downloaded, strict)
	check.Status = "failed"
	if err != nil && check.Note == "" {
		check.Note = err.Error()
	}
	return check
}

func csvDownloadedDifference(expected int64, downloaded int) int64 {
	difference := expected - int64(downloaded)
	if difference < 0 {
		return -difference
	}
	return difference
}

func csvEmailTimeoutForKind(kind csvExportKind) time.Duration {
	if kind.Sheet == "token" {
		return csvTokenEmailWaitTimeout
	}
	return csvEmailWaitTimeout
}

// csvEmailTimeoutBackoff returns the extra wait applied before re-requesting a
// CSV export email after consecutive delivery timeouts.  It doubles per
// consecutive timeout and caps at ten minutes so provider-side mail
// generation/risk control has time to recover without unbounded stalls.
func csvEmailTimeoutBackoff(consecutiveTimeouts int) time.Duration {
	backoff := csvEmailTimeoutBackoffBase
	for i := 0; i < consecutiveTimeouts && backoff < 10*time.Minute; i++ {
		backoff *= 2
	}
	if backoff > 10*time.Minute {
		return 10 * time.Minute
	}
	return backoff
}

func isRetryableCSVLinkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "NoSuchKey") ||
		strings.Contains(text, "HTTP 404") ||
		strings.Contains(text, "HTTP 429") ||
		strings.Contains(text, "HTTP 5") ||
		errors.Is(err, context.DeadlineExceeded)
}

func csvClassifyLink(link string) string {
	normalized := normalizeCSVEmailLink(link)
	if normalized == "" {
		return ""
	}
	text := strings.ToLower(linkSearchText(normalized))
	if !strings.Contains(text, "download") && !strings.Contains(text, ".csv") && !strings.Contains(text, ".zip") {
		return ""
	}
	if strings.Contains(text, "tokentransfer") || strings.Contains(text, "token_transfer") {
		return "token_transfers"
	}
	if strings.Contains(text, "transactions") || strings.Contains(text, "normaltransaction") || strings.Contains(text, "normal_transaction") {
		return "transactions"
	}
	return ""
}

func csvSummaryRow(address, chain string) map[string]any {
	now := time.Now().Format("2006-01-02 15:04:05")
	return map[string]any{
		"address":        address,
		"chainFullName":  chain,
		"chainShortName": chain,
		"downloadStatus": "CSV纯下载",
		"exportedAt":     now,
		"rawJSON":        jsonCompactAny(map[string]any{"source": "csv", "exportedAt": now}),
	}
}

func csvKindEnabled(kind csvExportKind, protocols []string) bool {
	set := protocolClassSet(protocols)
	switch kind.Sheet {
	case "transaction":
		return set["transaction"]
	case "internal":
		return set["internal"]
	case "nft":
		return set["nft"]
	default:
		return set["token"]
	}
}

func (c *CSVExportClient) fetchCSVListTotal(ctx context.Context, cfg Config, chain string, kind csvExportKind) (int64, error) {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return 0, errors.New("empty address")
	}
	chain = strings.ToLower(strings.TrimSpace(chain))
	switch kind.Sheet {
	case "transaction":
		path := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transactionsByClassfy/condition", chain, address)
		params := url.Values{}
		params.Set("offset", "0")
		params.Set("limit", "1")
		params.Set("nonzeroValue", "false")
		params.Set("address", address)
		params.Set("t", strconv.FormatInt(time.Now().UnixMilli(), 10))
		fullURL := c.baseURL + path + "?" + params.Encode()
		referer := fmt.Sprintf("%s/zh-hans/%s/address/%s", c.baseURL, chain, address)
		return c.fetchCSVListTotalGET(ctx, fullURL, referer)
	case "token":
		apiURL := fmt.Sprintf("%s/api/explorer/v2/%s/addresses/%s/transfers/condition/token?t=%d", c.baseURL, chain, address, time.Now().UnixMilli())
		referer := fmt.Sprintf("%s/zh-hans/%s/address/%s/token-transfer", c.baseURL, chain, address)
		payload := map[string]any{
			"address":      address,
			"offset":       0,
			"limit":        1,
			"nonzeroValue": true,
			"tokenType":    csvTokenTypeForChain(chain),
		}
		return c.fetchCSVListTotalPOST(ctx, apiURL, referer, payload)
	default:
		return 0, fmt.Errorf("unsupported csv total kind: %s", kind.Name)
	}
}

func (c *CSVExportClient) fetchCSVListTotalGET(ctx context.Context, fullURL, referer string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return 0, err
	}
	req = setCSVBrowserAPIHeaders(req, c.baseURL, referer, "application/json", c.proxyPin)
	resp, err := doHTTPRequest(c.httpClientFor(req), req, c.timingObserver)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, csvHTTPStatusError(resp.StatusCode, body)
	}
	return parseCSVListTotal(body)
}

func (c *CSVExportClient) fetchCSVListTotalPOST(ctx context.Context, apiURL, referer string, payload map[string]any) (int64, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req = setCSVBrowserAPIHeaders(req, c.baseURL, referer, "application/json", c.proxyPin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := doHTTPRequest(c.httpClientFor(req), req, c.timingObserver)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, csvHTTPStatusError(resp.StatusCode, respBody)
	}
	return parseCSVListTotal(respBody)
}

func setCSVBrowserAPIHeaders(req *http.Request, baseURL, referer, accept string, pin int) *http.Request {
	req.Header.Set("Accept", accept)
	req.Header.Set("App-Type", "web")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Referer", referer)
	req = setCSVUserAgentHeaders(req, baseURL, pin)
	req.Header.Set("x-apiKey", browserXAPIKey())
	req.Header.Set("x-locale", "zh_CN")
	req.Header.Set("x-utc", "8")
	if baseURL != "" {
		req.Header.Set("Origin", baseURL)
	}
	return req
}

func parseCSVListTotal(body []byte) (int64, error) {
	resp := csvListTotalResponse{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	if resp.Code != 0 {
		return 0, fmt.Errorf("csv total code=%d msg=%s", resp.Code, firstNonEmpty(resp.DetailMsg, resp.Msg))
	}
	data := struct {
		Total json.RawMessage `json:"total"`
	}{}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return 0, err
	}
	if len(data.Total) == 0 {
		return 0, errors.New("csv total response missing data.total")
	}
	return parseJSONInt64(data.Total)
}

func parseJSONInt64(raw json.RawMessage) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, err
	}
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, nil
		}
		f, err := strconv.ParseFloat(x.String(), 64)
		if err != nil {
			return 0, err
		}
		return int64(f), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(x), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported integer json value: %s", string(raw))
	}
}

func (c *CSVExportClient) countCSV(ctx context.Context, cfg Config, chain string, kind csvExportKind, start, end int64) (int, error) {
	if kind.Sheet != "token" {
		return 0, fmt.Errorf("countCSV only supports token sheets")
	}
	referer := fmt.Sprintf("%s/zh-hans/%s/address/%s/token-transfer", c.baseURL, chain, cfg.Address)
	payload := csvTokenRequestPayload(cfg, c.baseURL, chain, start, end, c.emailExportAlias(chain, kind.Name))
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("%s/download/explorer/v1/%s/%s/download/count?t=%d", c.baseURL, chain, kind.Endpoint, time.Now().UnixMilli())

	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req = setCSVPostHeaders(req, c.baseURL, referer, "application/json", c.proxyPin)
		if err := c.applyCSVDownloadSignature(ctx, req, body, chain, cfg.Address); err != nil {
			return 0, err
		}
		resp, err := doHTTPRequest(c.httpClientFor(req), req, c.timingObserver)
		if err != nil {
			lastErr = err
		} else {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				lastErr = csvHTTPStatusError(resp.StatusCode, respBody)
			} else {
				countResp := csvCountResponse{}
				if err := json.Unmarshal(respBody, &countResp); err != nil {
					lastErr = csvInvalidJSONError("csv count response", respBody, err)
				} else if countResp.Code != 0 {
					lastErr = fmt.Errorf("csv count code=%d msg=%s", countResp.Code, firstNonEmpty(countResp.DetailMsg, countResp.Msg))
				} else {
					return parseCSVCount(countResp.Data)
				}
			}
		}
		if err := csvPermanentSignError(lastErr); err != nil {
			return 0, err
		}
		delay := csvRequestRetryDelay(attempt)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("csv count request failed")
	}
	return 0, lastErr
}

func parseCSVCount(raw json.RawMessage) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, nil
	}
	var intCount int
	if err := json.Unmarshal(raw, &intCount); err == nil {
		return intCount, nil
	}
	var floatCount float64
	if err := json.Unmarshal(raw, &floatCount); err == nil {
		return int(floatCount), nil
	}
	var stringCount string
	if err := json.Unmarshal(raw, &stringCount); err == nil {
		n, err := strconv.Atoi(strings.TrimSpace(stringCount))
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	var objectCount map[string]any
	if err := json.Unmarshal(raw, &objectCount); err == nil {
		for _, key := range []string{"count", "total", "totalCount", "data"} {
			if value, ok := objectCount[key]; ok {
				n, err := strconv.Atoi(strings.TrimSpace(toString(value)))
				if err == nil {
					return n, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("unsupported csv count payload: %s", truncate(raw, 200))
}

func (c *CSVExportClient) downloadCSVDirect(ctx context.Context, cfg Config, chain string, kind csvExportKind, start, end int64) ([]byte, string, error) {
	referer, payload := csvDownloadRequestPayload(cfg, c.baseURL, chain, kind, start, end, c.emailExportAlias(chain, kind.Name))
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("%s/download/explorer/v1/%s/%s/download?t=%d", c.baseURL, chain, kind.Endpoint, time.Now().UnixMilli())
	var lastErr error
	for attempt := 0; attempt < csvDirectDownloadAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return nil, "", err
		}
		req = setCSVPostHeaders(req, c.baseURL, referer, "*/*", c.proxyPin)
		if err := c.applyCSVDownloadSignature(ctx, req, body, chain, cfg.Address); err != nil {
			return nil, "", err
		}
		resp, err := doHTTPRequest(c.httpClientFor(req), req, c.timingObserver)
		if err != nil {
			c.noteProxyFailure(req, 0, err)
			lastErr = err
		} else {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				c.noteProxyFailure(req, resp.StatusCode, nil)
				lastErr = csvHTTPStatusError(resp.StatusCode, respBody)
				if !csvRetryableHTTPStatus(resp.StatusCode) {
					return nil, "", lastErr
				}
			} else {
				filename := csvResponseFilename(resp)
				if err := csvDirectPayloadError(resp, respBody, filename); err != nil {
					return nil, "", err
				}
				return respBody, filename, nil
			}
		}
		if attempt+1 >= csvDirectDownloadAttempts {
			break
		}
		delay := csvRequestRetryDelay(attempt)
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("csv direct download failed")
	}
	return nil, "", lastErr
}

func csvDownloadRequestPayload(cfg Config, baseURL, chain string, kind csvExportKind, start, end int64, email string) (string, map[string]any) {
	if kind.Sheet == "token" {
		referer := fmt.Sprintf("%s/zh-hans/%s/address/%s/token-transfer", baseURL, chain, cfg.Address)
		return referer, csvTokenRequestPayload(cfg, baseURL, chain, start, end, email)
	}
	referer := fmt.Sprintf("%s/zh-hans/%s/address/%s", baseURL, chain, cfg.Address)
	payload := map[string]any{
		"address":      cfg.Address,
		"start":        start,
		"end":          end,
		"nonzeroValue": false,
		"url":          referer,
	}
	if strings.TrimSpace(email) != "" {
		payload["email"] = email
	}
	return referer, payload
}

func csvTokenRequestPayload(cfg Config, baseURL, chain string, start, end int64, email string) map[string]any {
	referer := fmt.Sprintf("%s/zh-hans/%s/address/%s/token-transfer", baseURL, chain, cfg.Address)
	payload := map[string]any{
		"address":      cfg.Address,
		"start":        start,
		"end":          end,
		"nonzeroValue": true,
		"tokenType":    csvTokenTypeForChain(chain),
		"url":          referer,
	}
	if strings.TrimSpace(email) != "" {
		payload["email"] = email
	}
	return payload
}

func setCSVPostHeaders(req *http.Request, baseURL, referer, accept string, pin int) *http.Request {
	req.Header.Set("Accept", accept)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", referer)
	req = setCSVUserAgentHeaders(req, baseURL, pin)
	req.Header.Set("x-apiKey", browserXAPIKey())
	return req
}

// setCSVUserAgentHeaders sets a rotated browser User-Agent plus the full
// matching client-hint and fetch-metadata header set, so the CSV download
// requests look like a real browser rather than a fixed Chrome client.
// The proxy pool decision is consumed here exactly once per request and
// carried on the request context, so the HTTP client (and its TLS
// fingerprint) selected at send time always matches this UA.  Returns the
// request (context may have been replaced).
func setCSVUserAgentHeaders(req *http.Request, baseURL string, pin int) *http.Request {
	index := csvHTTPProxyIndexForUse(pin)
	var agent, language string
	if index >= 0 {
		req = req.WithContext(context.WithValue(req.Context(), csvProxyIndexKey{}, index))
		agent = useragent.ChromeByIndex(index)
		language = useragent.AcceptLanguageByIndex(index)
	} else {
		identity := ""
		if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
			identity = parsed.Host
		}
		agent = useragent.GetChrome(identity)
		language = useragent.AcceptLanguage(identity)
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept-Language", language)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	brand := useragent.SecCHUABrand(agent)
	if brand == "" {
		return req
	}
	req.Header.Set("Sec-CH-UA", brand)
	mobile := "?0"
	if useragent.IsMobileUA(agent) {
		mobile = "?1"
	}
	req.Header.Set("Sec-CH-UA-Mobile", mobile)
	if full := useragent.SecCHUAFullVersionList(agent); full != "" {
		req.Header.Set("Sec-CH-UA-Full-Version-List", full)
	}
	if platform := useragent.SecCHUAPlatform(agent); platform != "" {
		req.Header.Set("Sec-CH-UA-Platform", platform)
	}
	setCSVClientHintDetails(req, agent)
	return req
}

// setCSVClientHintDetails adds the architecture/bitness/platform-version/
// model client hints matching the User-Agent's platform.
func setCSVClientHintDetails(req *http.Request, agent string) {
	arch := `"x86"`
	platformVersion := `"15.0.0"`
	model := `""`
	switch useragent.SecCHUAPlatform(agent) {
	case "macOS":
		platformVersion = `"14.6.0"`
	case "Linux":
		platformVersion = `""`
	case "iOS":
		arch = `"arm"`
		platformVersion = `"18.1.0"`
		model = `"iPhone"`
	default:
		if !strings.Contains(agent, "Windows") {
			return
		}
	}
	req.Header.Set("Sec-CH-UA-Arch", arch)
	req.Header.Set("Sec-CH-UA-Bitness", `"64"`)
	req.Header.Set("Sec-CH-UA-Platform-Version", platformVersion)
	req.Header.Set("Sec-CH-UA-Model", model)
}

// noteProxyFailure marks the proxy IP that actually carried the request as
// failed when it hit a 429/403/503 status or a connection-level error.  For
// pinned tasks the IP comes from the request context (the pinned entry may
// never be the affinity current); otherwise the current affinity proxy is
// used.  This forces an IP rotation on the next request instead of waiting
// on a flagged IP.
func (c *CSVExportClient) noteProxyFailure(request *http.Request, status int, err error) {
	if status != http.StatusTooManyRequests && status != http.StatusForbidden && status != http.StatusServiceUnavailable && err == nil {
		return
	}
	host := csvCurrentHTTPProxyHost()
	if index, ok := request.Context().Value(csvProxyIndexKey{}).(int); ok && index >= 0 {
		if proxy := csvHTTPProxyByIndex(index); proxy != nil {
			host = proxy.Host
		}
	}
	MarkCSVHTTPProxyFailed(host)
}

func csvResponseFilename(resp *http.Response) string {
	filename := "download.csv"
	if resp == nil {
		return filename
	}
	if disp := resp.Header.Get("Content-Disposition"); disp != "" {
		if i := strings.Index(strings.ToLower(disp), "filename="); i >= 0 {
			filename = strings.Trim(strings.TrimSpace(disp[i+9:]), `"`)
		}
	}
	return filename
}

func csvDirectPayloadError(resp *http.Response, body []byte, filename string) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return errors.New("csv direct returned empty response")
	}
	lowerContentType := ""
	if resp != nil {
		lowerContentType = strings.ToLower(resp.Header.Get("Content-Type"))
	}
	lowerFilename := strings.ToLower(filename)
	if strings.Contains(lowerContentType, "json") || bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("[")) {
		return fmt.Errorf("csv direct returned JSON response: %s", csvResponseSummary(trimmed))
	}
	prefix := bytes.ToLower(trimmed)
	if len(prefix) > 256 {
		prefix = prefix[:256]
	}
	if bytes.HasPrefix(prefix, []byte("<!doctype html")) || bytes.HasPrefix(prefix, []byte("<html")) || bytes.Contains(prefix, []byte("<title>")) {
		return fmt.Errorf("csv direct returned HTML response: %s", csvResponseSummary(trimmed))
	}
	if strings.HasSuffix(lowerFilename, ".csv") || strings.HasSuffix(lowerFilename, ".zip") || bytes.HasPrefix(trimmed, []byte("PK\x03\x04")) {
		return nil
	}
	if strings.Contains(lowerContentType, "html") && bytes.HasPrefix(prefix, []byte("<")) {
		return fmt.Errorf("csv direct returned HTML response: %s", csvResponseSummary(trimmed))
	}
	return nil
}

func isCSVDirectServiceUnavailable(reason string) bool {
	lower := strings.ToLower(reason)
	return strings.Contains(lower, "system is being upgraded") ||
		strings.Contains(lower, "please try again later") ||
		strings.Contains(lower, `"code":-1`) ||
		isCSVDirectPermanentFailure(reason)
}

func isCSVDirectPermanentFailure(reason string) bool {
	lower := strings.ToLower(reason)
	return strings.Contains(lower, "incorrect request sign parameters") ||
		strings.Contains(lower, `"code":50113`) ||
		strings.Contains(lower, "code=50113")
}

func csvPermanentSignError(err error) error {
	if err == nil || !isCSVDirectPermanentFailure(err.Error()) {
		return nil
	}
	return fmt.Errorf("OKLink CSV 请求签名失效: %w", err)
}

func (c *CSVExportClient) requestCSV(ctx context.Context, cfg Config, chain string, kind csvExportKind, start, end int64) error {
	if c.signedRequestErr != nil {
		return c.signedRequestErr
	}
	finishRequest, err := c.beginCSVEmailRequest(ctx, cfg, chain, kind)
	if err != nil {
		return err
	}
	defer finishRequest()
	referer := fmt.Sprintf("%s/zh-hans/%s/address/%s", c.baseURL, chain, cfg.Address)
	payload := map[string]any{}
	switch kind.Sheet {
	case "token":
		referer = fmt.Sprintf("%s/zh-hans/%s/address/%s/token-transfer", c.baseURL, chain, cfg.Address)
		payload = map[string]any{
			"address":      cfg.Address,
			"email":        c.emailExportAlias(chain, kind.Name),
			"nonzeroValue": true,
			"tokenType":    csvTokenTypeForChain(chain),
			"url":          referer,
		}
	default:
		payload = map[string]any{
			"address":      cfg.Address,
			"email":        c.emailExportAlias(chain, kind.Name),
			"nonzeroValue": false,
			"url":          referer,
		}
	}
	payload["start"] = start
	payload["end"] = end
	body, _ := json.Marshal(payload)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	apiURL := fmt.Sprintf("%s/download/explorer/v1/%s/%s/download/async?t=%s", c.baseURL, chain, kind.Endpoint, timestamp)
	if c.browserEmailRequester != nil {
		reportProgress(cfg, "CSV纯下载 %s: %s 使用 %s 申请邮件", strings.ToUpper(chain), kind.Name, csvBrowserEmailEngine(c.browserEmailRequester))
		return c.browserEmailRequester.Request(ctx, csvBrowserEmailRequest{URL: apiURL, PageURL: referer, Body: string(body)})
	}

	var lastErr error
	var signerVersion csvSignerVersion
	for attempt := 0; attempt < 12; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req = setCSVPostHeaders(req, c.baseURL, referer, "application/json", c.proxyPin)
		var signErr error
		signerVersion, signErr = c.applyCSVDownloadSignatureWithVersion(ctx, req, body, chain, cfg.Address)
		if signErr != nil {
			return signErr
		}
		if c.downloadSigner == nil && c.signedRequest != nil {
			c.signedRequest.Apply(req, c.baseURL, referer, timestamp)
		}
		resp, err := doHTTPRequest(c.httpClientFor(req), req, c.timingObserver)
		if err != nil {
			c.noteProxyFailure(req, 0, err)
			lastErr = err
		} else {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				c.noteProxyFailure(req, resp.StatusCode, nil)
				lastErr = csvHTTPStatusError(resp.StatusCode, respBody)
			} else {
				apiResp := csvAsyncResponse{}
				if err := json.Unmarshal(respBody, &apiResp); err != nil {
					lastErr = csvInvalidJSONError("csv async response", respBody, err)
				} else if apiResp.Code != 0 {
					lastErr = fmt.Errorf("csv code=%d msg=%s", apiResp.Code, firstNonEmpty(apiResp.DetailMsg, apiResp.Msg))
				} else {
					return nil
				}
			}
		}
		if err := csvSignerVersionedPermanentFailure(lastErr, signerVersion); err != nil {
			c.downloadSigner.MarkStale()
			reportProgress(cfg, "CSV纯下载 %s: %s 请求签名失败，已停止重试：%v", strings.ToUpper(chain), kind.Name, lastErr)
			return err
		}
		delay := csvRequestRetryDelay(attempt)
		reportProgress(cfg, "CSV纯下载 %s: %s 请求失败，%s 后重试 %d/12：%v", strings.ToUpper(chain), kind.Name, delay, attempt+1, lastErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("csv async request failed")
	}
	return lastErr
}

// emailExportAlias returns the CSV email destination.  Project policy
// (2026-08-12) allows mailbox/alias rotation to handle OKLink CSV email
// throttling: for Gmail destinations every call yields a fresh
// "local+oklN@domain" alias.  Gmail delivers +tagged mail to the same
// inbox, so IMAP credentials, UID baselines and mail correlation stay
// unchanged while OKLink sees a new recipient per request (including
// retries after timeout/cooldown).  When a mail pool is configured the
// active pool mailbox is used, falling back to the configured mailbox.
// Non-Gmail destinations are returned as-is.
func (c *CSVExportClient) emailExportAlias(chain, kind string) string {
	return rotateCSVEmailAlias(c.activeMail().Email)
}

// rotateCSVEmailAlias applies rotation to a destination: Gmail addresses get
// a +oklN alias; custom forward domains (OKLINK_CSV_FORWARD_DOMAINS) get a
// fresh rotating prefix (okl%08x, monotonic per process); everything else is
// returned unchanged.
func rotateCSVEmailAlias(email string) string {
	email = strings.TrimSpace(email)
	local, domain, found := strings.Cut(email, "@")
	if !found || local == "" {
		return email
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "gmail.com" || domain == "googlemail.com" {
		return fmt.Sprintf("%s+okl%d@%s", local, csvEmailAliasSeq.Add(1), domain)
	}
	if csvForwardDomains[domain] {
		return fmt.Sprintf("okl%08x@%s", csvEmailAliasSeq.Add(1), domain)
	}
	return email
}

func (c *CSVExportClient) beginCSVEmailRequest(ctx context.Context, cfg Config, chain string, kind csvExportKind) (func(), error) {
	if c == nil || !csvSignerEnabledForBaseURL(c.baseURL) {
		return func() {}, nil
	}
	chainKey := strings.ToLower(strings.TrimSpace(chain))
	csvEmailChainMu.Lock()
	lastRequest := csvEmailChainLast[chainKey]
	if !lastRequest.IsZero() {
		wait := time.Until(lastRequest.Add(csvEmailRequestCooldown + jitterDuration(20*time.Second)))
		if wait > 0 {
			reportProgress(cfg, "CSV email cooldown %s: %s wait %s", strings.ToUpper(chain), kind.Name, wait.Round(time.Second))
			csvEmailChainMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
				csvEmailChainMu.Lock()
			}
		}
	}
	return func() {
		csvEmailChainLast[chainKey] = time.Now()
		csvEmailChainMu.Unlock()
	}, nil
}

func csvRequestRetryDelay(attempt int) time.Duration {
	if attempt < 2 {
		return 2 * time.Second
	}
	if attempt < 6 {
		return 5 * time.Second
	}
	return 15 * time.Second
}

func csvTokenTypeForChain(chain string) string {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "bsc", "opbnb":
		return "BEP20"
	case "tron", "trx":
		return "TRC20"
	default:
		return "ERC20"
	}
}

func csvRetryableHTTPStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func csvHTTPStatusError(status int, body []byte) error {
	summary := csvResponseSummary(body)
	if summary == "" {
		return fmt.Errorf("HTTP %d", status)
	}
	return fmt.Errorf("HTTP %d: %s", status, summary)
}

func csvInvalidJSONError(context string, body []byte, err error) error {
	summary := csvResponseSummary(body)
	if summary == "" {
		return fmt.Errorf("%s invalid JSON: %w", context, err)
	}
	return fmt.Errorf("%s invalid JSON: %w; response=%s", context, err, summary)
}

func csvResponseSummary(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response"
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "<!doctype html") || strings.Contains(lower, "<html") || strings.Contains(lower, "<title>") {
		if title := extractHTMLTitle(text); title != "" {
			return "HTML response: " + title
		}
		return "HTML response"
	}
	return trimCSVSummary(text, 160)
}

func extractHTMLTitle(text string) string {
	lower := strings.ToLower(text)
	start := strings.Index(lower, "<title>")
	if start < 0 {
		return ""
	}
	start += len("<title>")
	end := strings.Index(lower[start:], "</title>")
	if end < 0 {
		return ""
	}
	return trimCSVSummary(stripHTMLTags(text[start:start+end]), 120)
}

func trimCSVSummary(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func extractCSVPayload(body []byte, filename string) ([]byte, error) {
	if strings.HasSuffix(strings.ToLower(filename), ".zip") || bytes.HasPrefix(body, []byte("PK\x03\x04")) {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, err
		}
		for _, file := range zr.File {
			if !strings.HasSuffix(strings.ToLower(file.Name), ".csv") {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
		return nil, errors.New("zip does not contain csv")
	}
	return body, nil
}

func isNoSuchKeyPayload(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.Contains(trimmed, "<Code>NoSuchKey</Code>") || strings.Contains(trimmed, "<Code>NoSuchKey")
}

var (
	csvHeaderlessTransactionHeaders = []string{"TxHash", "Block", "Local Time", "UTC Time", "From", "To", "Amount", "Fee", "Method", "Status", "Error"}
	csvHeaderlessTokenHeaders       = []string{"TxHash", "Block", "Local Time", "UTC Time", "From", "To", "Amount", "Symbol", "Token Address"}
)

func parseCSVRecordsForKind(kind csvExportKind, body []byte, address string) ([]map[string]string, []string, error) {
	records, headers, err := parseCSVRecords(body)
	if err != nil || (kind.Sheet != "token" && kind.Sheet != "transaction") || csvValidateAddress(records, address) {
		return records, headers, err
	}
	var headerless []map[string]string
	var headerlessHeaders []string
	var ok bool
	switch kind.Sheet {
	case "transaction":
		headerless, headerlessHeaders, ok, err = parseHeaderlessCSVRecords(body, csvHeaderlessTransactionHeaders)
	case "token":
		headerless, headerlessHeaders, ok, err = parseHeaderlessTokenCSVRecords(body)
	}
	if err != nil || !ok {
		return records, headers, err
	}
	return headerless, headerlessHeaders, nil
}

func parseHeaderlessTokenCSVRecords(body []byte) ([]map[string]string, []string, bool, error) {
	return parseHeaderlessCSVRecords(body, csvHeaderlessTokenHeaders)
}

func parseHeaderlessCSVRecords(body []byte, headers []string) ([]map[string]string, []string, bool, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, nil, false, err
	}
	if len(rows) == 0 || !looksLikeHeaderlessCSV(rows[0], len(headers)) {
		return nil, nil, false, nil
	}
	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		record := map[string]string{}
		for i, h := range headers {
			if i < len(row) {
				record[h] = normalizeScientificDecimalString(strings.TrimSpace(row[i]))
			}
		}
		for i := len(headers); i < len(row); i++ {
			record[fmt.Sprintf("_extra_%d", i-len(headers)+1)] = normalizeScientificDecimalString(strings.TrimSpace(row[i]))
		}
		out = append(out, record)
	}
	return out, append([]string{}, headers...), true, nil
}

func looksLikeHeaderlessTokenCSV(row []string) bool {
	return looksLikeHeaderlessCSV(row, len(csvHeaderlessTokenHeaders))
}

func looksLikeHeaderlessCSV(row []string, minimumColumns int) bool {
	if len(row) < minimumColumns {
		return false
	}
	txHash := strings.TrimSpace(row[0])
	from := strings.TrimSpace(row[4])
	to := strings.TrimSpace(row[5])
	return strings.HasPrefix(strings.ToLower(txHash), "0x") && isHexAddress(from) && isHexAddress(to)
}

func isHexAddress(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 42 || !strings.HasPrefix(strings.ToLower(value), "0x") {
		return false
	}
	for _, ch := range value[2:] {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}

func normalizeScientificDecimalString(value string) string {
	if value == "" || !strings.ContainsAny(value, "eE") {
		return value
	}
	ePos := strings.IndexAny(value, "eE")
	if ePos <= 0 || ePos == len(value)-1 {
		return value
	}
	mantissa := value[:ePos]
	exp, err := strconv.Atoi(value[ePos+1:])
	if err != nil {
		return value
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") || strings.HasPrefix(mantissa, "+") {
		if mantissa[0] == '-' {
			sign = "-"
		}
		mantissa = mantissa[1:]
	}
	if mantissa == "" {
		return value
	}
	fracDigits := 0
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		fracDigits = len(mantissa) - dot - 1
		mantissa = mantissa[:dot] + mantissa[dot+1:]
	}
	if mantissa == "" {
		return value
	}
	for _, ch := range mantissa {
		if ch < '0' || ch > '9' {
			return value
		}
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return "0"
	}
	scale := fracDigits - exp
	var out string
	if scale <= 0 {
		out = digits + strings.Repeat("0", -scale)
	} else if len(digits) > scale {
		split := len(digits) - scale
		out = digits[:split] + "." + digits[split:]
	} else {
		out = "0." + strings.Repeat("0", scale-len(digits)) + digits
	}
	if strings.Contains(out, ".") {
		out = strings.TrimRight(out, "0")
		out = strings.TrimRight(out, ".")
	}
	if sign == "-" && out != "0" {
		out = "-" + out
	}
	return out
}

func parseCSVRecords(body []byte) ([]map[string]string, []string, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(rows) < 2 {
		return nil, nil, nil
	}
	headers := rows[0]
	trimmedHeaders := make([]string, len(headers))
	for i, h := range headers {
		trimmedHeaders[i] = strings.TrimSpace(h)
	}
	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := map[string]string{}
		for i, h := range trimmedHeaders {
			if i < len(row) {
				record[h] = normalizeScientificDecimalString(strings.TrimSpace(row[i]))
			}
		}
		for i := len(headers); i < len(row); i++ {
			record[fmt.Sprintf("_extra_%d", i-len(headers)+1)] = normalizeScientificDecimalString(strings.TrimSpace(row[i]))
		}
		out = append(out, record)
	}
	return out, trimmedHeaders, nil
}

func csvRecordToExportRow(address, chain string, kind csvExportKind, record map[string]string) map[string]any {
	ts := firstCSVValue(record, "transactionTime", "Transaction Time", "Local Time", "UTC Time", "Time", "本地时间(UTC+8)", "UTC时间", "时间", "日期", "Date")
	unix := csvTimeUnix(ts)
	protocol := kind.Sheet
	if kind.Sheet == "token" {
		protocol = "token_20"
	}
	if kind.Sheet == "nft" {
		protocol = "token_721"
	}
	fromValue := csvRecordFrom(record)
	toValue := csvRecordTo(record)
	row := map[string]any{
		"targetAddress":        address,
		"chainFullName":        chain,
		"chainShortName":       chain,
		"protocolType":         protocol,
		"direction":            detectDirection(address, firstCSVValue(record, "from", "From", "发送方", "转出地址"), firstCSVValue(record, "to", "To", "接收方", "转入地址")),
		"txId":                 firstCSVValue(record, "txId", "TxHash", "Txn Hash", "Transaction Hash", "Hash", "交易哈希"),
		"methodId":             firstCSVValue(record, "Method", "Method ID", "methodId", "方法", "方法ID", "_extra_1"),
		"blockHash":            firstCSVValue(record, "Block Hash", "blockHash", "区块哈希"),
		"height":               firstCSVValue(record, "Block", "Block Height", "Height", "区块高度"),
		"transactionTime":      strconv.FormatInt(unix*1000, 10),
		"transactionTimeLocal": formatUnixMilli(strconv.FormatInt(unix*1000, 10)),
		"from":                 firstCSVValue(record, "from", "From", "发送方", "转出地址"),
		"to":                   firstCSVValue(record, "to", "To", "接收方", "转入地址"),
		"amount":               firstCSVValue(record, "Value", "Amount", "数量", "金额"),
		"transactionSymbol":    firstCSVValue(record, "Token", "Symbol", "Asset", "币种", "代币"),
		"txFee":                firstCSVValue(record, "TxFee", "Fee", "手续费"),
		"state":                firstCSVValue(record, "Status", "State", "交易状态", "状态", "_extra_2"),
		"tokenId":              firstCSVValue(record, "Token ID", "tokenId"),
		"tokenContractAddress": firstCSVValue(record, "Token Contract", "Contract", "Token Contract Address", "代币合约", "代币地址"),
		"inputdate":            "",
		"logs":                 "",
		"rawJSON":              jsonCompactAny(record),
	}
	if fromValue != "" || toValue != "" {
		row["direction"] = detectDirection(address, fromValue, toValue)
		row["from"] = fromValue
		row["to"] = toValue
	}
	state := toString(row["state"])
	extraState := firstCSVValue(record, "_extra_1", "_extra_2")
	if strings.HasPrefix(strings.ToLower(state), "0x") {
		row["methodId"] = state
		if extraState != "" {
			row["state"] = extraState
		}
	} else if method := toString(row["methodId"]); strings.EqualFold(method, "SUCCESS") || strings.EqualFold(method, "FAIL") || strings.EqualFold(method, "FAILED") {
		row["state"] = method
		row["methodId"] = ""
	}
	return row
}

func mapNewCSVRecords(address, chain string, kind csvExportKind, records []map[string]string, seenRows map[string]bool) ([]map[string]any, []map[string]string) {
	mapped := make([]map[string]any, 0, len(records))
	rawNew := make([]map[string]string, 0, len(records))
	for _, record := range records {
		row := csvRecordToExportRow(address, chain, kind, record)
		key := csvRecordDedupeKey(kind, row, record)
		if seenRows[key] {
			continue
		}
		seenRows[key] = true
		mapped = append(mapped, row)
		rawNew = append(rawNew, record)
	}
	return mapped, rawNew
}

func csvRecordDedupeKey(kind csvExportKind, row map[string]any, record map[string]string) string {
	rawKey := jsonCompactAny(record)
	if kind.Sheet == "token" {
		return rawKey
	}
	return firstNonEmpty(toString(row["txId"]), rawKey)
}

func firstCSVValue(record map[string]string, keys ...string) string {
	lower := map[string]string{}
	for k, v := range record {
		lower[strings.ToLower(strings.TrimSpace(k))] = v
	}
	for _, key := range keys {
		if v := record[key]; strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if v := lower[strings.ToLower(strings.TrimSpace(key))]; strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func lastCSVTransactionUnix(records []map[string]string) (int64, bool) {
	if len(records) == 0 {
		return 0, false
	}
	for i := len(records) - 1; i >= 0; i-- {
		ts := firstCSVValue(records[i], "transactionTime", "Transaction Time", "Local Time", "UTC Time", "Time", "本地时间(UTC+8)", "UTC时间", "时间", "日期", "Date")
		if unix := csvTimeUnix(ts); unix > 0 {
			return unix, true
		}
	}
	return 0, false
}

func csvTimeUnix(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 100000000000 {
			return n / 1000
		}
		return n
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Unix()
		}
	}
	return 0
}

var linkPattern = regexp.MustCompile(`(?:https?://|www\.|static\.oklink\.com)[^\s"'<>\\]+`)
var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

func extractLinks(raw string) []string {
	variants := []string{raw, collapseQuotedPrintableSoftBreaks(raw)}
	if decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(raw))); err == nil {
		variants = append(variants, string(decoded), collapseQuotedPrintableSoftBreaks(string(decoded)))
	}
	for _, text := range append([]string(nil), variants...) {
		variants = append(variants, stripHTMLTags(text))
	}
	seen := map[string]bool{}
	var out []string
	for _, text := range variants {
		for _, link := range linkPattern.FindAllString(text, -1) {
			link = normalizeCSVEmailLink(htmlUnescapeURL(strings.TrimRight(link, ".,);]")))
			if link == "" || seen[link] {
				continue
			}
			seen[link] = true
			out = append(out, link)
		}
	}
	return out
}

func stripHTMLTags(s string) string {
	s = htmlTagPattern.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.Join(strings.Fields(s), " ")
}

func collapseQuotedPrintableSoftBreaks(s string) string {
	s = strings.ReplaceAll(s, "=\r\n", "")
	s = strings.ReplaceAll(s, "=\n", "")
	return strings.ReplaceAll(s, "=3D", "=")
}

func linkSearchText(link string) string {
	parts := []string{link}
	if decoded, err := url.QueryUnescape(link); err == nil && decoded != link {
		parts = append(parts, decoded)
	}
	return strings.Join(parts, " ")
}

func htmlUnescapeURL(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "=3D", "=")
	s = strings.ReplaceAll(s, "=\r\n", "")
	s = strings.ReplaceAll(s, "=\n", "")
	return s
}
