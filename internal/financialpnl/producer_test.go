package financialpnl

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProducerMaterializesConfirmedSwapAsSellAndBuy(t *testing.T) {
	fake := &fakeClient{}
	now := time.Now().UTC()
	err := NewProducer(fake).MaterializeSwaps(context.Background(), []CanonicalSwap{{
		ChainID: 56, Trader: validQuery(now).Address, TokenIn: validQuery(now).Token, AmountIn: "100", USDIn: "99",
		TokenOut: "0x3333333333333333333333333333333333333333", AmountOut: "50", USDOut: "100", GasUSD: ptr("1"),
		Time: now, BlockNumber: 10, TransactionHash: hash('a'), EventIndex: 7, SemanticConfidence: "VERIFIED",
		PriceVersion: "price-v1", DataSnapshotVersion: "facts-v1", IngestJobID: "job-1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.inserts) != 1 {
		t.Fatalf("inserts=%d", len(fake.inserts))
	}
	insert := fake.inserts[0]
	if !strings.Contains(insert, ",DEX_SELL,") || !strings.Contains(insert, ",DEX_BUY,") || strings.Count(insert, ",1,DEX_") != 0 {
		// Event indexes are doubled (14/15), avoiding sell/buy key collision.
		if !strings.Contains(insert, ",14,DEX_SELL,") || !strings.Contains(insert, ",15,DEX_BUY,") {
			t.Fatalf("canonical legs missing: %s", insert)
		}
	}
	lines := strings.Split(strings.TrimSpace(insert), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected table header plus two CSV rows: %q", insert)
	}
	if strings.Count(insert, ",1,") < 1 {
		t.Fatalf("sell gas missing: %s", insert)
	}
}

func TestProducerRejectsTransferMasqueradingAsSale(t *testing.T) {
	now := time.Now().UTC()
	fake := &fakeClient{}
	producer := NewProducer(fake)
	event := PositionEvent{Time: now, TransactionHash: hash('a'), Type: EventDEXSell, Amount: "1", USDValue: ptr("100"), SemanticSource: "TOKEN_TRANSFER", SemanticConfidence: "HIGH", PriceVersion: "p1", DataSnapshotVersion: "d1"}
	if err := producer.MaterializePositionEvents(context.Background(), validQuery(now), []PositionEvent{event}, "job"); err != ErrUnconfirmedTrade {
		t.Fatalf("err=%v", err)
	}
	if len(fake.inserts) != 0 {
		t.Fatal("unconfirmed sale was written")
	}

	known := KnownTrade{Query: validQuery(now), Side: EventKnownSell, Amount: "1", USDValue: "100", Time: now, TransactionHash: hash('b'), SemanticSource: "TOKEN_TRANSFER", SemanticConfidence: "HIGH", PriceVersion: "p1", DataSnapshotVersion: "d1"}
	if err := producer.MaterializeKnownTrades(context.Background(), []KnownTrade{known}); err != ErrUnconfirmedTrade {
		t.Fatalf("err=%v", err)
	}
	badTransfer := PositionEvent{Time: now, TransactionHash: hash('c'), Type: EventTransferOut, Amount: "1", SemanticSource: "DEX_SWAP", SemanticConfidence: "HIGH", DataSnapshotVersion: "facts-v1"}
	if err := producer.MaterializePositionEvents(context.Background(), validQuery(now), []PositionEvent{badTransfer}, "job"); err != ErrUnconfirmedTrade {
		t.Fatalf("non-canonical transfer source accepted: %v", err)
	}
}

func TestProducerWritesTransferAsPositionOnly(t *testing.T) {
	now := time.Now().UTC()
	fake := &fakeClient{}
	event := PositionEvent{Time: now, TransactionHash: hash('a'), Type: EventTransferOut, Amount: "5", SemanticSource: "TOKEN_TRANSFER", SemanticConfidence: "HIGH", DataSnapshotVersion: "facts-v1"}
	if err := NewProducer(fake).MaterializePositionEvents(context.Background(), validQuery(now), []PositionEvent{event}, "job"); err != nil {
		t.Fatal(err)
	}
	if len(fake.inserts) != 1 || !strings.Contains(fake.inserts[0], ",TRANSFER_OUT,") || strings.Contains(fake.inserts[0], ",DEX_SELL,") {
		t.Fatalf("wrong movement: %v", fake.inserts)
	}
}

func TestProducerRejectsLowConfidenceOrIncompleteSwap(t *testing.T) {
	now := time.Now().UTC()
	base := CanonicalSwap{ChainID: 56, Trader: validQuery(now).Address, TokenIn: validQuery(now).Token, AmountIn: "1", USDIn: "1", TokenOut: "0x3333333333333333333333333333333333333333", AmountOut: "1", USDOut: "1", Time: now, TransactionHash: hash('a'), SemanticConfidence: "LOW", PriceVersion: "p1", DataSnapshotVersion: "d1"}
	if err := NewProducer(&fakeClient{}).MaterializeSwaps(context.Background(), []CanonicalSwap{base}); err != ErrUnconfirmedTrade {
		t.Fatalf("err=%v", err)
	}
	base.SemanticConfidence = "HIGH"
	base.PriceVersion = ""
	if err := NewProducer(&fakeClient{}).MaterializeSwaps(context.Background(), []CanonicalSwap{base}); err == nil {
		t.Fatal("missing price version accepted")
	}
}
