package cryptodownload

import "context"

func normalizedSource(source string) string {
	return NormalizeRequestedSource(source)
}

func supportedSource(source string) bool {
	switch normalizedSource(source) {
	case "rpc", "csv", "browser":
		return true
	default:
		return false
	}
}

func collectForSource(ctx context.Context, cfg Config) ExportData {
	switch normalizedSource(cfg.Source) {
	case "rpc":
		return collectAllFromRPC(ctx, cfg)
	case "csv":
		reportProgress(cfg, "CSV模式使用OKLink CSV下载，直连超限时使用邮箱CSV")
		return collectAllFromCSV(ctx, cfg)
	case "browser":
		return collectAllFromBrowser(ctx, cfg)
	default:
		return ExportData{Errors: []string{"source 只能是 rpc、csv 或 browser"}}
	}
}
