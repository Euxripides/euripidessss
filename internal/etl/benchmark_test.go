package etl

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/etl/backend/internal/model"
)

// generateBenchmarkRows creates n transaction rows for benchmarking
func generateBenchmarkRows(n int) []model.TransactionRow {
	rows := make([]model.TransactionRow, n)
	cards := []string{"card1", "card2", "card3", "card4", "card5"}
	parties := []string{"party1", "party2", "party3", "party4", "party5"}
	for i := 0; i < n; i++ {
		c := cards[rand.Intn(len(cards))]
		p := parties[rand.Intn(len(parties))]
		dir := "进"
		if rand.Intn(2) == 0 {
			dir = "出"
		}
		rows[i] = model.TransactionRow{
			"交易时间":    fmt.Sprintf("2024-%02d-%02d", rand.Intn(12)+1, rand.Intn(28)+1),
			"交易金额":    fmt.Sprintf("%.2f", rand.Float64()*10000),
			"收付标志":    dir,
			"交易卡号":    c,
			"交易对手账卡号": p,
			"对手户名":    "test",
		}
	}
	return rows
}

func BenchmarkCleanTransactions(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("rows_%d", size), func(b *testing.B) {
			rows := generateBenchmarkRows(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result := CleanTransactions(rows)
				_ = len(result)
			}
		})
	}
}

func BenchmarkDeduplicateTransactions(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("rows_%d", size), func(b *testing.B) {
			rows := generateBenchmarkRows(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result := DeduplicateTransactions(rows)
				_ = len(result)
			}
		})
	}
}

func BenchmarkBuildSummary(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("rows_%d", size), func(b *testing.B) {
			rows := generateBenchmarkRows(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = BuildSummary(rows)
			}
		})
	}
}

func BenchmarkBuildFlowGraph(b *testing.B) {
	sizes := []int{100, 1000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("rows_%d", size), func(b *testing.B) {
			rows := generateBenchmarkRows(size)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = BuildFlowGraph(rows, 600)
			}
		})
	}
}

func BenchmarkFullPipeline(b *testing.B) {
	rows := generateBenchmarkRows(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cleaned := CleanTransactions(rows)
		deduped := DeduplicateTransactions(cleaned)
		_ = BuildSummary(deduped)
	}
}
