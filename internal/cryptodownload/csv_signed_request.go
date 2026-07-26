package cryptodownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type csvSignedRequestTemplate struct {
	Headers   map[string]string
	Timestamp string
}

type csvHARFile struct {
	Log csvHARLog `json:"log"`
}

type csvHARLog struct {
	Entries []csvHAREntry `json:"entries"`
}

type csvHAREntry struct {
	Request csvHARRequest `json:"request"`
}

type csvHARRequest struct {
	Method  string         `json:"method"`
	URL     string         `json:"url"`
	Headers []csvHARHeader `json:"headers"`
}

type csvHARHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func loadCSVAsyncSignedRequest(path string) (*csvSignedRequestTemplate, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 CSV 请求 HAR 失败: %w", err)
	}
	var har csvHARFile
	if err := json.Unmarshal(data, &har); err != nil {
		return nil, fmt.Errorf("解析 CSV 请求 HAR 失败: %w", err)
	}
	for _, entry := range har.Log.Entries {
		if !isCSVAsyncHAREntry(entry.Request) {
			continue
		}
		template := csvSignedRequestTemplate{Headers: map[string]string{}}
		for _, header := range entry.Request.Headers {
			name := strings.TrimSpace(header.Name)
			if !csvSignedHeaderAllowed(name) {
				continue
			}
			template.Headers[http.CanonicalHeaderKey(name)] = header.Value
		}
		template.Timestamp = firstNonEmpty(template.Headers["Ok-Timestamp"], csvTimestampFromURL(entry.Request.URL))
		if len(template.Headers) == 0 {
			return nil, errors.New("CSV 请求 HAR 未包含可复用请求头")
		}
		return &template, nil
	}
	return nil, errors.New("CSV 请求 HAR 未找到 download/async POST 请求")
}

func isCSVAsyncHAREntry(req csvHARRequest) bool {
	return strings.EqualFold(req.Method, http.MethodPost) &&
		strings.Contains(req.URL, "/download/explorer/v1/") &&
		strings.Contains(req.URL, "/download/async")
}

func csvSignedHeaderAllowed(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || strings.HasPrefix(lower, ":") {
		return false
	}
	switch lower {
	case "accept-encoding", "connection", "content-length", "cookie", "host":
		return false
	default:
		return true
	}
}

func csvTimestampFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("t")
}

func (t csvSignedRequestTemplate) Apply(req *http.Request, baseURL, referer, timestamp string) {
	for name, value := range t.Headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", referer)
	if timestamp != "" {
		req.Header.Set("Ok-Timestamp", timestamp)
	}
}
