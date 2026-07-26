package cryptodownload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (c *BrowserScraperClient) FetchInternalByTimeWindow(ctx context.Context, cfg Config, chain, address string, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/api/explorer/v2/%s/addresses/%s/internalTx/condition", chain, address)
	earliest := int64(1262304000)
	initialCursor := time.Now().Unix() + 365*24*3600
	cursor := initialCursor
	if end, ok := c.latestRawWindowEnd(chain, "internal"); ok {
		cursor = end
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: internal从断点窗口继续 end=%d", strings.ToUpper(chain), end))
		}
	}
	var allRows []map[string]any
	for {
		rows, oldest, total, err := c.fetchInternalWindow(ctx, chain, path, earliest, cursor, limit, cursor == initialCursor, progress, mapper)
		allRows = append(allRows, rows...)
		if err != nil {
			return allRows, fmt.Errorf("internal window [%d,%d]: %w", earliest, cursor, err)
		}
		if len(rows) == 0 {
			return allRows, nil
		}
		next, complete, err := nextBrowserWindow(total, oldest, cursor)
		if err != nil {
			return allRows, err
		}
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: internal累计 %d 行", strings.ToUpper(chain), len(allRows)))
		}
		if complete {
			return allRows, nil
		}
		cursor = next
	}
}

func (c *BrowserScraperClient) fetchInternalWindow(ctx context.Context, chain, path string, start, end int64, limit int, allowLegacyCache bool, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, int64, int64, error) {
	fetchPage := func(offset int) browserPageResult {
		name := browserWindowCacheName(end, offset)
		body, readErr := c.readRaw(chain, "internal", name)
		if readErr != nil && allowLegacyCache {
			body, readErr = c.readRaw(chain, "internal", browserPageCacheName(offset, limit))
		}
		if readErr == nil {
			if result, err := mapBrowserPage(body, offset, mapper); err == nil {
				return result
			}
		}
		params := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}, "start": {strconv.FormatInt(start, 10)}, "end": {strconv.FormatInt(end, 10)}}
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
			if row := mapper(hit); row != nil {
				rows = append(rows, row)
			}
		}
		_ = c.writeRaw(chain, "internal", name, body)
		return browserPageResult{offset: offset, total: page.Total, hits: len(page.Hits), rows: rows}
	}

	first := fetchPage(0)
	if first.err != nil {
		return nil, 0, 0, first.err
	}
	total := first.total
	rows := append([]map[string]any(nil), first.rows...)
	oldest := oldestMappedTime(first.rows)
	target := total
	if target > browserMaxOffset {
		target = browserMaxOffset
	}
	offsets := make([]int, 0)
	for offset := limit; int64(offset) < target; offset += limit {
		offsets = append(offsets, offset)
	}
	workers := min(cap(c.sem), len(offsets))
	completed, fetched := 1, first.hits
	pageResults, errs := fetchBrowserPagesInBatches(ctx, offsets, workers, fetchPage, func(result browserPageResult) {
		completed++
		if result.err == nil {
			fetched += result.hits
		}
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: internal窗口 %d/%d 页，已取 %d/%d 行", strings.ToUpper(chain), completed, len(offsets)+1, fetched, target))
		}
	})
	sort.SliceStable(pageResults, func(i, j int) bool { return pageResults[i].offset < pageResults[j].offset })
	for _, result := range pageResults {
		rows = append(rows, result.rows...)
		if candidate := oldestMappedTime(result.rows); candidate > 0 && (oldest == 0 || candidate < oldest) {
			oldest = candidate
		}
	}
	if int64(fetched) < target {
		errs = append(errs, fmt.Errorf("internal incomplete: got %d/%d rows", fetched, target))
	}
	return rows, oldest, total, errors.Join(errs...)
}

func oldestMappedTime(rows []map[string]any) int64 {
	oldest := int64(0)
	for _, row := range rows {
		value, err := strconv.ParseInt(toString(row["transactionTime"]), 10, 64)
		if err == nil && value > 100000000000 {
			value /= 1000
		}
		if err == nil && value > 0 && (oldest == 0 || value < oldest) {
			oldest = value
		}
	}
	return oldest
}

func nextBrowserWindow(total, oldest, cursor int64) (int64, bool, error) {
	if total <= browserMaxOffset {
		return 0, true, nil
	}
	if oldest <= 1262304000 || oldest >= cursor {
		return 0, false, fmt.Errorf("cannot split browser window: total=%d oldest=%d cursor=%d", total, oldest, cursor)
	}
	return oldest - 1, false, nil
}
