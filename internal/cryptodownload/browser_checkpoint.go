package cryptodownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BrowserCheckpoint records the last-completed offset for a specific data kind
// on a chain, allowing resumed page fetches to skip already-consumed pages.
type BrowserCheckpoint struct {
	Chain      string `json:"chain"`
	Kind       string `json:"kind"`
	LastOffset int    `json:"last_offset"`
	Total      int64  `json:"total"`
}

// LoadBrowserCheckpoint reads a checkpoint from rawDir/chain/kind_checkpoint.json.
// Returns nil when no checkpoint file exists.
func LoadBrowserCheckpoint(rawDir, chain, kind string) (*BrowserCheckpoint, error) {
	if rawDir == "" {
		return nil, nil
	}
	path := browserCheckpointPath(rawDir, chain, kind)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read browser checkpoint: %w", err)
	}
	var cp BrowserCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal browser checkpoint: %w", err)
	}
	return &cp, nil
}

// SaveBrowserCheckpoint atomically persists a browser checkpoint.
func SaveBrowserCheckpoint(rawDir string, cp BrowserCheckpoint) error {
	if rawDir == "" {
		return nil
	}
	path := browserCheckpointPath(rawDir, cp.Chain, cp.Kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func browserCheckpointPath(rawDir, chain, kind string) string {
	elem := []string{rawDir, "checkpoints", sanitise(chain)}
	filename := sanitise(kind) + "_checkpoint.json"
	elem = append(elem, filename)
	return filepath.Join(elem...)
}

func sanitise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
