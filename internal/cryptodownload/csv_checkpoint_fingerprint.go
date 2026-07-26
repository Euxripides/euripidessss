package cryptodownload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type CSVCheckpointFingerprintInput struct {
	Source           string
	Address          string
	Chain            string
	StartTime        int64
	EndTime          int64
	SegmentSeconds   int64
	Kinds            []CSVCheckpointKind
	EnabledProtocols []string
	OKLinkBaseURL    string
	ProfileIdentity  string
	SignerIdentity   string
	RawLayoutVersion int
}

func CSVCheckpointConfigFingerprint(input CSVCheckpointFingerprintInput) string {
	kinds := append([]CSVCheckpointKind(nil), input.Kinds...)
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	protocols := append([]string(nil), input.EnabledProtocols...)
	for index := range protocols {
		protocols[index] = strings.ToLower(strings.TrimSpace(protocols[index]))
	}
	sort.Strings(protocols)
	payload := struct {
		Source           string              `json:"source"`
		Address          string              `json:"address"`
		Chain            string              `json:"chain"`
		StartTime        int64               `json:"start_time"`
		EndTime          int64               `json:"end_time"`
		SegmentSeconds   int64               `json:"segment_seconds"`
		Kinds            []CSVCheckpointKind `json:"kinds"`
		EnabledProtocols []string            `json:"enabled_protocols"`
		OKLinkBaseURL    string              `json:"oklink_base_url"`
		ProfileIdentity  string              `json:"profile_identity"`
		SignerIdentity   string              `json:"signer_identity"`
		RawLayoutVersion int                 `json:"raw_layout_version"`
	}{
		Source:           strings.ToLower(strings.TrimSpace(input.Source)),
		Address:          strings.ToLower(strings.TrimSpace(input.Address)),
		Chain:            strings.ToLower(strings.TrimSpace(input.Chain)),
		StartTime:        input.StartTime,
		EndTime:          input.EndTime,
		SegmentSeconds:   input.SegmentSeconds,
		Kinds:            kinds,
		EnabledProtocols: protocols,
		OKLinkBaseURL:    strings.TrimRight(strings.TrimSpace(input.OKLinkBaseURL), "/"),
		ProfileIdentity:  strings.TrimSpace(input.ProfileIdentity),
		SignerIdentity:   strings.TrimSpace(input.SignerIdentity),
		RawLayoutVersion: input.RawLayoutVersion,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
