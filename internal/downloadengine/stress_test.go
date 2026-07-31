package downloadengine

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── V2.1 RC2 压力测试 ──

func TestStress500KAddressPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("pressure test skipped in short mode")
	}

	datasets := []struct {
		name string
		path string
	}{
		{"synthetic_10k", filepath.Join("..", "..", "stress-data", "synthetic", "addresses_10000.csv")},
		{"synthetic_100k", filepath.Join("..", "..", "stress-data", "synthetic", "addresses_100000.csv")},
		{"synthetic_500k", filepath.Join("..", "..", "stress-data", "synthetic", "addresses_500000.csv")},
	}

	for _, ds := range datasets {
		t.Run(ds.name, func(t *testing.T) {
			addresses, err := loadAddressCSV(ds.path)
			if err != nil {
				t.Skipf("skip %s: %v (run tools/stress-test/address-generator first)", ds.name, err)
			}
			t.Logf("%s: loaded %d addresses", ds.name, len(addresses))

			// Phase 1: Import + Validate + Deduplicate
			phase1Start := time.Now()
			seen := make(map[string]bool, len(addresses))
			valid, invalid, dup := 0, 0, 0
			for _, addr := range addresses {
				addr = strings.ToLower(strings.TrimSpace(addr))
				if !isValidEVMAddr(addr) {
					invalid++
					continue
				}
				if seen[addr] {
					dup++
					continue
				}
				seen[addr] = true
				valid++
			}
			phase1 := time.Since(phase1Start)
			t.Logf("  import+dedup: %d valid, %d invalid, %d dup in %v (%.0f addr/s)",
				valid, invalid, dup, phase1, float64(len(addresses))/phase1.Seconds())

			// Phase 2: Discovery (simulated)
			phase2Start := time.Now()
			block := uint64(5000000)
			discItems := make([]AddressDiscovery, valid)
			uniqueAddrs := make([]string, 0, valid)
			for addr := range seen {
				uniqueAddrs = append(uniqueAddrs, addr)
			}
			for i := 0; i < valid; i++ {
				discItems[i] = AddressDiscovery{
					Address:        uniqueAddrs[i],
					FirstSeenBlock: &block,
					Status:         FSFound,
					Coverage:       CoverageV2Full,
				}
			}
			phase2 := time.Since(phase2Start)
			t.Logf("  discovery: %d addresses in %v (%.0f addr/s)", valid, phase2, float64(valid)/phase2.Seconds())

			// Phase 3: Grouping
			phase3Start := time.Now()
			groups := PlanGroups(uniqueAddrs, discItems, 100)
			phase3 := time.Since(phase3Start)
			t.Logf("  grouping: %d groups in %v", len(groups), phase3)

			// Phase 4: Chunk generation
			phase4Start := time.Now()
			totalChunks := 0
			for _, g := range groups {
				chunkSize := uint64(100000)
				for blk := g.MinBlock; blk <= g.MaxBlock+5000000; blk += chunkSize {
					totalChunks++
					_ = blk
				}
			}
			phase4 := time.Since(phase4Start)
			t.Logf("  chunks: %d generated in %v", totalChunks, phase4)

			// Memory
			totalSec := phase1.Seconds() + phase2.Seconds() + phase3.Seconds() + phase4.Seconds()
			t.Logf("  TOTAL: %.2fs, %.0f addr/s (pipeline)", totalSec, float64(valid)/totalSec)
		})
	}
}

func TestStressConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrent stress test skipped in short mode")
	}

	// 多 goroutine 并发读写 Router + Planner
	router := NewRouter()
	planner := NewRangePlanner(nil)

	var wg sync.WaitGroup
	const workers = 16
	const iterations = 1000

	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = router.ResolveCapabilities()
				_, _ = router.ResolveStreaming("transactions", "bsc")
				_, _ = planner.planBlock(RangePlanRequest{
					StartBlock: u64ptr(uint64(j * 1000)),
					EndBlock:   u64ptr(uint64(j*1000 + 50000)),
				})
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	totalOps := workers * iterations * 3 // 3 ops per iteration
	t.Logf("concurrent stress: %d goroutines × %d iterations × 3 ops = %d ops in %v (%.0f ops/s)",
		workers, iterations, totalOps, elapsed, float64(totalOps)/elapsed.Seconds())
}

// ── Helpers ──

func loadAddressCSV(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var addrs []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1MB buffer
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			addrs = append(addrs, line)
		}
	}
	return addrs, scanner.Err()
}

func Benchmark500KAddressPipeline(b *testing.B) {
	path := filepath.Join("..", "..", "stress-data", "synthetic", "addresses_500000.csv")
	addresses, err := loadAddressCSV(path)
	if err != nil {
		b.Skipf("no dataset: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seen := make(map[string]bool, len(addresses))
		valid := 0
		for _, addr := range addresses {
			addr = strings.ToLower(strings.TrimSpace(addr))
			if !isValidEVMAddr(addr) {
				continue
			}
			if seen[addr] {
				continue
			}
			seen[addr] = true
			valid++
		}
		_ = valid
	}
	_ = fmt.Sprintf
}
