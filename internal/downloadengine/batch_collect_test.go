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
	if testing.Short() {
		t.Skip("batch collection skipped in short mode")
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

	// 已知活跃地址作为种子
	seeds := []string{
		"0x55d398326f99059ff775485246999027b3197955",
		"0x10ed43c718714eb63d5aa57b78b54704e256024e",
		"0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c",
		"0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82",
		"0xe9e7cea3dedca5984780bafc599bd69add087d56",
		"0x2170ed0880ac9a755fd29b2688956bd959f933f8",
		"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d",
		"0xcA143Ce32Fe78f1f7019d7d551a6402fC5350c73",
	}

	blockStart := uint64(44500000)
	batchSize := uint64(200)
	maxRounds := 20

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
		err = client.StreamLogs(context.Background(), network,
			sqd.BlockRange{From: currentStart, To: currentEnd}, seeds,
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
				for _, log := range block.Logs {
					a := strings.ToLower(log.Address)
					if len(a) == 42 && !existing[a] {
						existing[a] = true
						fmt.Fprintln(file, a)
						newAddrs++
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
