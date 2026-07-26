package cryptodownload

import (
	"fmt"
	"path/filepath"
)

func validateCSVCheckpoint(state CSVCheckpointState) error {
	if state.Kinds == nil {
		return fmt.Errorf("kinds are missing")
	}
	for kind, checkpoint := range state.Kinds {
		if kind != CSVCheckpointTransactions && kind != CSVCheckpointTokenTransfers {
			return fmt.Errorf("unknown checkpoint kind %q", kind)
		}
		for index, segment := range checkpoint.Segments {
			if segment.StartTime > segment.EndTime || segment.Rows < 0 {
				return fmt.Errorf("%s segment %d has invalid range or row count", kind, index)
			}
			if segment.File == "" || filepath.Base(segment.File) != segment.File {
				return fmt.Errorf("%s segment %d has invalid file", kind, index)
			}
			if len(segment.SHA256) != 64 {
				return fmt.Errorf("%s segment %d has invalid SHA-256", kind, index)
			}
		}
	}
	return nil
}
