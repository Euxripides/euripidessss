package pricing

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const anchorBatchSize = 5000

var anchorSymbolRE = regexp.MustCompile(`^[A-Z0-9]{5,20}$`)

var bscAnchorTokens = map[string]string{
	"BNBUSDT":  "native:56",
	"ETHUSDT":  "0x2170ed0880ac9a755fd29b2688956bd959f933f8",
	"BTCUSDT":  "0x7130d2a12b9bcbdbaebd4c6e21a1b3f0c7f9c1c5",
	"USDCUSDT": "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d",
}

type AnchorCandle struct {
	ChainID, TradeCount                                     uint64
	Symbol, QuoteSymbol                                     string
	Minute                                                  time.Time
	Open, High, Low, Close, VolumeBase, VolumeQuote, Source string
	SourceFile, SourceChecksum                              string
}

type Candle struct {
	Time       time.Time `json:"time"`
	Open       string    `json:"open"`
	High       string    `json:"high"`
	Low        string    `json:"low"`
	Close      string    `json:"close"`
	VolumeUSD  string    `json:"volume_usd"`
	TradeCount uint64    `json:"trade_count"`
	Source     string    `json:"source"`
}

type AnchorRepository struct{ client ClickHouseClient }

func NewAnchorRepository(client ClickHouseClient) *AnchorRepository {
	return &AnchorRepository{client: client}
}

func (r *AnchorRepository) MonthExists(ctx context.Context, symbol string, month time.Time) (bool, error) {
	query := fmt.Sprintf(`SELECT count() AS n FROM onchain.price_anchor_1m FINAL WHERE chain_id=56 AND symbol='%s' AND minute>=toDateTime64('%s',3,'UTC') AND minute<toDateTime64('%s',3,'UTC')`,
		symbol, month.UTC().Format("2006-01-02 15:04:05.000"), month.AddDate(0, 1, 0).UTC().Format("2006-01-02 15:04:05.000"))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	n, err := strconv.ParseUint(text(rows[0]["n"]), 10, 64)
	expected := uint64(month.AddDate(0, 1, 0).Sub(month) / time.Minute)
	return n >= expected, err
}

func (r *AnchorRepository) ExistingMinutes(ctx context.Context, symbol string, month time.Time) (map[int64]struct{}, error) {
	query := fmt.Sprintf(`SELECT minute FROM onchain.price_anchor_1m FINAL WHERE chain_id=56 AND symbol='%s' AND minute>=toDateTime64('%s',3,'UTC') AND minute<toDateTime64('%s',3,'UTC') ORDER BY minute`,
		symbol, formatTime(month), formatTime(month.AddDate(0, 1, 0)))
	rows, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		minute, parseErr := parseTime(row["minute"])
		if parseErr != nil {
			return nil, parseErr
		}
		out[minute.Unix()] = struct{}{}
	}
	return out, nil
}

func (r *AnchorRepository) Put(ctx context.Context, rows []AnchorCandle) error {
	if len(rows) == 0 {
		return nil
	}
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	for _, row := range rows {
		if row.ChainID != 56 || !anchorSymbolRE.MatchString(row.Symbol) || row.Minute.IsZero() {
			return fmt.Errorf("%w: invalid anchor candle", ErrInvalidInput)
		}
		values := []string{strconv.FormatUint(row.ChainID, 10), row.Symbol, row.QuoteSymbol, formatTime(row.Minute), row.Open, row.High, row.Low, row.Close, row.VolumeBase, row.VolumeQuote, strconv.FormatUint(row.TradeCount, 10), row.Source, row.SourceFile, row.SourceChecksum, formatTime(time.Now().UTC())}
		if err := w.Write(values); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	columns := []string{"chain_id", "symbol", "quote_symbol", "minute", "open", "high", "low", "close", "volume_base", "volume_quote", "trade_count", "source", "source_file", "source_checksum", "ingested_at"}
	return r.client.InsertCSV(ctx, "onchain.price_anchor_1m", columns, &body)
}

func (r *AnchorRepository) Candles(ctx context.Context, symbol string, from, to time.Time, interval Resolution) ([]Candle, error) {
	if !anchorSymbolRE.MatchString(symbol) || !to.After(from) {
		return nil, fmt.Errorf("%w: invalid candle query", ErrInvalidInput)
	}
	bucket := map[Resolution]string{Resolution1Minute: "toStartOfMinute(minute)", Resolution5Minute: "toStartOfInterval(minute, INTERVAL 5 MINUTE)", Resolution15Minute: "toStartOfInterval(minute, INTERVAL 15 MINUTE)", Resolution1Hour: "toStartOfHour(minute)", Resolution4Hour: "toStartOfInterval(minute, INTERVAL 4 HOUR)", Resolution1Day: "toStartOfDay(minute)"}[interval]
	if bucket == "" {
		return nil, fmt.Errorf("%w: invalid candle interval", ErrInvalidInput)
	}
	query := fmt.Sprintf(`SELECT %s AS bucket,argMin(open,minute) open,max(high) high,min(low) low,argMax(close,minute) close,sum(volume_quote) volume_usd,sum(trade_count) trade_count,any(source) source FROM onchain.price_anchor_1m FINAL WHERE chain_id=56 AND symbol='%s' AND minute>=toDateTime64('%s',3,'UTC') AND minute<=toDateTime64('%s',3,'UTC') GROUP BY bucket ORDER BY bucket`, bucket, symbol, formatTime(from), formatTime(to))
	data, err := r.client.QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(data))
	for _, row := range data {
		t, e := parseTime(row["bucket"])
		if e != nil {
			return nil, e
		}
		n, _ := strconv.ParseUint(text(row["trade_count"]), 10, 64)
		out = append(out, Candle{Time: t, Open: text(row["open"]), High: text(row["high"]), Low: text(row["low"]), Close: text(row["close"]), VolumeUSD: text(row["volume_usd"]), TradeCount: n, Source: text(row["source"])})
	}
	return out, nil
}

type AnchorImportResult struct {
	Symbol     string `json:"symbol"`
	Month      string `json:"month"`
	SourceFile string `json:"source_file"`
	Checksum   string `json:"checksum"`
	Rows       uint64 `json:"rows"`
	Reused     bool   `json:"reused"`
}

type BinanceArchiveImporter struct {
	baseURL    string
	paths      EnginePaths
	repository *AnchorRepository
	prices     PriceRepository
	client     *http.Client
}

func NewBinanceArchiveImporter(baseURL string, paths EnginePaths, repository *AnchorRepository, prices PriceRepository, timeout time.Duration) *BinanceArchiveImporter {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &BinanceArchiveImporter{baseURL: strings.TrimRight(baseURL, "/"), paths: paths, repository: repository, prices: prices, client: &http.Client{Timeout: timeout}}
}

func (i *BinanceArchiveImporter) ImportMonth(ctx context.Context, symbol string, month time.Time) (AnchorImportResult, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	month = time.Date(month.UTC().Year(), month.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	if i == nil || i.repository == nil || i.prices == nil || !anchorSymbolRE.MatchString(symbol) || bscAnchorTokens[symbol] == "" || month.After(time.Now().UTC()) {
		return AnchorImportResult{}, fmt.Errorf("%w: invalid Binance anchor import", ErrInvalidInput)
	}
	if ok, err := i.repository.MonthExists(ctx, symbol, month); err == nil && ok {
		if refresher, available := i.prices.(interface {
			RefreshCoverage(context.Context, uint32, string) error
		}); available {
			if refreshErr := refresher.RefreshCoverage(ctx, 56, bscAnchorTokens[symbol]); refreshErr != nil {
				return AnchorImportResult{}, refreshErr
			}
		}
		return AnchorImportResult{Symbol: symbol, Month: month.Format("2006-01"), Reused: true}, nil
	}
	existing, err := i.repository.ExistingMinutes(ctx, symbol, month)
	if err != nil {
		return AnchorImportResult{}, err
	}
	name := fmt.Sprintf("%s-1m-%s.zip", symbol, month.Format("2006-01"))
	remote := "/data/spot/monthly/klines/" + symbol + "/1m/" + name
	dir := filepath.Join(i.paths.BinanceCache, symbol, "1m")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return AnchorImportResult{}, err
	}
	zipPath := filepath.Join(dir, name)
	checksumPath := zipPath + ".CHECKSUM"
	if err := i.download(ctx, remote, zipPath); err != nil {
		return AnchorImportResult{}, err
	}
	if err := i.download(ctx, remote+".CHECKSUM", checksumPath); err != nil {
		return AnchorImportResult{}, err
	}
	expected, err := readExpectedChecksum(checksumPath)
	if err != nil {
		return AnchorImportResult{}, err
	}
	actual, err := fileSHA256(zipPath)
	if err != nil {
		return AnchorImportResult{}, err
	}
	if !strings.EqualFold(expected, actual) {
		return AnchorImportResult{}, fmt.Errorf("Binance checksum mismatch for %s", name)
	}
	count, err := i.importZIP(ctx, symbol, zipPath, name, actual, existing)
	if err != nil {
		return AnchorImportResult{}, err
	}
	if refresher, available := i.prices.(interface {
		RefreshCoverage(context.Context, uint32, string) error
	}); available {
		if err := refresher.RefreshCoverage(ctx, 56, bscAnchorTokens[symbol]); err != nil {
			return AnchorImportResult{}, err
		}
	}
	manifest := AnchorImportResult{Symbol: symbol, Month: month.Format("2006-01"), SourceFile: name, Checksum: actual, Rows: count}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(filepath.Join(i.paths.Manifests, name+".json"), data, 0o640)
	return manifest, nil
}

func (i *BinanceArchiveImporter) download(ctx context.Context, remote, path string) error {
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, i.baseURL+remote, nil)
	if err != nil {
		return err
	}
	resp, err := i.client.Do(req)
	if err != nil {
		return fmt.Errorf("download Binance public data: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Binance public data HTTP %d", resp.StatusCode)
	}
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 512<<20))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}

func (i *BinanceArchiveImporter) importZIP(ctx context.Context, symbol, path, sourceFile, checksum string, existing map[int64]struct{}) (uint64, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	if len(zr.File) != 1 {
		return 0, fmt.Errorf("Binance archive must contain one CSV")
	}
	f, err := zr.File[0].Open()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	reader := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	reader.FieldsPerRecord = -1
	anchors := make([]AnchorCandle, 0, anchorBatchSize)
	prices := make([]HistoricalPrice, 0, anchorBatchSize)
	var total uint64
	flush := func() error {
		if len(anchors) == 0 {
			return nil
		}
		if err := i.repository.Put(ctx, anchors); err != nil {
			return err
		}
		if err := i.prices.PutPrices(ctx, prices); err != nil {
			return err
		}
		anchors = anchors[:0]
		prices = prices[:0]
		return nil
	}
	for {
		record, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return total, e
		}
		if len(record) < 9 {
			return total, fmt.Errorf("invalid Binance kline row")
		}
		ts, e := parseBinanceTimestamp(record[0])
		if e != nil {
			return total, e
		}
		if _, ok := existing[ts.Unix()]; ok {
			continue
		}
		for _, v := range record[1:8] {
			if _, ok := new(big.Rat).SetString(v); !ok {
				return total, fmt.Errorf("invalid Binance decimal")
			}
		}
		trades, e := strconv.ParseUint(record[8], 10, 64)
		if e != nil {
			return total, e
		}
		anchors = append(anchors, AnchorCandle{ChainID: 56, Symbol: symbol, QuoteSymbol: "USDT", Minute: ts, Open: record[1], High: record[2], Low: record[3], Close: record[4], VolumeBase: record[5], VolumeQuote: record[7], TradeCount: trades, Source: "BINANCE_PUBLIC_DATA", SourceFile: sourceFile, SourceChecksum: checksum})
		prices = append(prices, HistoricalPrice{ChainID: 56, TokenID: bscAnchorTokens[symbol], PriceTime: ts, PriceUSD: record[4], Source: "CENTRALIZED_MARKET", Confidence: "HIGH", Resolution: Resolution1Minute, VolumeUSD: &record[7], IsVerified: true, PriceVersion: "anchor-v1", SourceVersion: checksum[:12], SourcePriority: 10})
		total++
		if len(anchors) >= anchorBatchSize {
			if e := flush(); e != nil {
				return total, e
			}
		}
	}
	return total, flush()
}

func parseBinanceTimestamp(raw string) (time.Time, error) {
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	if v > 100_000_000_000_000 {
		return time.UnixMicro(v).UTC(), nil
	}
	return time.UnixMilli(v).UTC(), nil
}
func readExpectedChecksum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("invalid Binance checksum file")
	}
	if _, err = hex.DecodeString(fields[0]); err != nil {
		return "", fmt.Errorf("invalid Binance checksum")
	}
	return strings.ToLower(fields[0]), nil
}
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
