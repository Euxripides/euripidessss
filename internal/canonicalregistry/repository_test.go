package canonicalregistry

import (
	"context"
	"encoding/csv"
	"io"
	"strings"
	"testing"
	"time"
)

const (
	testAddress  = "0x1111111111111111111111111111111111111111"
	testAddress2 = "0x2222222222222222222222222222222222222222"
	testTxHash   = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type insertCall struct {
	table           string
	columns, values []string
}
type fakeClient struct {
	rows    []map[string]any
	query   string
	inserts []insertCall
	err     error
}

func (f *fakeClient) QueryJSON(_ context.Context, query string) ([]map[string]any, error) {
	f.query = query
	return f.rows, f.err
}
func (f *fakeClient) InsertCSV(_ context.Context, table string, columns []string, rows io.Reader) error {
	reader := csv.NewReader(rows)
	values, err := reader.Read()
	if err != nil {
		return err
	}
	f.inserts = append(f.inserts, insertCall{table: table, columns: append([]string(nil), columns...), values: values})
	return f.err
}

func TestResolveMethodConflictIsDeterministicallyAmbiguous(t *testing.T) {
	now := "2026-08-09T01:02:03Z"
	client := &fakeClient{rows: []map[string]any{
		{"method_id": "0xa9059cbb", "canonical_signature": "transfer(bytes32,uint256)", "display_name": "Transfer Bytes", "source": "fourbyte", "confidence": "MEDIUM", "is_verified": false, "updated_at": now},
		{"method_id": "0xa9059cbb", "canonical_signature": "transfer(address,uint256)", "display_name": "Transfer", "source": "erc20", "confidence": "HIGH", "is_verified": true, "updated_at": now},
	}}
	result, err := New(client).ResolveMethod(context.Background(), "0xA9059CBB")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ambiguous || result.Name != "ambiguous" || len(result.CandidateSignatures) != 2 {
		t.Fatalf("unexpected resolution: %+v", result)
	}
	if result.CandidateSignatures[0] != "transfer(address,uint256)" {
		t.Fatalf("candidates not sorted: %#v", result.CandidateSignatures)
	}
}

func TestResolveMethodRejectsSQLInjection(t *testing.T) {
	client := &fakeClient{}
	_, err := New(client).ResolveMethod(context.Background(), "0xa9059cbb' OR 1=1 --")
	if err == nil || client.query != "" {
		t.Fatalf("unsafe input reached query: err=%v query=%q", err, client.query)
	}
}

func TestTokenIdentityIncludesChainAndContract(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	client := &fakeClient{}
	repo := New(client)
	base := TokenMetadata{ChainID: 56, ContractAddress: testAddress, Name: "Token", Symbol: "USDT", Decimals: 18, TokenStandard: "BEP20", LogoSource: "LOCAL", MetadataSource: "MANUAL", MetadataConfidence: "HIGH", FirstSeenTime: now, MetadataUpdatedAt: now, UpdatedAt: now}
	if err := repo.UpsertTokenMetadata(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.ChainID = 1
	base.ContractAddress = testAddress2
	if err := repo.UpsertTokenMetadata(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if len(client.inserts) != 4 {
		t.Fatalf("want registry+history twice, got %d", len(client.inserts))
	}
	if client.inserts[0].values[0] == client.inserts[2].values[0] || client.inserts[0].values[1] == client.inserts[2].values[1] {
		t.Fatal("token identities were merged")
	}
	if client.inserts[1].values[2] == client.inserts[3].values[2] {
		t.Fatal("history observation identity ignored chain/contract")
	}
}

func TestTokenHistoryRetryUsesDeterministicObservationID(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	client := &fakeClient{}
	repo := New(client)
	token := TokenMetadata{ChainID: 56, ContractAddress: testAddress, Name: "A, quoted \"token\"", Symbol: "TOK", Decimals: 18, TokenStandard: "BEP20", LogoSource: "LOCAL", MetadataSource: "MANUAL", MetadataConfidence: "HIGH", FirstSeenTime: now, MetadataUpdatedAt: now, UpdatedAt: now}
	if err := repo.UpsertTokenMetadata(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertTokenMetadata(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if client.inserts[1].values[2] != client.inserts[3].values[2] {
		t.Fatal("retry created a different observation id")
	}
	if client.inserts[0].values[2] != token.Name {
		t.Fatal("CSV encoding corrupted token name")
	}
}

func TestHistoricalPriceUsesAsOfAndStrictIdentity(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	client := &fakeClient{rows: []map[string]any{{"chain_id": float64(56), "token_address": testAddress, "timestamp_bucket": now.Format(time.RFC3339Nano), "price_usd": "1.002", "source": "provider", "confidence": "HIGH", "observed_at": now.Format(time.RFC3339Nano), "updated_at": now.Format(time.RFC3339Nano)}}}
	result, err := New(client).GetTokenPriceAsOf(context.Background(), 56, testAddress, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.PriceUSD != "1.002" {
		t.Fatalf("unexpected price: %+v", result)
	}
	if !strings.Contains(client.query, "timestamp_bucket <=") || !strings.Contains(client.query, "chain_id = 56") || !strings.Contains(client.query, testAddress) {
		t.Fatalf("missing as-of identity predicates: %s", client.query)
	}
}

func TestABIHashMustMatchPayload(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	client := &fakeClient{}
	err := New(client).UpsertABI(context.Background(), ABIRecord{ChainID: 56, ContractAddress: testAddress, ABIHash: strings.Repeat("0", 64), ABIJSON: `[]`, Source: "verified", ObservedAt: now})
	if err == nil || len(client.inserts) != 0 {
		t.Fatalf("mismatched ABI hash accepted: %v", err)
	}
}

func TestAddressLabelRejectsInvalidTypeAndEntityID(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	client := &fakeClient{}
	entity := "not-a-uuid"
	err := New(client).UpsertAddressLabel(context.Background(), AddressLabel{ChainID: 56, Address: testAddress, LabelName: "Binance", LabelType: "ENTITY", EntityID: &entity, Source: "manual", Confidence: "HIGH", FirstSeen: now, LastVerified: now})
	if err == nil || len(client.inserts) != 0 {
		t.Fatalf("invalid entity id accepted: %v", err)
	}
	err = New(client).UpsertAddressLabel(context.Background(), AddressLabel{ChainID: 56, Address: testAddress, LabelName: "x", LabelType: "ARBITRARY", Source: "manual", Confidence: "HIGH", FirstSeen: now, LastVerified: now})
	if err == nil {
		t.Fatal("invalid label type accepted")
	}
}

func TestParsedEventRequiresValidDecodedJSON(t *testing.T) {
	now := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	client := &fakeClient{}
	err := New(client).UpsertParsedEvent(context.Background(), ParsedEvent{ChainID: 56, BlockNumber: 1, BlockTime: now, TransactionHash: testTxHash, ContractAddress: testAddress, Topic0: testTxHash, EventName: "Transfer", EventSignature: "Transfer(address,address,uint256)", DecodedFields: "{", DecoderSource: "ABI", DecoderConfidence: "HIGH", ParserVersion: "v2.0", SchemaVersion: 2})
	if err == nil || len(client.inserts) != 0 {
		t.Fatalf("invalid decoded JSON accepted: %v", err)
	}
}
