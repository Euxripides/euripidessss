package datasetevents

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestBusIdempotentConsumers(t *testing.T) {
	bus, err := NewBus(filepath.Join(t.TempDir(), "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var calls int32
	bus.Subscribe("c", func(_ context.Context, e Event) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	ev := Event{
		ID: IndexedEventID("job1/chunk1"), Type: DatasetIndexed,
		Addresses: []string{"0xaaa"}, RowCount: 10,
	}
	if err := bus.Publish(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("consumer calls = %d, want 1 (idempotent)", got)
	}
	// 重启重放：processed 已持久化，不得重复副作用
	bus2, err := NewBus(bus.path)
	if err != nil {
		t.Fatal(err)
	}
	var calls2 int32
	bus2.Subscribe("c", func(_ context.Context, e Event) error {
		atomic.AddInt32(&calls2, 1)
		return nil
	})
	bus2.Replay(ctx)
	if got := atomic.LoadInt32(&calls2); got != 0 {
		t.Fatalf("replay calls = %d, want 0 (processed persisted)", got)
	}
}
