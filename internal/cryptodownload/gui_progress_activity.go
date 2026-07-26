package cryptodownload

import (
	"strconv"
	"strings"
	"time"
)

func nowGUIActivityTime() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func parseGUIBrowserProgressMessage(message string) (GUIAddressDownloadPart, bool, bool, bool) {
	if part, ok := parseGUIBrowserTotalMessage(message); ok {
		return part, false, true, true
	}
	if part, ok := parseGUIBrowserAccumulatedMessage(message); ok {
		return part, true, false, true
	}
	return GUIAddressDownloadPart{}, false, false, false
}

func parseGUIBrowserTotalMessage(message string) (GUIAddressDownloadPart, bool) {
	const prefix = "浏览器爬取 "
	if !strings.HasPrefix(message, prefix) || !strings.Contains(message, "总量=") {
		return GUIAddressDownloadPart{}, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	colon := strings.Index(rest, ":")
	totalPos := strings.LastIndex(rest, "总量=")
	if colon <= 0 || totalPos <= colon {
		return GUIAddressDownloadPart{}, false
	}
	chain := strings.ToUpper(strings.TrimSpace(rest[:colon]))
	kind := browserProgressKind(rest[colon+1 : totalPos])
	if kind == "" {
		return GUIAddressDownloadPart{}, false
	}
	total, err := parseLeadingGUIInt64(rest[totalPos+len("总量="):])
	if err != nil {
		return GUIAddressDownloadPart{}, false
	}
	return GUIAddressDownloadPart{
		Key:    guiDownloadPartKey(chain, kind),
		Chain:  chain,
		Kind:   kind,
		Total:  total,
		Status: "running",
	}, true
}

func parseLeadingGUIInt64(text string) (int64, error) {
	trimmed := strings.TrimSpace(text)
	end := 0
	for end < len(trimmed) {
		if trimmed[end] < '0' || trimmed[end] > '9' {
			break
		}
		end++
	}
	if end == 0 {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseInt(trimmed[:end], 10, 64)
}

func parseGUIBrowserAccumulatedMessage(message string) (GUIAddressDownloadPart, bool) {
	const prefix = "浏览器爬取 "
	if !strings.HasPrefix(message, prefix) || !strings.Contains(message, "累计 ") {
		return GUIAddressDownloadPart{}, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(message, prefix))
	colon := strings.Index(rest, ":")
	if colon <= 0 {
		return GUIAddressDownloadPart{}, false
	}
	chain := strings.ToUpper(strings.TrimSpace(rest[:colon]))
	afterChain := strings.TrimSpace(rest[colon+1:])
	accumulatedPos := strings.Index(afterChain, "累计 ")
	if accumulatedPos <= 0 {
		return GUIAddressDownloadPart{}, false
	}
	kind := browserProgressKind(afterChain[:accumulatedPos])
	if kind == "" {
		return GUIAddressDownloadPart{}, false
	}
	downloaded, _, _, ok := parseGUIDownloadCountText(afterChain[accumulatedPos+len("累计 "):])
	if !ok {
		return GUIAddressDownloadPart{}, false
	}
	return GUIAddressDownloadPart{
		Key:        guiDownloadPartKey(chain, kind),
		Chain:      chain,
		Kind:       kind,
		Downloaded: downloaded,
		Total:      -1,
		Status:     "running",
	}, true
}

func browserProgressKind(raw string) string {
	text := strings.ToLower(strings.TrimSpace(raw))
	text = strings.TrimSuffix(text, "，offset上限=")
	switch {
	case strings.Contains(text, "普通交易") || strings.Contains(text, "transactions"):
		return "transactions"
	case strings.Contains(text, "代币转账") || strings.Contains(text, "token"):
		return "token_transfers"
	case strings.Contains(text, "内部交易") || strings.Contains(text, "internal"):
		return "internal"
	case strings.Contains(text, "nft"):
		return "nft"
	default:
		return ""
	}
}
