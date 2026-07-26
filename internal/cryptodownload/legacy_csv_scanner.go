package cryptodownload

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type LegacyCSVScan struct {
	NextSegment     int
	TotalRows       int
	FirstUnix       int64
	LastUnix        int64
	HasExtraColumns bool
	Segments        []LegacyCSVSegment
}

type LegacyCSVSegment struct {
	Number    int
	Path      string
	Rows      int
	FirstUnix int64
	LastUnix  int64
}

func ValidateLegacyCSVMigrationRange(start, end int64) error {
	if (start == 0 || start == defaultCSVStartTime) && end == 0 {
		return nil
	}
	return &LegacyCSVRangeError{Start: start, End: end}
}

func ScanLegacyCSV(dir, address string, kind csvExportKind) (LegacyCSVScan, error) {
	paths, err := legacyCSVSegmentPaths(dir, kind.Name)
	if err != nil {
		return LegacyCSVScan{}, err
	}
	result := LegacyCSVScan{NextSegment: 1, Segments: make([]LegacyCSVSegment, 0, len(paths))}
	var previousLast int64
	for index, numbered := range paths {
		expected := index + 1
		if numbered.number != expected {
			return LegacyCSVScan{}, &LegacyCSVGapError{Kind: kind.Name, MissingSegment: expected, FoundSegment: numbered.number}
		}
		segment, hasExtra, scanErr := scanLegacyCSVSegment(numbered.path, address, kind, numbered.number)
		if scanErr != nil {
			return LegacyCSVScan{}, scanErr
		}
		if previousLast > 0 && segment.FirstUnix > previousLast {
			return LegacyCSVScan{}, &LegacyCSVCorruptionError{Path: numbered.path, Segment: numbered.number, Reason: LegacyCSVReasonNonMonotonic}
		}
		if index == 0 {
			result.FirstUnix = segment.FirstUnix
		}
		previousLast = segment.LastUnix
		result.LastUnix = segment.LastUnix
		result.TotalRows += segment.Rows
		result.HasExtraColumns = result.HasExtraColumns || hasExtra
		result.Segments = append(result.Segments, segment)
		result.NextSegment = numbered.number + 1
	}
	return result, nil
}

type legacyCSVNumberedPath struct {
	number int
	path   string
}

func legacyCSVSegmentPaths(dir, kind string) ([]legacyCSVNumberedPath, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read legacy CSV directory %s: %w", dir, err)
	}
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(kind) + `_segment_(\d{4})\.csv$`)
	paths := make([]legacyCSVNumberedPath, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := pattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		number, parseErr := strconv.Atoi(match[1])
		if parseErr != nil {
			return nil, fmt.Errorf("parse legacy CSV segment number %s: %w", entry.Name(), parseErr)
		}
		paths = append(paths, legacyCSVNumberedPath{number: number, path: filepath.Join(dir, entry.Name())})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].number < paths[j].number })
	return paths, nil
}

func scanLegacyCSVSegment(path, address string, kind csvExportKind, number int) (LegacyCSVSegment, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return LegacyCSVSegment{}, false, &LegacyCSVCorruptionError{Path: path, Segment: number, Reason: LegacyCSVReasonUnparseable, Cause: err}
	}
	records, headers, err := parseCSVRecordsForKind(kind, body, address)
	if err != nil {
		return LegacyCSVSegment{}, false, &LegacyCSVCorruptionError{Path: path, Segment: number, Reason: LegacyCSVReasonUnparseable, Cause: err}
	}
	if len(records) == 0 || len(headers) == 0 {
		return LegacyCSVSegment{}, false, &LegacyCSVCorruptionError{Path: path, Segment: number, Reason: LegacyCSVReasonEmpty}
	}
	if !csvValidateAddress(records, address) {
		return LegacyCSVSegment{}, false, &LegacyCSVCorruptionError{Path: path, Segment: number, Reason: LegacyCSVReasonAddressMismatch}
	}
	first, last, extra, recordErr := legacyCSVRecordBounds(records, headers)
	if recordErr != nil {
		return LegacyCSVSegment{}, false, &LegacyCSVCorruptionError{Path: path, Segment: number, Reason: recordErr.reason}
	}
	return LegacyCSVSegment{Number: number, Path: path, Rows: len(records), FirstUnix: first, LastUnix: last}, extra, nil
}

type legacyCSVRecordError struct{ reason LegacyCSVCorruptionReason }

func legacyCSVRecordBounds(records []map[string]string, headers []string) (int64, int64, bool, *legacyCSVRecordError) {
	var first, last int64
	hasExtra := false
	for index, record := range records {
		if legacyCSVIsRepeatedHeader(record, headers) {
			return 0, 0, false, &legacyCSVRecordError{reason: LegacyCSVReasonRepeatedHeader}
		}
		for key := range record {
			hasExtra = hasExtra || strings.HasPrefix(key, "_extra_")
		}
		timestamp := firstCSVValue(record, "transactionTime", "Transaction Time", "Local Time", "UTC Time", "Time", "本地时间(UTC+8)", "UTC时间", "时间", "日期", "Date")
		unix := csvTimeUnix(timestamp)
		if unix <= 0 {
			return 0, 0, false, &legacyCSVRecordError{reason: LegacyCSVReasonInvalidTime}
		}
		if index == 0 {
			first = unix
		}
		last = unix
	}
	return first, last, hasExtra, nil
}

func legacyCSVIsRepeatedHeader(record map[string]string, headers []string) bool {
	for _, header := range headers {
		if value, exists := record[header]; exists && strings.TrimSpace(value) == strings.TrimSpace(header) {
			return true
		}
	}
	return false
}
