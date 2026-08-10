package canonicalregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
)

func TestRepositoryClickHouseIntegration(t *testing.T) {
	if os.Getenv("CLICKHOUSE_REGISTRY_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_REGISTRY_INTEGRATION=1")
	}
	port, _ := strconv.Atoi(os.Getenv("CLICKHOUSE_HTTP_PORT"))
	if port == 0 {
		port = 8123
	}
	cfg := clickhouse.Config{Enabled: true, Host: envDefault("CLICKHOUSE_HOST", "127.0.0.1"), HTTPPort: port, Database: envDefault("CLICKHOUSE_DATABASE", "onchain"), User: envDefault("CLICKHOUSE_USER", "etl_app"), Password: os.Getenv("CLICKHOUSE_PASSWORD"), RequestTimeout: 20 * time.Second}
	client, err := clickhouse.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	seed := sha256.Sum256([]byte(fmt.Sprintf("registry-%d", time.Now().UnixNano())))
	hexSeed := hex.EncodeToString(seed[:])
	selector := "0x" + hexSeed[:8]
	address := "0x" + hexSeed[:40]
	txHash := "0x" + hexSeed
	topic := txHash
	entityID := deterministicUUID(hexSeed, "entity")
	jobID := deterministicUUID(hexSeed, "job")
	source := "integration_" + hexSeed[:12]
	now := time.Now().UTC().Truncate(time.Millisecond)
	repo := New(client)
	defer func() {
		queries := []string{
			fmt.Sprintf("ALTER TABLE onchain.method_registry DELETE WHERE method_id = '%s' SETTINGS mutations_sync=1", selector),
			fmt.Sprintf("ALTER TABLE onchain.token_metadata_registry DELETE WHERE chain_id = 56 AND contract_address = '%s' SETTINGS mutations_sync=1", address), fmt.Sprintf("ALTER TABLE onchain.token_metadata_history DELETE WHERE chain_id = 56 AND contract_address = '%s' SETTINGS mutations_sync=1", address), fmt.Sprintf("ALTER TABLE onchain.abi_registry DELETE WHERE chain_id = 56 AND contract_address = '%s' SETTINGS mutations_sync=1", address), fmt.Sprintf("ALTER TABLE onchain.entity_registry DELETE WHERE entity_id = '%s' SETTINGS mutations_sync=1", entityID), fmt.Sprintf("ALTER TABLE onchain.address_labels DELETE WHERE chain_id = 56 AND address = '%s' SETTINGS mutations_sync=1", address), fmt.Sprintf("ALTER TABLE onchain.token_prices DELETE WHERE chain_id = 56 AND token_address = '%s' SETTINGS mutations_sync=1", address), fmt.Sprintf("ALTER TABLE onchain.parsed_events DELETE WHERE chain_id = 56 AND tx_hash = '%s' SETTINGS mutations_sync=1", txHash), fmt.Sprintf("ALTER TABLE onchain.semantic_jobs DELETE WHERE job_id = '%s' SETTINGS mutations_sync=1", jobID)}
		for _, query := range queries {
			_ = client.Exec(context.Background(), query)
		}
	}()
	for _, signature := range []string{"alpha(address)", "beta(uint256)"} {
		if err = repo.UpsertMethod(ctx, MethodRecord{MethodID: selector, CanonicalSignature: signature, DisplayName: signature, Source: source, Confidence: "HIGH", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	method, err := repo.ResolveMethod(ctx, selector)
	if err != nil || !method.Ambiguous {
		t.Fatalf("method conflict: %+v %v", method, err)
	}
	token := TokenMetadata{ChainID: 56, ContractAddress: address, Name: "Integration Token", Symbol: "INT", Decimals: 18, TokenStandard: "BEP20", LogoSource: source, MetadataSource: source, MetadataConfidence: "HIGH", FirstSeenTime: now, MetadataUpdatedAt: now, UpdatedAt: now}
	if err = repo.UpsertTokenMetadata(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetTokenMetadataAsOf(ctx, 56, address, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertABI(ctx, ABIRecord{ChainID: 56, ContractAddress: address, ABIJSON: `[]`, Source: source, Verified: true, ObservedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetPreferredABI(ctx, 56, address, nil); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertEntity(ctx, Entity{EntityID: entityID, Name: "Integration Entity", Type: "TEST", RiskLevel: "NONE", Source: source, Confidence: "HIGH", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetEntity(ctx, entityID); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertAddressLabel(ctx, AddressLabel{ChainID: 56, Address: address, LabelName: "Integration", LabelType: "ENTITY", EntityID: &entityID, Source: source, Confidence: "HIGH", FirstSeen: now, LastVerified: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertAddressLabel(ctx, AddressLabel{ChainID: 56, Address: address, LabelName: "No Entity", LabelType: "USER", Source: source, Confidence: "LOW", FirstSeen: now, LastVerified: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if labels, labelErr := repo.ListAddressLabels(ctx, 56, address, nil); labelErr != nil || len(labels) != 2 {
		t.Fatalf("labels=%d err=%v", len(labels), labelErr)
	}
	if err = repo.UpsertTokenPrice(ctx, TokenPrice{ChainID: 56, TokenAddress: address, TimestampBucket: now, PriceUSD: "1.25", Source: source, Confidence: "HIGH", ObservedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetTokenPriceAsOf(ctx, 56, address, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = repo.UpsertParsedEvent(ctx, ParsedEvent{ChainID: 56, BlockNumber: 1, BlockTime: now, TransactionHash: txHash, ContractAddress: address, Topic0: topic, EventName: "Integration", EventSignature: "Integration()", DecodedFields: `{}`, DecoderSource: source, DecoderConfidence: "HIGH", ParserVersion: "v2.0", SchemaVersion: 2, IngestedAt: now}); err != nil {
		t.Fatal(err)
	}
	if events, eventErr := repo.ListParsedEvents(ctx, 56, txHash); eventErr != nil || len(events) != 1 {
		t.Fatalf("events=%d err=%v", len(events), eventErr)
	}
	job := SemanticJob{JobID: jobID, JobType: "REENRICH", ChainID: 56, Dataset: "labels", FromBlock: 1, ToBlock: 2, TargetVersion: "v2.0", Status: "SUCCEEDED", ProcessedRows: 1, CreatedAt: now, UpdatedAt: now}
	if err = repo.UpsertSemanticJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetSemanticJob(ctx, jobID); err != nil {
		t.Fatal(err)
	}
}

func envDefault(name, value string) string {
	if configured := os.Getenv(name); configured != "" {
		return configured
	}
	return value
}
