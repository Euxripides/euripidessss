package cryptodownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func NewGUIManager(configDir string) (*GUIManager, error) {
	store, err := NewGUIJobStore(configDir)
	if err != nil {
		return nil, err
	}
	history, err := NewGUIDownloadHistoryStore(configDir)
	if err != nil {
		return nil, err
	}
	settings, err := loadGUISettingsFromConfigDir(configDir)
	if err != nil {
		return nil, err
	}
	records, err := store.LoadAll()
	if err != nil {
		return nil, err
	}
	manager := &GUIManager{jobs: make(map[string]*GUIJob, len(records)), store: store, history: history, configDir: configDir, scheduler: NewGUIBatchScheduler(1)}
	for _, record := range records {
		job := restoreGUIJob(record, store, history, settings)
		job.scheduler = manager.scheduler
		if until, ok := guiCooldownUntil(job.CooldownUntil); ok {
			manager.scheduler.StartCooldown(time.Until(until))
		}
		manager.jobs[job.ID] = job
		job.persist()
	}
	return manager, nil
}

func loadGUISettingsFromConfigDir(configDir string) (GUIPersistedSettings, error) {
	if configDir == "" {
		return loadGUIPersistedSettings()
	}
	settings := defaultGUIPersistedSettings()
	encoded, err := os.ReadFile(filepath.Join(configDir, "wallet-exporter", "gui-settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read GUI settings: %w", err)
	}
	if err := json.Unmarshal(encoded, &settings); err != nil {
		return defaultGUIPersistedSettings(), fmt.Errorf("decode GUI settings: %w", err)
	}
	return normalizeGUIPersistedSettings(settings), nil
}
