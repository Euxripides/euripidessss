package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveFullAndPartial(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, "1")
	if err := s.AddCertified("bsc", 56, "0xabc", "token_transfers",
		[]Interval{{From: 40_000_000, To: 50_000_000}}, 100, nil); err != nil {
		t.Fatal(err)
	}
	res := s.Resolve("bsc", "0xabc", "token_transfers", 45_000_000, 55_000_000, time.Now())
	if res.FullHit || len(res.Missing) != 1 || res.Missing[0].From != 50_000_001 {
		t.Fatalf("部分请求应只缺尾部: %+v", res)
	}
	res = s.Resolve("bsc", "0xabc", "token_transfers", 30_000_000, 60_000_000, time.Now())
	if res.FullHit || res.CoverageRatio != 10_000_001.0/30_000_001.0 {
		t.Fatalf("部分命中失败 ratio=%v: %+v", res.CoverageRatio, res)
	}
	if len(res.Missing) != 2 || res.Missing[0].To != 39_999_999 || res.Missing[1].From != 50_000_001 {
		t.Fatalf("缺口定位失败: %+v", res.Missing)
	}
}

func TestMergeCertifiedRanges(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, "1")
	_ = s.AddCertified("bsc", 56, "0xabc", "transactions",
		[]Interval{{From: 0, To: 99}, {From: 200, To: 299}}, 0, nil)
	_ = s.AddCertified("bsc", 56, "0xabc", "transactions",
		[]Interval{{From: 100, To: 199}}, 0, nil)
	res := s.Resolve("bsc", "0xabc", "transactions", 0, 299, time.Now())
	if !res.FullHit || len(res.Covered) != 1 || res.Covered[0].To != 299 {
		t.Fatalf("合并失败: %+v", res.Covered)
	}
}

func TestSnapshotTTL(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, "1")
	now := time.Now().UTC()
	snap := &SnapshotCoverage{Block: 52_000_000, Time: now, TTLSeconds: 300}
	_ = s.AddCertified("bsc", 56, "0xabc", "balances", nil, 1, snap)
	if res := s.Resolve("bsc", "0xabc", "balances", 0, 0, now.Add(60*time.Second)); !res.FullHit {
		t.Fatalf("TTL 内应命中: %+v", res)
	}
	if res := s.Resolve("bsc", "0xabc", "balances", 0, 0, now.Add(301*time.Second)); res.FullHit || res.Certification != "STALE" {
		t.Fatalf("过期应 STALE: %+v", res)
	}
}

func TestIncompatibleSchema(t *testing.T) {
	root := t.TempDir()
	old := NewStore(root, "2")
	_ = old.AddCertified("bsc", 56, "0xabc", "token_transfers",
		[]Interval{{From: 0, To: 99}}, 1, nil)
	cur := NewStore(root, "1")
	res := cur.Resolve("bsc", "0xabc", "token_transfers", 0, 99, time.Now())
	if res.FullHit || res.Compatible {
		t.Fatalf("兼容键不同应不复用: %+v", res)
	}
}

func TestShardedFilesAndRebuild(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, "1")
	_ = s.AddCertified("bsc", 56, "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd", "transactions",
		[]Interval{{From: 0, To: 9}}, 5, nil)
	shardDir := filepath.Join(root, "smart_download", "registry", "coverage", "bsc", "ab")
	if _, err := os.Stat(filepath.Join(shardDir, "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd.json")); err != nil {
		t.Fatalf("分片文件未写入: %v", err)
	}
	// Rebuild 重建
	s2 := NewStore(root, "1")
	s2.Rebuild([]RebuildInput{{
		ChainKey: "bsc", ChainID: 56, Address: "0xabc",
		Dataset: "token_transfers", FromBlock: 10, ToBlock: 19,
		Rows: 4, Certified: true, UpdatedAt: time.Now(),
	}})
	if res := s2.Resolve("bsc", "0xabc", "token_transfers", 10, 19, time.Now()); !res.FullHit {
		t.Fatalf("Rebuild 后未命中: %+v", res)
	}
}
