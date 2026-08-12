package downloadengine

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/chain"
	"github.com/etl/backend/internal/datasource/sqd"
)

// ── Batch collect real BSC addresses across multiple SQD rounds ──

func TestBatchCollect500KAddresses(t *testing.T) {
	if testing.Short() || os.Getenv("DOWNLOADENGINE_REAL_BATCH_COLLECT") != "1" {
		t.Skip("set DOWNLOADENGINE_REAL_BATCH_COLLECT=1 to run the networked 500K address collector")
	}

	outDir := filepath.Join("..", "..", "stress-data", "bsc_real")
	_ = os.MkdirAll(outDir, 0755)
	outPath := filepath.Join(outDir, "addresses_accumulated.csv")

	// Load existing
	existing := make(map[string]bool)
	if f, err := os.Open(outPath); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			addr := strings.TrimSpace(strings.ToLower(scanner.Text()))
			if len(addr) == 42 && addr[:2] == "0x" {
				existing[addr] = true
			}
		}
		f.Close()
	}
	t.Logf("Existing addresses: %d", len(existing))

	client := sqd.New(&http.Client{Timeout: 120 * time.Second})
	network, err := chain.Resolve("bsc")
	if err != nil {
		t.Skipf("chain: %v", err)
	}

	// 动态解析最新 finalized 区块窗口（避免重复扫描旧窗口）
	// 用近期日期（7-31）解析，若失败退回固定起点
	blockStart := uint64(107153260)
	if resolved, err := client.ResolveDateRange(context.Background(), network, "2026-07-31", "2026-07-31"); err == nil {
		blockStart = resolved.From
		t.Logf("动态区块起点: %d (7-31 finalized 窗口)", blockStart)
	} else {
		t.Logf("日期解析失败，使用固定起点: %d (%v)", blockStart, err)
	}
	batchSize := uint64(200)
	maxRounds := 40

	file, err := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	collectStart := time.Now()
	totalRounds := 0

	for round := 0; round < maxRounds; round++ {
		currentStart := blockStart + uint64(round)*uint64(batchSize)
		currentEnd := currentStart + batchSize

		var newAddrs int
		// nil addresses = 全量扫描该区块窗口（过滤条件为空时返回全部交易）
		err = client.StreamTransactions(context.Background(), network,
			sqd.BlockRange{From: currentStart, To: currentEnd}, nil,
			func(block sqd.Block) error {
				for _, tx := range block.Transactions {
					for _, a := range []string{tx.From, tx.To} {
						a = strings.ToLower(a)
						if a == "" || len(a) != 42 {
							continue
						}
						if !existing[a] {
							existing[a] = true
							fmt.Fprintln(file, a)
							newAddrs++
						}
					}
				}
				return nil
			})

		totalRounds++
		if err != nil {
			t.Logf("  round %d: block %d-%d: %v (%d new, %d total) — waiting cooldown",
				round+1, currentStart, currentEnd, err, newAddrs, len(existing))
			if strings.Contains(err.Error(), "cooldown") || strings.Contains(err.Error(), "503") {
				time.Sleep(65 * time.Second) // wait for cooldown to expire
				continue
			}
		} else {
			t.Logf("  round %d: block %d-%d: OK (+%d new, %d total)",
				round+1, currentStart, currentEnd, newAddrs, len(existing))
		}

		if len(existing) >= 500000 {
			t.Logf("  TARGET REACHED: %d addresses!", len(existing))
			break
		}

		time.Sleep(2 * time.Second) // rate limit
	}

	collectDur := time.Since(collectStart)
	t.Logf("=== Batch Complete: %d unique addresses in %d rounds over %v ===",
		len(existing), totalRounds, collectDur)
}
