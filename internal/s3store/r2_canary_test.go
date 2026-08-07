package s3store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestR2Canary 真实 Cloudflare R2 最小连接测试（仅 RUN_R2_CANARY=1 时执行）。
// 步骤：PUT → HEAD → GET → LIST → DELETE → HEAD(不存在)。
// 禁止打印 R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY / SQD_DEPLOY_KEY。
func TestR2Canary(t *testing.T) {
	if os.Getenv("RUN_R2_CANARY") != "1" {
		t.Skip("设置 RUN_R2_CANARY=1 执行真实 R2 Canary")
	}
	ctx := context.Background()
	const key = "sqd-cloud/health/r2-canary.txt"
	body := []byte("pangu-sqd-cloud r2 canary\n")
	red := newRedactor()

	cfg := Config{
		Endpoint:  os.Getenv("R2_ENDPOINT"),
		Bucket:    "pangu-sqd-cloud", // 用户指定 bucket
		AccessKey: os.Getenv("R2_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Region:    os.Getenv("R2_REGION"),
	}
	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		t.Fatal("缺少 R2_ENDPOINT / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY（长度检查通过但值为空）")
	}
	store := NewS3Store(cfg)

	type stepResult struct {
		name   string
		pass   bool
		status int
		ms     int64
		err    string
	}
	var results []stepResult
	run := func(name string, fn func() error) {
		start := time.Now()
		err := fn()
		results = append(results, stepResult{
			name: name, pass: err == nil, status: store.LastStatus(),
			ms: time.Since(start).Milliseconds(), err: red(errString(err)),
		})
	}

	run("PUT", func() error { return store.Put(ctx, key, body) })
	run("HEAD", func() error {
		ok, err := store.Exists(ctx, key)
		if err == nil && !ok {
			return fmt.Errorf("HEAD 返回不存在")
		}
		return err
	})
	run("GET", func() error {
		got, err := store.Get(ctx, key)
		if err == nil && string(got) != string(body) {
			return fmt.Errorf("内容不一致：got %d bytes want %d bytes", len(got), len(body))
		}
		return err
	})
	run("LIST", func() error {
		items, err := store.List(ctx, "sqd-cloud/health/")
		if err == nil {
			found := false
			for _, it := range items {
				if it.Key == key {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("LIST 未找到 %s（共 %d 项）", key, len(items))
			}
		}
		return err
	})
	run("DELETE", func() error { return store.Delete(ctx, key) })
	run("HEAD-after-DELETE", func() error {
		ok, err := store.Exists(ctx, key)
		if err == nil && ok {
			return fmt.Errorf("DELETE 后对象仍存在")
		}
		return err
	})

	allPass := true
	for _, r := range results {
		statusText := "0"
		if r.status > 0 {
			statusText = fmt.Sprintf("%d", r.status)
		}
		status := "PASS"
		if !r.pass {
			status = "FAIL"
			allPass = false
		}
		t.Logf("%s %s HTTP=%s %dms err=%s", r.name, status, statusText, r.ms, r.err)
	}
	if !allPass {
		t.Fatal("R2 Canary 存在 FAIL 步骤")
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// newRedactor 生成脱敏函数：把密钥/Key ID 值替换为 ***，防止任何错误文本泄漏。
func newRedactor() func(string) string {
	secrets := []string{
		os.Getenv("R2_ACCESS_KEY_ID"),
		os.Getenv("R2_SECRET_ACCESS_KEY"),
		os.Getenv("SQD_DEPLOY_KEY"),
	}
	return func(s string) string {
		if s == "" {
			return ""
		}
		for _, v := range secrets {
			if v != "" {
				s = strings.ReplaceAll(s, v, "***")
			}
		}
		return s
	}
}
