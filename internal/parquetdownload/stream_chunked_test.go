package parquetdownload

import (
	"context"
	"errors"
	"testing"

	"github.com/etl/backend/internal/datasource/sqd"
)

// TestStreamChunkedResumesFromLastHandled 验证 503 中断后从最后处理块续拉（不重复写行）。
func TestStreamChunkedResumesFromLastHandled(t *testing.T) {
	var calls []sqd.BlockRange
	call := 0
	err := streamChunked(context.Background(), SQDBlockRange{From: 100, To: 260}, 100,
		func(ctx context.Context, rng sqd.BlockRange, lastHandled *uint64) error {
			call++
			calls = append(calls, rng)
			// 模拟分片 1：处理到块 140 后遇 503 中断
			if call == 1 {
				*lastHandled = 140
				return errors.New("sqd: cooling down after 503 No available workers")
			}
			// 后续调用正常完成
			*lastHandled = rng.To
			return nil
		})
	if err != nil {
		t.Fatalf("streamChunked 应成功（重试后），得到: %v", err)
	}
	// 期望调用序列：100-199（失败于 140）→ 141-199（续拉）→ 200-260
	if len(calls) != 3 {
		t.Fatalf("期望 3 次调用，得到 %d: %+v", len(calls), calls)
	}
	if calls[0].From != 100 || calls[1].From != 141 || calls[2].From != 200 {
		t.Fatalf("断点续拉错误: %+v", calls)
	}
}

// TestStreamChunkedNonRetryableStops 验证不可恢复错误（schema）直接返回。
func TestStreamChunkedNonRetryableStops(t *testing.T) {
	calls := 0
	err := streamChunked(context.Background(), SQDBlockRange{From: 100, To: 200}, 100,
		func(ctx context.Context, rng sqd.BlockRange, lastHandled *uint64) error {
			calls++
			return errors.New("SQD Schema 探测失败")
		})
	if err == nil {
		t.Fatal("schema 错误应直接返回")
	}
	if calls != 1 {
		t.Fatalf("不可恢复错误不应重试，调用 %d 次", calls)
	}
}
