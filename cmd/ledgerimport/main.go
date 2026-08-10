// Command ledgerimport imports Pangu BSC token ledgers and per-address flow
// exports into the onchain ClickHouse warehouse with cross-source dedup.
//
// Usage:
//
//	go run ./cmd/ledgerimport -ledger-root "E:\项目\虚拟币\贺州_盘古\分析\盘古-数据分析\盘古-数据分析\资金分析" -flows-root "E:\项目\虚拟币\贺州_盘古\分析\盘古-数据分析\盘古-数据分析\资金流水明细"
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/etl/backend/internal/ledgerimport"
)

func main() {
	cfg := ledgerimport.Config{}
	flag.StringVar(&cfg.LedgerRoot, "ledger-root", "", "root directory containing the 交付_* full-ledger deliveries")
	flag.StringVar(&cfg.FlowsRoot, "flows-root", "", "root directory containing the 资金流水明细 exports")
	flag.StringVar(&cfg.JobID, "job-id", "pangu-ledger-20260810", "stable ingest job id (also used for dedup provenance)")
	flag.StringVar(&cfg.CredentialFile, "credential-file", `E:\database\clickhouse\config\clickhouse.env`, "ClickHouse credential env file")
	flag.IntVar(&cfg.MaxBatchRows, "max-batch-rows", 200_000, "rows per staging insert batch")
	flag.BoolVar(&cfg.DropStagingAfter, "drop-staging-after", true, "drop staging tables after verification")
	flag.BoolVar(&cfg.SkipCompleted, "skip-completed", true, "skip source groups already recorded COMPLETED in migration_manifest")
	timeoutSec := flag.Int("request-timeout-sec", 1800, "ClickHouse HTTP request timeout in seconds")
	flag.Parse()

	if cfg.LedgerRoot == "" || cfg.FlowsRoot == "" {
		log.Fatalf("-ledger-root and -flows-root are required")
	}
	for _, root := range []string{cfg.LedgerRoot, cfg.FlowsRoot} {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			log.Fatalf("directory not found: %s", root)
		}
	}
	cfg.RequestTimeout = time.Duration(*timeoutSec) * time.Second
	cfg.Logger = log.New(os.Stdout, "[ledgerimport] ", log.LstdFlags)

	logPath := filepath.Join(filepath.Dir(cfg.FlowsRoot), "ledger_import.log")
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		cfg.Logger = log.New(io.MultiWriter(os.Stdout, logFile), "[ledgerimport] ", log.LstdFlags)
		defer logFile.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()

	result, err := ledgerimport.Run(ctx, cfg)
	if err != nil {
		log.Fatalf("import failed: %v", err)
	}
	log.Printf("import completed: transfers staged=%d unique=%d inserted=%d; txs staged=%d unique=%d inserted=%d; activity=%d; tokens=%d; coverage=%d; manifest=%d",
		result.StagedTransfers, result.UniqueTransfers, result.InsertedTransfers,
		result.StagedTxs, result.UniqueTxs, result.InsertedTxs,
		result.ActivityRows, result.InsertedTokens, result.CoverageRows, result.ManifestRows)
	for _, s := range result.Stats {
		log.Printf("source %-22s rows=%d parsed=%d rejected=%d skipped=%d sha=%s", s.RangeID, s.RowsRead, s.ParsedRows, s.Rejected, s.SkippedCovered, s.SHA256)
	}
}
