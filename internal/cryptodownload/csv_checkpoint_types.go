package cryptodownload

import "strings"

const csvCheckpointVersion = 1

type CSVCheckpointKind string

const (
	CSVCheckpointTransactions   CSVCheckpointKind = "transactions"
	CSVCheckpointTokenTransfers CSVCheckpointKind = "token_transfers"
)

type CSVSegmentManifest struct {
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	File      string `json:"file"`
	Rows      int64  `json:"rows"`
	SHA256    string `json:"sha256"`
}

type CSVKindCheckpoint struct {
	NextStart int64                `json:"next_start"`
	EndTime   int64                `json:"end_time"`
	Complete  bool                 `json:"complete"`
	Segments  []CSVSegmentManifest `json:"segments"`
}

type CSVCheckpointState struct {
	Version           int                                     `json:"version"`
	ConfigFingerprint string                                  `json:"config_fingerprint"`
	Address           string                                  `json:"address"`
	Chain             string                                  `json:"chain"`
	Kinds             map[CSVCheckpointKind]CSVKindCheckpoint `json:"kinds"`
}

func NewCSVCheckpointState(address, chain, fingerprint string) CSVCheckpointState {
	return CSVCheckpointState{
		Version:           csvCheckpointVersion,
		ConfigFingerprint: fingerprint,
		Address:           strings.ToLower(strings.TrimSpace(address)),
		Chain:             strings.ToLower(strings.TrimSpace(chain)),
		Kinds:             make(map[CSVCheckpointKind]CSVKindCheckpoint),
	}
}
