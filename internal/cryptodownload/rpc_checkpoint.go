package cryptodownload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RPCCheckpoint records the last-scanned block for a chain/address pair,
// allowing interrupted RPC scans to resume from the checkpoint instead of
// re-scanning from the beginning.
type RPCCheckpoint struct {
	Chain         string `json:"chain"`
	Address       string `json:"address"`
	LastBlock     uint64 `json:"last_block"`
	LastBlockTime int64  `json:"last_block_time"`
	UpdatedAt     string `json:"updated_at"`
}

// LoadRPCCheckpoint reads a checkpoint from rawDir/checkpoints/.
func LoadRPCCheckpoint(rawDir, chain, address string) (*RPCCheckpoint, error) {
	if rawDir == "" {
		return nil, nil
	}
	path := rpcCheckpointPath(rawDir, chain, address)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read RPC checkpoint: %w", err)
	}
	var cp RPCCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal RPC checkpoint: %w", err)
	}
	return &cp, nil
}

// SaveRPCCheckpoint atomically persists an RPC checkpoint.
func SaveRPCCheckpoint(rawDir string, cp RPCCheckpoint) error {
	if rawDir == "" {
		return nil
	}
	cp.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	path := rpcCheckpointPath(rawDir, cp.Chain, cp.Address)
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

func rpcCheckpointPath(rawDir, chain, address string) string {
	dir := filepath.Join(rawDir, "checkpoints", sanitise(chain))
	filename := sanitise(address) + "_rpc_checkpoint.json"
	return filepath.Join(dir, filename)
}
