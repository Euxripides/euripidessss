package cryptodownload

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (c *CSVExportClient) writeCSVRaw(cfg Config, chain, kindName string, segment int, rangeStart, rangeEnd, nextCursor int64, body []byte) error {
	var kind *csvExportKind
	for index := range csvExportKinds {
		if csvExportKinds[index].Name == kindName {
			kind = &csvExportKinds[index]
			break
		}
	}
	if kind == nil {
		return &CSVRawDurableError{Stage: ErrCSVRawValidate, Err: fmt.Errorf("unknown CSV kind %q", kindName)}
	}
	records, _, err := parseCSVRecordsForKind(*kind, body, cfg.Address)
	if err != nil || len(records) == 0 || !csvValidateAddress(records, cfg.Address) {
		return &CSVRawDurableError{Stage: ErrCSVRawValidate, Err: errors.Join(err, fmt.Errorf("payload does not contain rows for address %q", cfg.Address))}
	}
	payload, err := newCSVRawPayload(body, len(records))
	if err != nil {
		return err
	}
	if c.rawDir == "" {
		return nil
	}
	fingerprint := csvRawFingerprint(cfg, chain)
	checkpointKind := CSVCheckpointKind(kindName)
	lock, err := AcquireCSVWriterLock(c.rawDir, chain, cfg.Address, fingerprint, checkpointKind)
	if err != nil {
		return err
	}
	defer lock.Close()
	store, err := NewCSVCheckpointStore(c.rawDir, chain, cfg.Address, fingerprint)
	if err != nil {
		return err
	}
	state, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		state = NewCSVCheckpointState(cfg.Address, chain, fingerprint)
	} else if err != nil {
		return err
	}
	dir := filepath.Join(c.rawDir, sanitizeFilePart("csv_"+chain), sanitizeFilePart(strings.ToLower(cfg.Address)))
	target := filepath.Join(dir, fmt.Sprintf("%s_segment_%04d.csv", sanitizeFilePart(kindName), segment))
	manifest := CSVSegmentManifest{StartTime: rangeStart, EndTime: rangeEnd, File: filepath.Base(target), Rows: payload.Rows, SHA256: hex.EncodeToString(payload.Sum[:])}
	checkpoint := state.Kinds[checkpointKind]
	checkpoint.NextStart = nextCursor
	checkpoint.EndTime = rangeEnd
	checkpoint.Segments = append(checkpoint.Segments, manifest)
	state.Kinds[checkpointKind] = checkpoint
	writer := NewCSVRawDurableWriter(store)
	if c.durableRename != nil {
		writer.rename = c.durableRename
	}
	return writer.Commit(target, payload, manifest, state)
}

func csvRawFingerprint(cfg Config, chain string) string {
	return CSVCheckpointConfigFingerprint(CSVCheckpointFingerprintInput{
		Source:           cfg.Source,
		Address:          cfg.Address,
		Chain:            chain,
		StartTime:        cfg.CSVStartTime,
		EndTime:          cfg.CSVEndTime,
		SegmentSeconds:   csvTokenWindowSeconds,
		Kinds:            []CSVCheckpointKind{CSVCheckpointTransactions, CSVCheckpointTokenTransfers},
		EnabledProtocols: cfg.Protocols,
		OKLinkBaseURL:    cfg.BaseURL,
		ProfileIdentity:  csvRawProfileIdentity(cfg),
		SignerIdentity:   csvRawSignerIdentity(cfg),
		RawLayoutVersion: 1,
	})
}

func csvRawProfileIdentity(cfg Config) string {
	return strings.ToLower(strings.TrimSpace(cfg.CSVIMAPHost)) + "|" + strings.ToLower(strings.TrimSpace(cfg.CSVIMAPUser))
}

func csvRawSignerIdentity(cfg Config) string {
	har := strings.ToLower(filepath.Base(strings.TrimSpace(cfg.CSVRequestHAR)))
	if har == "." || har == "" {
		har = "dynamic-session-signer"
	}
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(cfg.BaseURL)), "/") + "|" + har
}

func csvNextCheckpointCursor(kind csvExportKind, records []map[string]string, rangeStart, _ int64, overallStart int64) (int64, bool) {
	lastTime, ok := lastCSVTransactionUnix(records)
	if kind.Name == string(CSVCheckpointTransactions) {
		return lastTime - 1, ok
	}
	if len(records) >= csvMaxRowsPerExport {
		return lastTime - 1, ok
	}
	if rangeStart > overallStart {
		return rangeStart - 1, true
	}
	return 0, false
}

func (c *CSVExportClient) commitCSVEmailSegment(cfg Config, chain string, kind csvExportKind, segment int, rangeStart, rangeEnd, nextCursor int64, records []map[string]string, body []byte, seenRows map[string]bool, hasPrior bool) ([]map[string]any, []map[string]string, bool, error) {
	mapped, rawNew := mapNewCSVRecords(cfg.Address, strings.ToUpper(chain), kind, records, seenRows)
	if len(mapped) == 0 && len(records) > 0 && hasPrior {
		return nil, nil, true, nil
	}
	if err := c.writeCSVRaw(cfg, chain, kind.Name, segment, rangeStart, rangeEnd, nextCursor, body); err != nil {
		return nil, nil, false, err
	}
	return mapped, rawNew, false, nil
}

var (
	ErrCSVRawValidate   = errors.New("validate CSV raw payload")
	ErrCSVRawWrite      = errors.New("write CSV raw payload")
	ErrCSVRawSync       = errors.New("sync CSV raw payload")
	ErrCSVRawClose      = errors.New("close CSV raw payload")
	ErrCSVRawRename     = errors.New("rename CSV raw payload")
	ErrCSVRawCheckpoint = errors.New("save CSV raw checkpoint")
)

type CSVRawDurableError struct {
	Stage error
	Path  string
	Err   error
}

func (e *CSVRawDurableError) Error() string {
	return fmt.Sprintf("%v %q: %v", e.Stage, e.Path, e.Err)
}

func (e *CSVRawDurableError) Unwrap() []error { return []error{e.Stage, e.Err} }

type CSVRawPayload struct {
	Body []byte
	Rows int64
	Sum  [sha256.Size]byte
}

func newCSVRawPayload(body []byte, rows int) (CSVRawPayload, error) {
	if len(body) == 0 || rows < 0 || isNoSuchKeyPayload(body) {
		return CSVRawPayload{}, &CSVRawDurableError{Stage: ErrCSVRawValidate, Err: fmt.Errorf("empty, invalid, or missing payload")}
	}
	copyBody := append([]byte(nil), body...)
	return CSVRawPayload{Body: copyBody, Rows: int64(rows), Sum: sha256.Sum256(copyBody)}, nil
}

type CSVRawDurableWriter struct {
	store  *CSVCheckpointStore
	rename func(string, string) error
}

func NewCSVRawDurableWriter(store *CSVCheckpointStore) *CSVRawDurableWriter {
	return &CSVRawDurableWriter{store: store, rename: os.Rename}
}

func (w *CSVRawDurableWriter) Commit(target string, payload CSVRawPayload, manifest CSVSegmentManifest, state CSVCheckpointState) error {
	if w == nil || w.store == nil || len(payload.Body) == 0 {
		return &CSVRawDurableError{Stage: ErrCSVRawValidate, Path: target, Err: fmt.Errorf("writer, store, or payload is missing")}
	}
	if manifest.File != filepath.Base(target) || manifest.Rows != payload.Rows || manifest.SHA256 != hex.EncodeToString(payload.Sum[:]) {
		return &CSVRawDurableError{Stage: ErrCSVRawValidate, Path: target, Err: fmt.Errorf("manifest does not match payload or target")}
	}
	if err := durableWriteCSV(target, payload.Body, w.rename); err != nil {
		return err
	}
	if err := w.store.Save(state); err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawCheckpoint, Path: w.store.Path(), Err: err}
	}
	return nil
}

func durableWriteCSV(target string, body []byte, rename func(string, string) error) (err error) {
	if len(body) == 0 {
		return &CSVRawDurableError{Stage: ErrCSVRawValidate, Path: target, Err: fmt.Errorf("payload is empty")}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawWrite, Path: target, Err: err}
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".csv-segment-*.tmp")
	if err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawWrite, Path: target, Err: err}
	}
	tempPath := temp.Name()
	tempOpen := true
	defer func() {
		if tempOpen {
			err = errors.Join(err, temp.Close())
		}
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if _, err := temp.Write(body); err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawWrite, Path: target, Err: err}
	}
	if err := temp.Sync(); err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawSync, Path: target, Err: err}
	}
	if err := temp.Close(); err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawClose, Path: target, Err: err}
	}
	tempOpen = false
	if err := rename(tempPath, target); err != nil {
		return &CSVRawDurableError{Stage: ErrCSVRawRename, Path: target, Err: err}
	}
	return nil
}
