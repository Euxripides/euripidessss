package downloadengine

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

// ── Real BSC Data Stress via SQD (no API key required) ──

func TestRealBSCHundredBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real SQD test in short mode")
	}

	client := sqd.New(&http.Client{Timeout: 90 * time.Second})
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Skipf("chain resolve: %v", err)
	}

	// 取 100 个区块 (44000000-44000100)
	startBlock := uint64(44000000)
	endBlock := startBlock + 100

	t.Logf("=== BSC Real Blocks: %d → %d ===", startBlock, endBlock)

	var blocks []sqd.Block
	var totalTx, totalLogs int
	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	streamStart := time.Now()
	err = client.StreamLogs(context.Background(), network, sqd.BlockRange{From: startBlock, To: endBlock}, nil,
		func(block sqd.Block) error {
			blocks = append(blocks, block)
			totalTx += len(block.Transactions)
			totalLogs += len(block.Logs)
			return nil
		})
	streamDur := time.Since(streamStart)

	if err != nil {
		t.Fatalf("SQD StreamLogs: %v", err)
	}

	runtime.GC()
	runtime.ReadMemStats(&memAfter)

	t.Logf("  Blocks: %d, TXs: %d, Logs: %d in %v", len(blocks), totalTx, totalLogs, streamDur)
	t.Logf("  Throughput: %.0f blocks/s, %d tx/s, %d logs/s",
		float64(len(blocks))/streamDur.Seconds(),
		int(float64(totalTx)/streamDur.Seconds()),
		int(float64(totalLogs)/streamDur.Seconds()))
	t.Logf("  Memory Δ: %.2f MB, blocks/slice: %.1f KB/block",
		float64(memAfter.Alloc-memBefore.Alloc)/1e6,
		float64(memAfter.Alloc-memBefore.Alloc)/float64(len(blocks))/1024)

	// Chunk 生成模拟
	chunkStart := time.Now()
	chunks := (endBlock-startBlock+50000-1)/50000
	_ = time.Since(chunkStart)

	t.Logf("  Chunks: %d (50K block chunks)", chunks)
}

func TestRealBSCCountAddresses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real SQD count test in short mode")
	}

	client := sqd.New(&http.Client{Timeout: 90 * time.Second})
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Skipf("chain resolve: %v", err)
	}

	// 查更多区块获取地址分布
	startBlock := uint64(44000000)
	endBlock := startBlock + 200

	t.Logf("=== BSC Address Count: %d blocks ===", endBlock-startBlock)

	addrSet := make(map[string]int)
	totalTx := 0

	countStart := time.Now()
	err = client.StreamLogs(context.Background(), network, sqd.BlockRange{From: startBlock, To: endBlock}, nil,
		func(block sqd.Block) error {
			for _, tx := range block.Transactions {
				addrSet[strings.ToLower(tx.From)]++
				if tx.To != "" {
					addrSet[strings.ToLower(tx.To)]++
				}
				totalTx++
			}
			return nil
		})
	countDur := time.Since(countStart)

	if err != nil {
		t.Fatalf("SQD StreamLogs: %v", err)
	}

	t.Logf("  Unique addresses: %d from %d TXs in %v",
		len(addrSet), totalTx, countDur)
	t.Logf("  Rate: %.0f blocks/s, %d addresses/s",
		float64(endBlock-startBlock)/countDur.Seconds(),
		int(float64(len(addrSet))/countDur.Seconds()))

	// Top 5 addresses
	type addrCount struct{ addr string; count int }
	sorted := make([]addrCount, 0, len(addrSet))
	for a, c := range addrSet {
		sorted = append(sorted, addrCount{a, c})
	}
	// sort by count descending (quick partial)
	for i := 0; i < 5 && i < len(sorted); i++ {
		maxIdx := i
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[maxIdx].count {
				maxIdx = j
			}
		}
		sorted[i], sorted[maxIdx] = sorted[maxIdx], sorted[i]
		t.Logf("  #%d: %s = %d TXs", i+1, sorted[i].addr[:20]+"...", sorted[i].count)
	}
}

func TestRealBSCThroughputBenchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real SQD benchmark in short mode")
	}

	client := sqd.New(&http.Client{Timeout: 120 * time.Second})
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Skipf("chain resolve: %v", err)
	}

	// 逐级增大: 100 → 500 → 1000 blocks
	levels := []struct {
		name  string
		count uint64
	}{
		{"100_blocks", 100},
		{"500_blocks", 500},
	}

	startBlock := uint64(44000000)
	for _, lv := range levels {
		t.Run(lv.name, func(t *testing.T) {
			var totalTx, totalLogs, blockCount int
			streamStart := time.Now()
			err = client.StreamLogs(context.Background(), network,
				sqd.BlockRange{From: startBlock, To: startBlock + lv.count}, nil,
				func(block sqd.Block) error {
					blockCount++
					totalTx += len(block.Transactions)
					totalLogs += len(block.Logs)
					return nil
				})
			streamDur := time.Since(streamStart)

			if err != nil {
				t.Fatalf("SQD: %v", err)
			}
			t.Logf("  %d blocks, %d TXs, %d logs in %v (%.0f blocks/s, %.0f kB/s)",
				blockCount, totalTx, totalLogs, streamDur,
				float64(blockCount)/streamDur.Seconds(),
				float64(blockCount*2)/streamDur.Seconds())
		})
	}

	_ = fmt.Sprintf
}

// ── Real BSC with known active addresses ──

func TestRealBSCWithActiveAddresses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real SQD address test in short mode")
	}

	client := sqd.New(&http.Client{Timeout: 120 * time.Second})
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Skipf("chain resolve: %v", err)
	}

	// BSC 已知高活跃地址（USDT, PancakeSwap Router, WBNB, CAKE, BUSD）
	activeAddrs := []string{
		"0x55d398326f99059ff775485246999027b3197955", // USDT
		"0x10ed43c718714eb63d5aa57b78b54704e256024e", // PancakeSwap Router
		"0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c", // WBNB
		"0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82", // CAKE
		"0xe9e7cea3dedca5984780bafc599bd69add087d56", // BUSD
		"0x2170ed0880ac9a755fd29b2688956bd959f933f8", // ETH
		"0x7130d2a12b9bcbfae4f2634d864a1ee1ce3ead9c", // BTCB
	}

	startBlock := uint64(44000000)
	endBlock := startBlock + 200

	t.Logf("=== BSC Active Addresses: %d blocks, %d addresses ===", endBlock-startBlock, len(activeAddrs))

	addrTxCount := make(map[string]int)
	addrSet := make(map[string]bool)
	var totalTx, totalLogs, blockCount int

	streamStart := time.Now()
	err = client.StreamLogs(context.Background(), network,
		sqd.BlockRange{From: startBlock, To: endBlock}, activeAddrs,
		func(block sqd.Block) error {
			blockCount++
			for _, tx := range block.Transactions {
				totalTx++
				addrTxCount[strings.ToLower(tx.From)]++
				if tx.To != "" {
					addrTxCount[strings.ToLower(tx.To)]++
				}
				addrSet[strings.ToLower(tx.From)] = true
				if tx.To != "" {
					addrSet[strings.ToLower(tx.To)] = true
				}
			}
			totalLogs += len(block.Logs)
			return nil
		})
	streamDur := time.Since(streamStart)

	if err != nil {
		t.Fatalf("SQD: %v", err)
	}

	t.Logf("  Blocks: %d, TXs: %d, Logs: %d in %v", blockCount, totalTx, totalLogs, streamDur)
	t.Logf("  Unique addresses: %d (contacted by %d known active)", len(addrSet), len(activeAddrs))

	if totalTx > 0 {
		t.Logf("  Throughput: %.0f blocks/s, %.0f tx/s, %.0f logs/s",
			float64(blockCount)/streamDur.Seconds(),
			float64(totalTx)/streamDur.Seconds(),
			float64(totalLogs)/streamDur.Seconds())

		// Top 5 地址
		type ac struct{ a string; c int }
		var sorted []ac
		for a, c := range addrTxCount {
			sorted = append(sorted, ac{a, c})
		}
		for i := 0; i < 5 && i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].c > sorted[i].c {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
			t.Logf("  #%d: %s = %d TXs", i+1, sorted[i].a[:20]+"...", sorted[i].c)
		}
	} else {
		t.Log("  (no transactions in this block range for these addresses)")
	}
}
