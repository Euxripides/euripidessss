package pricing

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type BackfillRequest struct {
	JobID          string
	ChainID        uint64
	Token          string
	From           time.Time
	To             time.Time
	Resolution     Resolution
	SourcePriority []string
}

type BackfillJob struct {
	BackfillRequest
	TokenID     string
	Status      string
	FetchedRows uint64
	WrittenRows uint64
	Error       string
	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	UpdatedAt   time.Time
}

type BackfillSource interface {
	Name() string
	Fetch(ctx context.Context, request BackfillRequest) ([]HistoricalPrice, error)
}

type BackfillJobStore interface {
	SaveJob(ctx context.Context, job BackfillJob) error
	SaveGaps(ctx context.Context, jobID string, gaps []PriceGap) error
}

type BackfillService struct {
	repository PriceRepository
	jobs       BackfillJobStore
	sources    map[string]BackfillSource
	detector   *GapDetector
	now        func() time.Time
}

func NewBackfillService(repository PriceRepository, jobs BackfillJobStore, sources []BackfillSource) *BackfillService {
	indexed := make(map[string]BackfillSource, len(sources))
	for _, source := range sources {
		if source != nil {
			indexed[strings.ToUpper(source.Name())] = source
		}
	}
	return &BackfillService{repository: repository, jobs: jobs, sources: indexed, detector: NewGapDetector(repository), now: time.Now}
}

// Run is a worker-side operation. Resolver/Explorer never invokes a BackfillSource.
func (s *BackfillService) Run(ctx context.Context, request BackfillRequest) (job BackfillJob, err error) {
	if s == nil || s.repository == nil || s.jobs == nil || strings.TrimSpace(request.JobID) == "" || len(request.SourcePriority) == 0 {
		return BackfillJob{}, fmt.Errorf("%w: incomplete backfill request", ErrInvalidInput)
	}
	tokenID, err := CanonicalTokenID(request.ChainID, request.Token)
	if err != nil {
		return BackfillJob{}, err
	}
	if request.From.IsZero() || request.To.Before(request.From) {
		return BackfillJob{}, fmt.Errorf("%w: invalid backfill range", ErrInvalidInput)
	}
	if _, err = request.Resolution.Duration(); err != nil {
		return BackfillJob{}, err
	}
	now := s.now().UTC()
	job = BackfillJob{BackfillRequest: request, TokenID: tokenID, Status: "RUNNING", CreatedAt: now, StartedAt: &now, UpdatedAt: now}
	if err = s.jobs.SaveJob(ctx, job); err != nil {
		return job, err
	}
	defer func() {
		finished := s.now().UTC()
		job.FinishedAt = &finished
		job.UpdatedAt = finished
		if err != nil {
			job.Status = "FAILED"
			job.Error = err.Error()
		} else {
			job.Status = "SUCCEEDED"
		}
		if saveErr := s.jobs.SaveJob(context.WithoutCancel(ctx), job); err == nil && saveErr != nil {
			err = saveErr
		}
	}()
	for _, sourceName := range request.SourcePriority {
		source := s.sources[strings.ToUpper(strings.TrimSpace(sourceName))]
		if source == nil {
			continue
		}
		prices, fetchErr := source.Fetch(ctx, request)
		if fetchErr != nil {
			continue
		}
		job.FetchedRows += uint64(len(prices))
		if len(prices) == 0 {
			continue
		}
		for i := range prices {
			prices[i].ChainID = uint32(request.ChainID)
			prices[i].TokenID = tokenID
			if prices[i].Source == "" {
				prices[i].Source = source.Name()
			}
			if prices[i].Resolution == "" {
				prices[i].Resolution = request.Resolution
			}
		}
		if err = s.repository.PutPrices(ctx, prices); err != nil {
			return job, err
		}
		job.WrittenRows += uint64(len(prices))
		break
	}
	gaps, detectErr := s.detector.Detect(ctx, GapRequest{ChainID: request.ChainID, Token: tokenID, From: request.From, To: request.To, Resolution: request.Resolution})
	if detectErr != nil {
		return job, detectErr
	}
	if err = s.jobs.SaveGaps(ctx, request.JobID, gaps); err != nil {
		return job, err
	}
	return job, nil
}
