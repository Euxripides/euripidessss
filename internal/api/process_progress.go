package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/etl/backend/internal/etl"
	"github.com/etl/backend/internal/model"
	"github.com/gin-gonic/gin"
)

type processStageProgress struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	Current        int64   `json:"current"`
	Total          int64   `json:"total"`
	Unit           string  `json:"unit"`
	Percent        float64 `json:"percent"`
	Speed          float64 `json:"speed"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	ETASeconds     float64 `json:"eta_seconds"`
	Message        string  `json:"message,omitempty"`
	startedAt      time.Time
}

type processProgressState struct {
	JobID          string                  `json:"job_id"`
	Status         string                  `json:"status"`
	Error          string                  `json:"error,omitempty"`
	StartedAt      time.Time               `json:"started_at"`
	ElapsedSeconds float64                 `json:"elapsed_seconds"`
	Stages         []*processStageProgress `json:"stages"`
	mu             sync.Mutex
}

var processProgressRegistry = struct {
	sync.RWMutex
	items map[string]*processProgressState
}{items: make(map[string]*processProgressState)}

func newProcessProgress(jobID string, unified bool) *processProgressState {
	definitions := [][2]string{
		{"scan", "扫描识别来源"},
		{"preserve", "保留源文件"},
		{"source_merge", "分类原字段合并"},
	}
	if unified {
		definitions = append(definitions,
			[2]string{"normalize", "分类字段统一"},
			[2]string{"final_merge", "跨来源清洗去重合并"},
		)
	}
	definitions = append(definitions, [2]string{"export", "导出最终结果"})
	state := &processProgressState{JobID: jobID, Status: "running", StartedAt: time.Now()}
	for _, definition := range definitions {
		state.Stages = append(state.Stages, &processStageProgress{
			ID: definition[0], Name: definition[1], Status: "pending",
		})
	}
	processProgressRegistry.Lock()
	processProgressRegistry.items[jobID] = state
	processProgressRegistry.Unlock()
	return state
}

func (state *processProgressState) update(event etl.ProgressEvent) {
	state.mu.Lock()
	defer state.mu.Unlock()
	now := time.Now()
	for _, stage := range state.Stages {
		if stage.ID != event.Stage {
			continue
		}
		if stage.startedAt.IsZero() {
			stage.startedAt = now
		}
		stage.Name = event.Name
		stage.Status = event.Status
		stage.Current = event.Current
		stage.Total = event.Total
		stage.Unit = event.Unit
		stage.Message = event.Message
		stage.ElapsedSeconds = now.Sub(stage.startedAt).Seconds()
		if stage.Total > 0 {
			stage.Percent = float64(stage.Current) * 100 / float64(stage.Total)
			if stage.Status != "done" && stage.Percent >= 100 {
				stage.Percent = 99.9
			}
			if stage.Percent > 100 {
				stage.Percent = 100
			}
		}
		if stage.ElapsedSeconds > 0 && stage.Current > 0 {
			stage.Speed = float64(stage.Current) / stage.ElapsedSeconds
			if stage.Total > stage.Current {
				stage.ETASeconds = float64(stage.Total-stage.Current) / stage.Speed
			}
		}
		if stage.Status == "done" {
			stage.Percent = 100
			stage.ETASeconds = 0
		}
		break
	}
}

func (state *processProgressState) finish(err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if err != nil {
		state.Status = "failed"
		state.Error = err.Error()
		for _, stage := range state.Stages {
			if stage.Status == "running" {
				stage.Status = "failed"
			}
		}
		return
	}
	state.Status = "done"
}

func HandleProcessProgress(c *gin.Context) {
	jobID := c.Param("job_id")
	processProgressRegistry.RLock()
	state := processProgressRegistry.items[jobID]
	processProgressRegistry.RUnlock()
	if state == nil {
		c.JSON(404, gin.H{"detail": "任务进度不存在"})
		return
	}
	state.mu.Lock()
	state.ElapsedSeconds = time.Since(state.StartedAt).Seconds()
	payload, _ := json.Marshal(state)
	state.mu.Unlock()
	c.Data(200, "application/json; charset=utf-8", payload)
}

type persistedArtifact struct {
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Rows     int64  `json:"rows,omitempty"`
	Size     int64  `json:"size"`
}

func persistProcessArtifacts(jobID string, artifacts []model.PipelineArtifact) error {
	dir := filepath.Join(cfg.OutputDir, "etl_jobs", jobID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	items := make([]persistedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, persistedArtifact{
			ID: artifact.ID, Stage: artifact.Stage, Provider: artifact.Provider,
			Name: artifact.Name, Path: artifact.Path, Rows: artifact.Rows, Size: artifact.Size,
		})
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "artifacts.json"), data, 0644)
}

func HandleProcessArtifact(c *gin.Context) {
	jobID := c.Param("job_id")
	artifactID := c.Param("artifact_id")
	manifestPath := filepath.Join(cfg.OutputDir, "etl_jobs", jobID, "artifacts.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		c.JSON(404, gin.H{"detail": "任务产物清单不存在"})
		return
	}
	var items []persistedArtifact
	if err := json.Unmarshal(data, &items); err != nil {
		c.JSON(500, gin.H{"detail": "任务产物清单损坏"})
		return
	}
	outputRoot, _ := filepath.Abs(cfg.OutputDir)
	for _, artifact := range items {
		if artifact.ID != artifactID {
			continue
		}
		path, _ := filepath.Abs(artifact.Path)
		relative, relErr := filepath.Rel(outputRoot, path)
		if relErr != nil || relative == ".." || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
			c.JSON(403, gin.H{"detail": "产物路径不在任务目录内"})
			return
		}
		if _, err := os.Stat(path); err != nil {
			c.JSON(404, gin.H{"detail": "任务产物不存在"})
			return
		}
		c.FileAttachment(path, artifact.Name)
		return
	}
	c.JSON(404, gin.H{"detail": fmt.Sprintf("未找到产物 %s", artifactID)})
}
