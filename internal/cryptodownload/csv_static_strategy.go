package cryptodownload

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const csvStaticProbeLimit = 8 << 10

type csvStaticObjectVersion struct {
	etag         string
	lastModified string
}

type csvStaticObject struct {
	size           int64
	filename       string
	sha256         string
	version        csvStaticObjectVersion
	rangeSupported bool
}

type csvStaticProbe struct {
	object csvStaticObject
}

func probeCSVStaticObject(ctx context.Context, client *http.Client, observer requestTimingObserver, link string) (csvStaticProbe, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return csvStaticProbe{}, fmt.Errorf("create static CSV probe: %w", err)
	}
	setCSVStaticHeaders(req)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := doHTTPRequest(client, req, observer)
	if err != nil {
		return csvStaticProbe{}, newCSVStaticError(csvStaticRetryable, 0, err)
	}
	defer resp.Body.Close()
	prefix, readErr := io.ReadAll(io.LimitReader(resp.Body, csvStaticProbeLimit+1))
	if readErr != nil {
		return csvStaticProbe{}, newCSVStaticError(csvStaticRetryable, resp.StatusCode, readErr)
	}
	if isNoSuchKeyPayload(prefix) {
		return csvStaticProbe{}, errCSVStaticNotReady
	}
	if csvStaticLooksHTML(prefix) {
		return csvStaticProbe{}, newCSVStaticError(csvStaticInvalid, resp.StatusCode, fmt.Errorf("static CSV probe returned HTML"))
	}
	object := csvStaticObject{
		size:     resp.ContentLength,
		filename: csvDownloadFilename(resp.Header),
		sha256:   csvStaticSHA256(resp.Header),
		version:  csvStaticVersion(resp.Header),
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return csvStaticProbe{object: object}, nil
	case http.StatusPartialContent:
		total, err := parseCSVStaticProbeRange(resp.Header.Get("Content-Range"))
		if err != nil || len(prefix) != 1 {
			object.size = -1
			return csvStaticProbe{object: object}, nil
		}
		object.size = total
		object.rangeSupported = total >= defaultCSVStaticPolicy().rangeThreshold && object.version.stable()
		return csvStaticProbe{object: object}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		object.size = -1
		return csvStaticProbe{object: object}, nil
	default:
		return csvStaticProbe{}, csvStaticStatusError(resp.StatusCode, prefix)
	}
}

func (v csvStaticObjectVersion) stable() bool {
	return v.etag != "" || v.lastModified != ""
}

func downloadCSVStaticToPath(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, object csvStaticObject, target string, policy csvStaticPolicy) error {
	_, err := downloadCSVStaticToPathWithFilename(ctx, client, observer, link, object, target, policy)
	return err
}

func downloadCSVStaticToPathWithFilename(ctx context.Context, client *http.Client, observer requestTimingObserver, link string, object csvStaticObject, target string, policy csvStaticPolicy) (string, error) {
	if object.rangeSupported && object.size >= policy.rangeThreshold {
		err := downloadCSVStaticRangesToPath(ctx, client, observer, link, object, target, policy)
		if err == nil {
			return object.filename, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if !csvStaticFallsBackToSingle(err) {
			return "", err
		}
	}
	return downloadCSVStaticSingleToPath(ctx, client, observer, link, target, policy)
}

func parseCSVStaticProbeRange(value string) (int64, error) {
	var total int64
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "bytes 0-0/%d", &total); err != nil || total <= 0 {
		return 0, fmt.Errorf("invalid probe Content-Range %q", value)
	}
	return total, nil
}

func csvStaticVersion(header http.Header) csvStaticObjectVersion {
	return csvStaticObjectVersion{etag: strings.TrimSpace(header.Get("ETag")), lastModified: strings.TrimSpace(header.Get("Last-Modified"))}
}

func csvStaticSHA256(header http.Header) string {
	for _, key := range []string{"X-Checksum-Sha256", "X-Amz-Meta-Sha256"} {
		if value := strings.TrimSpace(header.Get(key)); len(value) == 64 {
			if _, err := hex.DecodeString(value); err == nil {
				return strings.ToLower(value)
			}
		}
	}
	for _, part := range strings.Split(header.Get("Digest"), ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(name, "sha-256") {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err == nil && len(decoded) == 32 {
			return hex.EncodeToString(decoded)
		}
	}
	return ""
}

func csvDownloadFilename(header http.Header) string {
	disposition := header.Get("Content-Disposition")
	if index := strings.Index(strings.ToLower(disposition), "filename="); index >= 0 {
		if filename := strings.Trim(strings.TrimSpace(disposition[index+9:]), `"`); filename != "" {
			return filename
		}
	}
	return "download.csv"
}

func setCSVStaticHeaders(req *http.Request) { req.Header.Set("User-Agent", "Mozilla/5.0") }

func csvStaticLooksHTML(body []byte) bool {
	prefix := strings.ToLower(strings.TrimSpace(string(body)))
	return strings.HasPrefix(prefix, "<!doctype html") || strings.HasPrefix(prefix, "<html")
}

func errorsOr(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}
