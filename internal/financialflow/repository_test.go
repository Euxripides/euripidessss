package financialflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeCSVClient struct {
	query string
	body  string
}

func (f *fakeCSVClient) QueryCSV(_ context.Context, query string) (io.ReadCloser, error) {
	f.query = query
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func TestRepositoryStreamsBoundedDeterministicQuery(t *testing.T) {
	client := &fakeCSVClient{body: strings.Join([]string{
		testAddressA, testToken, "IN", "TRANSFER", "2025-01-01 00:00:00.000", "123", "4", "0xabc", "1", "100", "200", "LOCAL", "2025-01-01 00:00:00.000", "2", "TOKEN_TRANSFER", "SUCCESS", "archive",
	}, ",") + "\n"}
	repository := NewRepository(client)
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	batch, err := repository.Load(context.Background(), Query{ChainID: 56, Address: testAddressA, Token: testToken, From: from, To: from.Add(time.Hour), MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if batch.RowsRead != 1 || len(batch.Events) != 1 || batch.Events[0].USDValue != "200" {
		t.Fatalf("unexpected batch: %+v", batch)
	}
	for _, required := range []string{"FROM onchain.address_activity AS aa FINAL", "ANY LEFT JOIN", "ORDER BY block_time_text,block_number,transaction_index", "LIMIT 11", "token_address='" + testToken + "'"} {
		if !strings.Contains(client.query, required) {
			t.Fatalf("query missing %q:\n%s", required, client.query)
		}
	}
}

func TestRepositoryRejectsUnsafeQueryBeforeClient(t *testing.T) {
	client := &fakeCSVClient{}
	repository := NewRepository(client)
	from := time.Now().UTC()
	_, err := repository.Load(context.Background(), Query{ChainID: 56, Address: "x' OR 1=1 --", From: from, To: from.Add(time.Hour)})
	if !errors.Is(err, ErrInvalidQuery) || client.query != "" {
		t.Fatalf("expected pre-query rejection, got %v query=%q", err, client.query)
	}
}

func TestRepositoryFailsClosedOnRowLimit(t *testing.T) {
	row := strings.Join([]string{testAddressA, testToken, "IN", "TRANSFER", "2025-01-01 00:00:00.000", "1", "0", "0xabc", "1", "1", "", "", "", "1", "TOKEN_TRANSFER", "SUCCESS", "archive"}, ",") + "\n"
	client := &fakeCSVClient{body: row + row}
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	batch, err := NewRepository(client).Load(context.Background(), Query{ChainID: 56, Address: testAddressA, From: from, To: from.Add(time.Hour), MaxRows: 1})
	if !errors.Is(err, ErrRowLimit) || !batch.InputTruncated || batch.RowsRead != 2 {
		t.Fatalf("expected explicit row-limit failure, batch=%+v err=%v", batch, err)
	}
}

func TestBuildNativeQueryUsesEmptyTokenAddress(t *testing.T) {
	from := time.Now().UTC()
	query, _, err := BuildQuery(Query{ChainID: 56, Address: testAddressA, Token: NativeAssetID, From: from, To: from.Add(time.Hour), MaxRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "token_address=''") {
		t.Fatalf("native asset predicate missing: %s", query)
	}
	if !strings.Contains(query, "transaction_fee_native") || !strings.Contains(query, "'GAS_FEE' AS event_kind") {
		t.Fatalf("native query must stream gas as a separate ledger event: %s", query)
	}
}
