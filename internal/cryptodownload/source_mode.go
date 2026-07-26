package cryptodownload

import (
	"errors"
	"fmt"
	"strings"
)

type DownloadMode string

const (
	DownloadModeAuto      DownloadMode = "auto"
	DownloadModeBrowser   DownloadMode = "browser"
	DownloadModeCSVDirect DownloadMode = "csv-direct"
	DownloadModeCSVEmail  DownloadMode = "csv-email"
	DownloadModeRPC       DownloadMode = "rpc"
	DownloadModeLegacyCSV DownloadMode = "csv"
)

type Source string

const (
	SourceUnavailable Source = "unavailable"
	SourceCSVDirect   Source = "csv-direct"
	SourceCSVEmail    Source = "csv-email"
	SourceBrowser     Source = "browser"
	SourceRPC         Source = "rpc"
)

type Dataset string

const (
	DatasetOverview             Dataset = "overview"
	DatasetTransactions         Dataset = "transactions"
	DatasetTokenTransfers       Dataset = "token-transfers"
	DatasetInternalTransactions Dataset = "internal-transactions"
	DatasetNFTTransfers         Dataset = "nft-transfers"
	DatasetAssets               Dataset = "assets"
	DatasetAssetHistory         Dataset = "asset-history"
	DatasetMethods              Dataset = "methods"
)

var (
	ErrUnknownDownloadMode = errors.New("unknown download mode")
	ErrUnknownSource       = errors.New("unknown source")
	ErrUnknownDataset      = errors.New("unknown dataset")
)

func NormalizeRequestedSource(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func ParseDownloadMode(raw string) (DownloadMode, error) {
	mode := DownloadMode(NormalizeRequestedSource(raw))
	if !mode.valid() {
		return "", fmt.Errorf("download mode %q: %w", raw, ErrUnknownDownloadMode)
	}
	return mode, nil
}

func ParseSource(raw string) (Source, error) {
	source := Source(NormalizeRequestedSource(raw))
	if !source.valid() {
		return "", fmt.Errorf("source %q: %w", raw, ErrUnknownSource)
	}
	return source, nil
}

func ParseDataset(raw string) (Dataset, error) {
	dataset := Dataset(NormalizeRequestedSource(raw))
	if !dataset.valid() {
		return "", fmt.Errorf("dataset %q: %w", raw, ErrUnknownDataset)
	}
	return dataset, nil
}

func (mode DownloadMode) valid() bool {
	switch mode {
	case DownloadModeAuto,
		DownloadModeBrowser,
		DownloadModeCSVDirect,
		DownloadModeCSVEmail,
		DownloadModeRPC,
		DownloadModeLegacyCSV:
		return true
	default:
		return false
	}
}

func (source Source) valid() bool {
	switch source {
	case SourceCSVDirect, SourceCSVEmail, SourceBrowser, SourceRPC:
		return true
	default:
		return false
	}
}

func (dataset Dataset) valid() bool {
	switch dataset {
	case DatasetOverview,
		DatasetTransactions,
		DatasetTokenTransfers,
		DatasetInternalTransactions,
		DatasetNFTTransfers,
		DatasetAssets,
		DatasetAssetHistory,
		DatasetMethods:
		return true
	default:
		return false
	}
}
