package cryptodownload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func guiTaskOutputDir(outputDir, prefix, jobID string) string {
	name := limitFilePart(sanitizeFilePart(strings.TrimSpace(prefix)), 24)
	if strings.TrimSpace(prefix) == "" {
		name = "wallet_export"
	}
	id := sanitizeFilePart(strings.TrimSpace(jobID))
	if strings.TrimSpace(jobID) == "" {
		id = "task"
	}
	taskName := name + "_" + id
	if len(filepath.Join(outputDir, taskName)) > 150 {
		taskName = "task_" + id
	}
	return filepath.Join(outputDir, taskName)
}

func prepareGUIOutputDirectories(outputDir, prefix, jobID string, entries []GUIAddressChain) (string, string, error) {
	taskDir := guiTaskOutputDir(outputDir, prefix, jobID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return "", "", err
	}
	for i, entry := range entries {
		if err := os.MkdirAll(guiAddressOutputDir(taskDir, entry.Address, i), 0755); err != nil {
			return "", "", err
		}
	}
	return taskDir, filepath.Join(taskDir, "下载情况.xlsx"), nil
}

func guiAddressOutputDir(outputDir, address string, idx int) string {
	name := shortAddressForFile(strings.ToLower(strings.TrimSpace(address)))
	if name == "" {
		name = fmt.Sprintf("address_%03d", idx+1)
	} else {
		name = fmt.Sprintf("%03d_%s", idx+1, name)
	}
	return filepath.Join(outputDir, name)
}

func limitFilePart(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}
