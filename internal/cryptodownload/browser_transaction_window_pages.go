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
)

func (c *BrowserScraperClient) fetchTransactionWindowPages(ctx context.Context, chain, path string, start, end int64, limit int, progress func(string), mapper func(json.RawMessage) map[string]any) ([]map[string]any, int64, int64, error) {
	fetchPage := func(offset int) browserPageResult {
		name := browserWindowCacheName(end, offset)
		body, readErr := c.readRaw(chain, "transactions", name)
		var raw json.RawMessage
		if readErr == nil {
			parsed, err := parseBrowserAPIBody(body)
			if err == nil {
				raw = parsed
			}
		}
		if raw == nil {
			params := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(limit)}}
			if start > 0 {
				params.Set("start", strconv.FormatInt(start, 10))
			}
			if end > 0 {
				params.Set("end", strconv.FormatInt(end, 10))
			}
			fetchedRaw, fetchedBody, err := c.getRaw(ctx, path, params)
			if err != nil {
				return browserPageResult{offset: offset, err: err}
			}
			raw = fetchedRaw
			body = fetchedBody
			_ = c.writeRaw(chain, "transactions", name, body)
		}
		page := browserListPage{}
		if err := json.Unmarshal(raw, &page); err != nil {
			return browserPageResult{offset: offset, err: err}
		}
		result := browserPageResult{offset: offset, total: page.Total, hits: len(page.Hits)}
		for _, hit := range page.Hits {
			if row := mapper(hit); row != nil {
				result.rows = append(result.rows, row)
			}
			if blockTime, ok := rawBlockTime(hit); ok && (result.firstBlockTime == 0 || blockTime < result.firstBlockTime) {
				result.firstBlockTime = blockTime
			}
		}
		return result
	}

	first := fetchPage(0)
	if first.err != nil {
		return nil, 0, 0, first.err
	}
	total := first.total
	if total <= 0 {
		total = int64(first.hits)
	}
	target := min(total, int64(browserMaxOffset))
	if first.hits < limit {
		target = int64(first.hits)
	}
	offsets := make([]int, 0, (int(target)+limit-1)/limit)
	for offset := limit; int64(offset) < target; offset += limit {
		offsets = append(offsets, offset)
	}
	workers := min(cap(c.sem), len(offsets))
	completed, fetched := 1, first.hits
	results, fetchErrs := fetchBrowserPagesInBatches(ctx, offsets, workers, fetchPage, func(result browserPageResult) {
		completed++
		if result.err == nil {
			fetched += result.hits
		}
		if progress != nil {
			progress(fmt.Sprintf("浏览器爬取 %s: transactions窗口 %d/%d 页，已取 %d/%d 行", strings.ToUpper(chain), completed, len(offsets)+1, fetched, target))
		}
	})
	sort.SliceStable(results, func(i, j int) bool { return results[i].offset < results[j].offset })
	rows := append([]map[string]any(nil), first.rows...)
	oldest := first.firstBlockTime
	for _, result := range results {
		rows = append(rows, result.rows...)
		if result.firstBlockTime > 0 && (oldest == 0 || result.firstBlockTime < oldest) {
			oldest = result.firstBlockTime
		}
	}
	if int64(fetched) < target {
		fetchErrs = append(fetchErrs, fmt.Errorf("transactions incomplete: got %d/%d rows", fetched, target))
	}
	return rows, oldest, total, errors.Join(fetchErrs...)
}
