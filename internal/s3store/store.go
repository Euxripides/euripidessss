// Package s3store 提供 S3-compatible 对象存储客户端（Cloudflare R2 / AWS S3 / 本地文件存储）。
// 用于 SQD Cloud Phase 4：Job Queue（pending/leased/completed/failed）、Parquet 输出与 Manifest。
// 密钥只允许来自环境变量（R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY），
// 不落盘、不进日志、不进前端响应、不进 Git。
package s3store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ObjectInfo 对象元信息。
type ObjectInfo struct {
	Key  string `json:"key"`
	Size int64  `json:"size,omitempty"`
}

// ObjectStore 对象存储统一接口（设计 §11）。
type ObjectStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, body []byte) error
	Exists(ctx context.Context, key string) (bool, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

// Config S3-compatible 客户端配置。
type Config struct {
	Endpoint  string // 例如 https://<account>.r2.cloudflarestorage.com
	Bucket    string
	AccessKey string
	SecretKey string
	Token     string // 可选 Session Token（R2 通常不需要；非空时才发送）
	Region    string // R2 使用 auto
}

// S3Store 基于 SigV4 的 S3-compatible 客户端（path-style）。
type S3Store struct {
	cfg        Config
	client     *http.Client
	lastStatus atomic.Int64
}

// NewS3Store 创建 S3 客户端。
func NewS3Store(cfg Config) *S3Store {
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	return &S3Store{
		cfg:    cfg,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	req, err := s.newRequest(ctx, http.MethodGet, key, "", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	s.setStatus(resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("S3 GET %s: HTTP %d %s", key, resp.StatusCode, truncate(string(body), 200))
	}
	return io.ReadAll(resp.Body)
}

func (s *S3Store) Put(ctx context.Context, key string, body []byte) error {
	req, err := s.newRequest(ctx, http.MethodPut, key, "", body)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	s.setStatus(resp.StatusCode)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		bodyText, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("S3 PUT %s: HTTP %d %s", key, resp.StatusCode, truncate(string(bodyText), 200))
	}
	return nil
}

func (s *S3Store) Exists(ctx context.Context, key string) (bool, error) {
	req, err := s.newRequest(ctx, http.MethodHead, key, "", nil)
	if err != nil {
		return false, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	s.setStatus(resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("S3 HEAD %s: HTTP %d", key, resp.StatusCode)
	}
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	req, err := s.newRequest(ctx, http.MethodDelete, key, "", nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	s.setStatus(resp.StatusCode)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("S3 DELETE %s: HTTP %d", key, resp.StatusCode)
	}
	return nil
}

// List ListObjectsV2（递归 prefix，无 delimiter），返回匹配前缀的全部对象。
// 注意：队列对象嵌套在 {job}/{chunk}/ 下，使用 delimiter 会漏掉深层对象。
func (s *S3Store) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	continuation := ""
	for {
		query := "list-type=2&prefix=" + url.QueryEscape(prefix)
		if continuation != "" {
			query += "&continuation-token=" + url.QueryEscape(continuation)
		}
		req, err := s.newRequest(ctx, http.MethodGet, "", query, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		s.setStatus(resp.StatusCode)
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, fmt.Errorf("S3 LIST %s: HTTP %d %s", prefix, resp.StatusCode, truncate(string(body), 200))
		}
		var result listBucketResult
		if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("S3 LIST 解析失败: %w", err)
		}
		resp.Body.Close()
		for _, c := range result.Contents {
			out = append(out, ObjectInfo{Key: c.Key, Size: c.Size})
		}
		if result.IsTruncated && result.NextContinuationToken != "" {
			continuation = result.NextContinuationToken
			continue
		}
		break
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// LastStatus 返回最近一次请求的实际 HTTP 状态码（网络错误时为 0）。
func (s *S3Store) LastStatus() int {
	return int(s.lastStatus.Load())
}

func (s *S3Store) setStatus(code int) {
	s.lastStatus.Store(int64(code))
}

type listBucketResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	} `xml:"Contents"`
}

// newRequest 构造并签名请求。key 为空时使用 bucket 根（List）。
func (s *S3Store) newRequest(ctx context.Context, method, key, query string, body []byte) (*http.Request, error) {
	endpoint := strings.TrimRight(s.cfg.Endpoint, "/")
	bucket := s.cfg.Bucket
	if key != "" {
		bucket += "/" + escapeKey(key)
	}
	u := endpoint + "/" + bucket
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	payloadHash := sha256Hex(body)
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("host", req.URL.Host)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	if s.cfg.Token != "" {
		req.Header.Set("x-amz-security-token", s.cfg.Token)
	}
	host := req.URL.Host
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalURI := "/" + bucket
	if canonicalURI == "/" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalQueryString(query)
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := dateStamp + "/" + s.cfg.Region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+s.cfg.SecretKey), dateStamp), s.cfg.Region), "s3"), "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.cfg.AccessKey, scope, signedHeaders, signature,
	))
	return req, nil
}

func canonicalQueryString(query string) string {
	if query == "" {
		return ""
	}
	values, _ := url.ParseQuery(query)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func escapeKey(key string) string {
	segments := strings.Split(key, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ── 本地文件存储（开发/测试/无 R2 环境）──

// LocalStore 以本地目录模拟对象存储（key 使用 / 分隔）。
type LocalStore struct {
	root string
}

// NewLocalStore 创建本地对象存储。
func NewLocalStore(root string) *LocalStore {
	return &LocalStore{root: root}
}

func (s *LocalStore) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func (s *LocalStore) Get(ctx context.Context, key string) ([]byte, error) {
	p := s.path(key)
	if err := checkPathInside(s.root, p); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (s *LocalStore) Put(ctx context.Context, key string, body []byte) error {
	p := s.path(key)
	if err := checkPathInside(s.root, p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o644)
}

func (s *LocalStore) Exists(ctx context.Context, key string) (bool, error) {
	p := s.path(key)
	if err := checkPathInside(s.root, p); err != nil {
		return false, err
	}
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *LocalStore) Delete(ctx context.Context, key string) error {
	p := s.path(key)
	if err := checkPathInside(s.root, p); err != nil {
		return err
	}
	err := os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *LocalStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	root := filepath.Join(s.root, filepath.FromSlash(prefix))
	var out []ObjectInfo
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		out = append(out, ObjectInfo{Key: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// checkPathInside 防路径穿越（对象键不得逃出存储根目录）。
func checkPathInside(root, p string) error {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("对象键越界: " + filepath.ToSlash(p))
	}
	return nil
}

// NewFromEnv 按环境变量创建存储：
//   - R2_BACKEND=local → LocalStore（根目录 R2_LOCAL_ROOT，默认 <temp>/r2store）
//   - 否则需要 R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY
func NewFromEnv(localRoot string) (ObjectStore, error) {
	if strings.EqualFold(os.Getenv("R2_BACKEND"), "local") {
		if localRoot == "" {
			localRoot = os.Getenv("R2_LOCAL_ROOT")
		}
		if localRoot == "" {
			return nil, errors.New("R2_BACKEND=local 需要 R2_LOCAL_ROOT")
		}
		return NewLocalStore(localRoot), nil
	}
	cfg := Config{
		Endpoint:  os.Getenv("R2_ENDPOINT"),
		Bucket:    os.Getenv("R2_BUCKET"),
		AccessKey: os.Getenv("R2_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Region:    os.Getenv("R2_REGION"),
	}
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("缺少 R2/S3 凭据（R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID/R2_SECRET_ACCESS_KEY）")
	}
	return NewS3Store(cfg), nil
}
