package explorer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/etl/backend/internal/clickhouse"
	"github.com/etl/backend/internal/config"
)

func TestClickHousePaginationOver100K(t *testing.T) {
	if os.Getenv("CLICKHOUSE_PAGINATION_INTEGRATION") != "1" {
		t.Skip("set CLICKHOUSE_PAGINATION_INTEGRATION=1 to validate 100K+ cursor pagination")
	}
	client, err := clickhouse.New(config.Load().ClickHouse)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	const rows = 100_001
	const address = "0x8888888888888888888888888888888888888888"
	const jobID = "pagination_acceptance_100001"
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Minute)
		defer stop()
		_ = client.Exec(cleanup, "ALTER TABLE onchain.address_activity DELETE WHERE ingest_job_id='"+jobID+"' SETTINGS mutations_sync=2")
	}()
	_ = client.Exec(ctx, "ALTER TABLE onchain.address_activity DELETE WHERE ingest_job_id='"+jobID+"' SETTINGS mutations_sync=2")
	insert := fmt.Sprintf(`INSERT INTO onchain.address_activity
(chain_id,address,counterparty_address,direction,activity_type,block_number,block_time,tx_hash,event_index,token_address,token_symbol,amount,usd_value,method_id,method_name,status,counterparty_entity_type,counterparty_label,source_provider,ingest_job_id,source_range_id)
SELECT 56,'%s','0x9999999999999999999999999999999999999999',if(number%%2=0,'IN','OUT'),'NATIVE_TRANSFER',
900000000+number,toDateTime64('2026-01-01 00:00:00',3,'UTC')+toIntervalSecond(number),
concat('0x',lower(hex(SHA256(toString(number))))),concat('tx:',toString(number)),'','',toDecimal256(number+1,38),NULL,'','','SUCCESS','','','acceptance','%s','pagination'
FROM numbers(%d)`, address, jobID, rows)
	if err := client.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(client)
	seen := make(map[string]struct{}, rows)
	cursor := ""
	pages := 0
	for {
		page, err := repository.ListActivity(ctx, ActivityQuery{ChainID: 56, Address: address, Activity: ActivityAll, PageSize: 200, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		for _, item := range page.Items {
			key := item.TransactionHash + "/" + item.EventIndex
			if _, exists := seen[key]; exists {
				t.Fatalf("duplicate at page %d: %s", pages, key)
			}
			seen[key] = struct{}{}
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			t.Fatalf("cursor did not advance at page %d", pages)
		}
		cursor = page.NextCursor
	}
	if len(seen) != rows {
		t.Fatalf("pagination returned %d unique rows, want %d", len(seen), rows)
	}
	t.Logf("validated rows=%d pages=%d duplicates=0 omissions=0", len(seen), pages)
}
