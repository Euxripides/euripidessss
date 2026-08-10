package canonicalregistry

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (r *Repository) UpsertSemanticJob(ctx context.Context, job SemanticJob) error {
	job.JobID = strings.ToLower(strings.TrimSpace(job.JobID))
	if !uuidRE.MatchString(job.JobID) {
		return fmt.Errorf("%w: invalid job_id", ErrInvalidInput)
	}
	var err error
	if job.JobType, err = requiredText("job_type", strings.ToUpper(job.JobType), 64); err != nil {
		return err
	}
	if job.Dataset, err = requiredText("dataset", job.Dataset, 128); err != nil {
		return err
	}
	if job.ChainID == 0 {
		return fmt.Errorf("%w: chain_id must be positive", ErrInvalidInput)
	}
	if job.ToBlock < job.FromBlock {
		return fmt.Errorf("%w: invalid block range", ErrInvalidInput)
	}
	if job.TargetVersion, err = requiredText("target_version", job.TargetVersion, 64); err != nil || !versionRE.MatchString(job.TargetVersion) {
		return fmt.Errorf("%w: invalid target_version", ErrInvalidInput)
	}
	job.Status = strings.ToUpper(strings.TrimSpace(job.Status))
	if !jobStatuses[job.Status] {
		return fmt.Errorf("%w: invalid status", ErrInvalidInput)
	}
	if len(job.ErrorMessage) > 4096 || strings.ContainsRune(job.ErrorMessage, 0) {
		return fmt.Errorf("%w: invalid error_message", ErrInvalidInput)
	}
	if job.CreatedAt, err = requireTime("created_at", job.CreatedAt); err != nil {
		return err
	}
	if job.StartedAt != nil {
		parsed, parseErr := requireTime("started_at", *job.StartedAt)
		if parseErr != nil {
			return parseErr
		}
		job.StartedAt = &parsed
	}
	if job.CompletedAt != nil {
		parsed, parseErr := requireTime("completed_at", *job.CompletedAt)
		if parseErr != nil {
			return parseErr
		}
		job.CompletedAt = &parsed
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = time.Now().UTC()
	} else if job.UpdatedAt, err = requireTime("updated_at", job.UpdatedAt); err != nil {
		return err
	}
	return r.insert(ctx, "onchain.semantic_jobs", []string{"job_id", "job_type", "chain_id", "dataset", "from_block", "to_block", "target_version", "status", "processed_rows", "failed_rows", "error_message", "created_at", "started_at", "completed_at", "updated_at"}, []string{job.JobID, job.JobType, strconv.FormatUint(uint64(job.ChainID), 10), job.Dataset, strconv.FormatUint(job.FromBlock, 10), strconv.FormatUint(job.ToBlock, 10), job.TargetVersion, job.Status, strconv.FormatUint(job.ProcessedRows, 10), strconv.FormatUint(job.FailedRows, 10), job.ErrorMessage, formatTime(job.CreatedAt), nullableTimeCSV(job.StartedAt), nullableTimeCSV(job.CompletedAt), formatTime(job.UpdatedAt)})
}

func (r *Repository) GetSemanticJob(ctx context.Context, jobID string) (SemanticJob, error) {
	jobID = strings.ToLower(strings.TrimSpace(jobID))
	if !uuidRE.MatchString(jobID) {
		return SemanticJob{}, fmt.Errorf("%w: invalid job_id", ErrInvalidInput)
	}
	rows, err := r.query(ctx, fmt.Sprintf(`SELECT job_id,job_type,chain_id,dataset,from_block,to_block,target_version,status,processed_rows,failed_rows,error_message,created_at,started_at,completed_at,updated_at FROM onchain.semantic_jobs FINAL WHERE job_id = '%s' LIMIT 1`, jobID))
	if err != nil {
		return SemanticJob{}, err
	}
	if len(rows) == 0 {
		return SemanticJob{}, ErrNotFound
	}
	job, err := decodeJob(rows[0])
	if err != nil || strings.ToLower(job.JobID) != jobID {
		return SemanticJob{}, fmt.Errorf("%w: malformed job row", ErrQueryFailed)
	}
	return job, nil
}

func decodeJob(row map[string]any) (SemanticJob, error) {
	var out SemanticJob
	var err error
	out.JobID, err = stringValue(row["job_id"])
	if err != nil {
		return out, err
	}
	out.JobType, _ = stringValue(row["job_type"])
	chain, err := uintValue(row["chain_id"], 32)
	if err != nil {
		return out, err
	}
	out.ChainID = uint32(chain)
	out.Dataset, _ = stringValue(row["dataset"])
	out.FromBlock, err = uintValue(row["from_block"], 64)
	if err != nil {
		return out, err
	}
	out.ToBlock, err = uintValue(row["to_block"], 64)
	if err != nil {
		return out, err
	}
	out.TargetVersion, _ = stringValue(row["target_version"])
	out.Status, _ = stringValue(row["status"])
	out.ProcessedRows, err = uintValue(row["processed_rows"], 64)
	if err != nil {
		return out, err
	}
	out.FailedRows, err = uintValue(row["failed_rows"], 64)
	if err != nil {
		return out, err
	}
	out.ErrorMessage, _ = stringValue(row["error_message"])
	out.CreatedAt, err = timeValue(row["created_at"])
	if err != nil {
		return out, err
	}
	if row["started_at"] != nil {
		value, timeErr := timeValue(row["started_at"])
		if timeErr != nil {
			return out, timeErr
		}
		out.StartedAt = &value
	}
	if row["completed_at"] != nil {
		value, timeErr := timeValue(row["completed_at"])
		if timeErr != nil {
			return out, timeErr
		}
		out.CompletedAt = &value
	}
	out.UpdatedAt, err = timeValue(row["updated_at"])
	return out, err
}
