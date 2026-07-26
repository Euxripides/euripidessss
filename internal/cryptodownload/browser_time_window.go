package cryptodownload

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const browserMaxOffset = 10000

func browserShouldFetchMetadata(cfg Config) bool {
	return !strings.EqualFold(strings.TrimSpace(cfg.Source), "csv")
}

func (c *BrowserScraperClient) FetchTransactionsByTimeWindow(ctx context.Context, cfg Config, chain, address string, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/transactionsByClassfy/condition", chain, address)
	if end, ok := c.latestRawWindowEnd(chain, "transactions"); ok {
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: transactions从断点窗口继续 end=%d", strings.ToUpper(chain), end))
		}
	}
	fetchWindow := func(start, end int64) ([]map[string]any, int64, int64, error) {
		return c.fetchTransactionWindowPages(ctx, chain, path, start, end, limit, progress, mapper)
	}

	earliest := int64(1262304000)
	cursor := time.Now().Unix() + 365*24*3600
	if end, ok := c.latestRawWindowEnd(chain, "transactions"); ok {
		cursor = end
	}
	var allRows []map[string]any
	for {
		rows, firstBT, total, err := fetchWindow(earliest, cursor)
		if err != nil {
			return allRows, fmt.Errorf("transaction window [%d,%d]: %w", earliest, cursor, err)
		}
		if len(rows) == 0 {
			break
		}
		if total > int64(browserMaxOffset) && progress != nil {
			reportProgress(cfg, "浏览器爬取 %s: 普通交易总量=%d，offset上限=%d，启用时间窗拆分", strings.ToUpper(chain), total, browserMaxOffset)
		}
		allRows = append(allRows, rows...)
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: transactions累计 %d 行", strings.ToUpper(chain), len(allRows)))
		}
		if total < int64(browserMaxOffset) {
			break
		}
		if firstBT <= earliest || firstBT >= cursor {
			break
		}
		cursor = firstBT - 1
	}
	return allRows, nil
}

func browserWindowCacheName(end int64, offset int) string {
	return fmt.Sprintf("end_%012d_offset_%09d", end, offset)
}

func (c *BrowserScraperClient) latestRawWindowEnd(chain, kind string) (int64, bool) {
	if c.rawDir == "" {
		return 0, false
	}
	dir := filepath.Dir(c.rawFilePath(chain, kind, ""))
	pattern := filepath.Join(dir, sanitizeFilePart(kind)+"_end_*_offset_*.json")
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		return 0, false
	}
	var bestEnd int64
	var found bool
	for _, file := range files {
		base := filepath.Base(file)
		end, ok := parseBrowserWindowEnd(base)
		if !ok {
			continue
		}
		if !found || end > bestEnd {
			bestEnd = end
			found = true
		}
	}
	return bestEnd, found
}

func parseBrowserWindowEnd(fileName string) (int64, bool) {
	const marker = "_end_"
	start := strings.Index(fileName, marker)
	if start < 0 {
		return 0, false
	}
	start += len(marker)
	end := strings.Index(fileName[start:], "_offset_")
	if end < 0 {
		return 0, false
	}
	value, err := strconv.ParseInt(fileName[start:start+end], 10, 64)
	return value, err == nil
}
