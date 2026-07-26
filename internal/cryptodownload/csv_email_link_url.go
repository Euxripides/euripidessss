package cryptodownload

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const csvMailAllowedHost = "static.oklink.com"

var staticCSVTargetPattern = regexp.MustCompile(`(?i)https?://static\.oklink\.com/[^\s"'<>\\]+?\.(?:csv|zip)(?:\?[^\s"'<>\\]*)?`)

func normalizeCSVEmailLink(raw string) string {
	link := strings.TrimSpace(raw)
	if link == "" || strings.HasSuffix(link, "=") {
		return ""
	}
	variants := []string{link, htmlUnescapeURL(link)}
	for range 3 {
		decoded, err := url.QueryUnescape(variants[len(variants)-1])
		if err != nil || decoded == variants[len(variants)-1] {
			break
		}
		variants = append(variants, decoded)
	}
	for _, variant := range variants {
		candidate := strings.TrimRight(strings.TrimSpace(variant), ".,);]")
		lowerCandidate := strings.ToLower(candidate)
		if strings.HasPrefix(lowerCandidate, "javascript:") || strings.HasPrefix(lowerCandidate, "data:") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(candidate), csvMailAllowedHost+"/") {
			candidate = "https://" + candidate
		}
		if normalized := canonicalCSVEmailURL(candidate); normalized != "" {
			return normalized
		}
		if target := staticCSVTargetPattern.FindString(variant); target != "" {
			if normalized := canonicalCSVEmailURL(target); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func canonicalCSVEmailURL(raw string) string {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), ".,);]"))
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Hostname() == "" || parsed.Port() != "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if !strings.EqualFold(parsed.Hostname(), csvMailAllowedHost) {
		return ""
	}
	decodedPath, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || strings.ContainsAny(decodedPath, "\\\x00") || path.Clean(decodedPath) != decodedPath {
		return ""
	}
	lowerPath := strings.ToLower(decodedPath)
	allowedPath := strings.HasPrefix(lowerPath, "/cdn/explorer/download/") || strings.HasPrefix(lowerPath, "/download/explorer/")
	if !allowedPath || (!strings.HasSuffix(lowerPath, ".csv") && !strings.HasSuffix(lowerPath, ".zip")) {
		return ""
	}
	parsed.Scheme = "https"
	parsed.Host = csvMailAllowedHost
	parsed.Fragment = ""
	return parsed.String()
}

func (c *CSVExportClient) downloadCSVEmailLink(ctx context.Context, raw string, progress csvDownloadProgress) ([]byte, string, error) {
	link := normalizeCSVEmailLink(raw)
	if link == "" {
		return nil, "", fmt.Errorf("email CSV link rejected by host/path policy")
	}
	return c.downloadLinkWithProgress(ctx, link, progress)
}
