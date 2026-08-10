package pricing

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type ClickHouseJobStore struct{ client ClickHouseClient }

func NewClickHouseJobStore(client ClickHouseClient) *ClickHouseJobStore {
	return &ClickHouseJobStore{client: client}
}

func (s *ClickHouseJobStore) SaveJob(ctx context.Context, job BackfillJob) error {
	if s == nil || s.client == nil || !uuidRE.MatchString(job.JobID) {
		return fmt.Errorf("%w: invalid price job", ErrInvalidInput)
	}
	if len(job.SourcePriority) > 64 {
		return fmt.Errorf("%w: too many price sources", ErrInvalidInput)
	}
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	row := []string{job.JobID, "PRICE_GAP_REPAIR", strconv.FormatUint(job.ChainID, 10), job.TokenID,
		formatTime(job.From), formatTime(job.To), string(job.Resolution), clickHouseArray(job.SourcePriority), job.Status,
		strconv.FormatUint(job.FetchedRows, 10), strconv.FormatUint(job.WrittenRows, 10), bounded(job.Error, 4096),
		formatTime(job.CreatedAt), nullableTime(job.StartedAt), nullableTime(job.FinishedAt), formatTime(job.UpdatedAt)}
	if err := w.Write(row); err != nil {
		return err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	columns := []string{"job_id", "job_type", "chain_id", "token_address", "range_start", "range_end", "resolution", "source_priority", "status", "fetched_rows", "written_rows", "error_message", "created_at", "started_at", "finished_at", "updated_at"}
	if err := s.client.InsertCSV(ctx, "onchain.price_backfill_jobs", columns, &buffer); err != nil {
		return fmt.Errorf("save price backfill job: %w", err)
	}
	return nil
}

func (s *ClickHouseJobStore) SaveGaps(ctx context.Context, jobID string, gaps []PriceGap) error {
	if s == nil || s.client == nil || !uuidRE.MatchString(jobID) {
		return fmt.Errorf("%w: invalid repair job id", ErrInvalidInput)
	}
	if len(gaps) == 0 {
		return nil
	}
	var buffer bytes.Buffer
	w := csv.NewWriter(&buffer)
	now := time.Now().UTC()
	for index, gap := range gaps {
		gapID := deterministicGapUUID(jobID, index)
		row := []string{gapID, strconv.FormatUint(uint64(gap.ChainID), 10), gap.TokenID, string(gap.Resolution), formatTime(gap.Start), formatTime(gap.End), strconv.FormatUint(gap.MissingBuckets, 10), "OPEN", jobID, formatTime(now), formatTime(now)}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	columns := []string{"gap_id", "chain_id", "token_address", "resolution", "gap_start", "gap_end", "missing_buckets", "status", "repair_job_id", "detected_at", "updated_at"}
	if err := s.client.InsertCSV(ctx, "onchain.price_gaps", columns, &buffer); err != nil {
		return fmt.Errorf("save price gaps: %w", err)
	}
	return nil
}

func clickHouseArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ReplaceAll(strings.TrimSpace(value), "'", "")
		quoted = append(quoted, "'"+clean+"'")
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func nullableTime(value *time.Time) string {
	if value == nil {
		return `\N`
	}
	return formatTime(*value)
}
func bounded(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

// deterministicGapUUID creates an RFC 4122 v5-shaped identifier without a new dependency.
func deterministicGapUUID(jobID string, index int) string {
	base := strings.ReplaceAll(strings.ToLower(jobID), "-", "")
	suffix := fmt.Sprintf("%012x", index+1)
	return base[:8] + "-" + base[8:12] + "-5" + base[13:16] + "-a" + base[17:20] + "-" + suffix
}
