package cryptodownload

import (
	"context"
	"fmt"
	"sync"
)

type browserPageFetch func(offset int) browserPageResult

func fetchBrowserPagesInBatches(ctx context.Context, offsets []int, workers int, fetch browserPageFetch, observe func(browserPageResult)) ([]browserPageResult, []error) {
	if workers < 1 {
		workers = 1
	}
	results := make([]browserPageResult, 0, len(offsets))
	for start := 0; start < len(offsets); start += workers {
		if err := ctx.Err(); err != nil {
			return results, []error{err}
		}
		end := start + workers
		if end > len(offsets) {
			end = len(offsets)
		}
		batch := make([]browserPageResult, end-start)
		var wg sync.WaitGroup
		wg.Add(len(batch))
		for i, offset := range offsets[start:end] {
			go func() {
				defer wg.Done()
				batch[i] = fetch(offset)
			}()
		}
		wg.Wait()
		var errs []error
		for _, result := range batch {
			if observe != nil {
				observe(result)
			}
			if result.err != nil {
				errs = append(errs, fmt.Errorf("offset %d: %w", result.offset, result.err))
				continue
			}
			results = append(results, result)
		}
		if len(errs) > 0 {
			return results, errs
		}
	}
	return results, nil
}
