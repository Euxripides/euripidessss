package parquetdownload

import (
	"math"
	"time"
)

func updateCoverage(job *Job) {
	if job == nil {
		return
	}
	type selectedStage struct {
		source string
		stage  string
	}
	items := []selectedStage{
		{source: "transactions", stage: "transactions"},
		{source: "logs", stage: "logs"},
		{source: "traces", stage: "traces"},
	}
	statuses := make(map[string]string, len(items))
	var progress float64
	for _, item := range items {
		if !hasSelectedSource(job.SelectedSources, item.source) {
			statuses[item.source] = CoverageNotSelected
			continue
		}
		stage := findStage(job, item.stage)
		statuses[item.source] = coverageStatus(job, stage)
		if stage != nil {
			progress += clampPercent(stage.Progress)
		}
	}
	coveragePercent := progress / float64(len(items))
	job.Coverage = DatasetCoverage{
		JobID:              job.ID,
		ChainID:            job.ChainID,
		TransactionsStatus: statuses["transactions"],
		LogsStatus:         statuses["logs"],
		TraceStatus:        statuses["traces"],
		CoveragePercent:    math.Round(coveragePercent*100) / 100,
		UpdatedAt:          time.Now(),
	}
}

func coverageStatus(job *Job, stage *Stage) string {
	if stage == nil {
		return CoveragePartial
	}
	switch stage.Status {
	case StatusDone:
		return CoverageComplete
	case StatusSkipped:
		return CoveragePartial
	case StatusFailed:
		return CoverageFailed
	case StatusRunning, StatusQueued, StatusPausing, StatusPaused, StatusCanceling:
		if job.Status == StatusRunning || job.Status == StatusQueued || job.Status == StatusPausing ||
			job.Status == StatusPaused || job.Status == StatusCanceling {
			return CoverageDownloading
		}
		return CoveragePartial
	case StatusCanceled:
		return CoveragePartial
	default:
		return CoveragePartial
	}
}

func findStage(job *Job, key string) *Stage {
	for index := range job.Stages {
		if job.Stages[index].Key == key {
			return &job.Stages[index]
		}
	}
	return nil
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func settleStages(job *Job, terminalStatus string) {
	for index := range job.Stages {
		stage := &job.Stages[index]
		switch terminalStatus {
		case StatusDone:
			if stage.Status == StatusRunning {
				stage.Status = StatusDone
				stage.Progress = 100
			}
			if stage.Status == StatusQueued || stage.Status == StatusCanceling {
				stage.Status = StatusSkipped
				stage.Progress = 100
				if stage.Detail == "" {
					stage.Detail = "未选择或无需执行"
				}
			}
		case StatusCanceled:
			if stage.Status == StatusRunning || stage.Status == StatusQueued || stage.Status == StatusCanceling {
				stage.Status = StatusCanceled
				if stage.Detail == "" {
					stage.Detail = "任务已取消"
				}
			}
		case StatusFailed:
			if stage.Status == StatusRunning || stage.Status == StatusCanceling {
				stage.Status = StatusFailed
				if stage.Detail == "" {
					stage.Detail = "任务执行失败"
				}
			} else if stage.Status == StatusQueued {
				stage.Status = StatusSkipped
				if stage.Detail == "" {
					stage.Detail = "前序阶段失败，未执行"
				}
			}
		}
	}
}

func addTaskEvent(job *Job, eventType, message, stage string, details map[string]any) {
	if job == nil {
		return
	}
	job.Events = append(job.Events, TaskEvent{
		Type:      eventType,
		Message:   message,
		Stage:     stage,
		Details:   details,
		CreatedAt: time.Now(),
	})
	if len(job.Events) > 500 {
		job.Events = append([]TaskEvent(nil), job.Events[len(job.Events)-500:]...)
	}
}
