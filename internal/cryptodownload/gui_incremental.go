package cryptodownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func guiIncrementalConfig(cfg Config, entry GUIAddressChain, enabled bool) (Config, string) {
	if !enabled || !strings.EqualFold(cfg.Source, "csv") || strings.TrimSpace(cfg.RawDir) == "" {
		return cfg, ""
	}
	cursor, found := guiIncrementalCursor(cfg.RawDir, entry)
	if !found {
		return cfg, "增量模式：未找到检查点，执行首次全量下载"
	}
	cfg.CSVStartTime = cursor + 1
	cfg.CSVEndTime = time.Now().Unix()
	cfg.RawDir = filepath.Join(cfg.RawDir, "incremental", strconv.FormatInt(cursor, 10))
	return cfg, fmt.Sprintf("增量范围：%s 至当前", time.Unix(cfg.CSVStartTime, 0).Format("2006-01-02 15:04"))
}

func guiIncrementalCursor(rawDir string, entry GUIAddressChain) (int64, bool) {
	path := filepath.Join(rawDir, "csv_"+strings.ToLower(strings.TrimSpace(entry.Chain)), strings.ToLower(strings.TrimSpace(entry.Address)), "export_state.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var state CSVCheckpointState
	if json.Unmarshal(encoded, &state) != nil {
		return 0, false
	}
	var cursor int64
	for _, checkpoint := range state.Kinds {
		for _, segment := range checkpoint.Segments {
			if segment.EndTime > cursor {
				cursor = segment.EndTime
			}
		}
	}
	return cursor, cursor > 0
}
