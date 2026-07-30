//go:build windows

package datasourcemanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/etl/backend/internal/rpcmanager"
)

func TestSourceLifecycleAndEncryptedSecret(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if !strings.Contains(request.URL.Path, "/metadata") {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer private-source-key" {
			t.Errorf("missing decrypted authorization header")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"dataset":"binance-mainnet","real_time":true,"start_block":1}`))
	}))
	defer server.Close()
	root := testRoot(t)
	rpc, err := rpcmanager.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	writeInitialState(t, root, server.URL)
	manager, err := New(root, rpc)
	if err != nil {
		t.Fatal(err)
	}
	input := ConfigInput{
		ID: "sqd-mock", Type: TypeStream, Name: "SQD 测试流", Endpoint: server.URL,
		APIKey: "private-source-key", TimeoutMS: 3000, MaxConcurrency: 3, RetryCount: 2, Enabled: true,
	}
	source, err := manager.Save(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !source.SecretConfigured || strings.Contains(source.EndpointMasked, "private-source-key") {
		t.Fatalf("secret state mismatch: %+v", source)
	}
	content, err := os.ReadFile(filepath.Join(root, "config", "datasources.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "private-source-key") {
		t.Fatal("plaintext source secret leaked into config file")
	}
	result := manager.Test(context.Background(), source.ID)
	if !result.Success || result.Dataset != "binance-mainnet" || calls.Load() < 2 {
		t.Fatalf("source test failed: %+v calls=%d", result, calls.Load())
	}
	snapshot, err := manager.Snapshot()
	if err != nil || len(snapshot.Sources) != 2 {
		t.Fatalf("snapshot mismatch sources=%d err=%v", len(snapshot.Sources), err)
	}
	input.Name = "SQD 测试流已更新"
	input.APIKey = ""
	updated, err := manager.Save(context.Background(), input)
	if err != nil || updated.Name != input.Name || !updated.SecretConfigured {
		t.Fatalf("update mismatch: %+v err=%v", updated, err)
	}
	if err := manager.Delete("aws-mock"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = manager.Snapshot()
	if err != nil || len(snapshot.Sources) != 1 {
		t.Fatalf("delete mismatch sources=%d err=%v", len(snapshot.Sources), err)
	}
	manager.Close()
	manager, err = New(root, rpc)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	snapshot, err = manager.Snapshot()
	if err != nil {
		t.Fatalf("snapshot after restart: %v", err)
	}
	var persistedSource *Source
	for i := range snapshot.Sources {
		if snapshot.Sources[i].ID == "sqd-mock" {
			persistedSource = &snapshot.Sources[i]
			break
		}
	}
	if persistedSource == nil || persistedSource.Name != updatedNameForTest || !persistedSource.SecretConfigured {
		t.Fatalf("restart state mismatch: %+v", snapshot.Sources)
	}
}

const updatedNameForTest = "SQD 测试流已更新"

func TestRejectsUnsafeEndpointAndSystemDrive(t *testing.T) {
	if _, err := New(`C:\datasource-test`, nil); err == nil {
		t.Fatal("expected C drive rejection")
	}
	root := testRoot(t)
	rpc, err := rpcmanager.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	writeInitialState(t, root, "http://127.0.0.1:1")
	manager, err := New(root, rpc)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = manager.Save(context.Background(), ConfigInput{
		Type: TypeStream, Name: "不安全源", Endpoint: "http://example.com",
		TimeoutMS: 1000, MaxConcurrency: 1, Enabled: false,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTPS rejection, got %v", err)
	}
}

func writeInitialState(t *testing.T, root, endpoint string) {
	t.Helper()
	state := persistedState{Configs: []storedConfig{
		{ConfigInput: ConfigInput{
			ID: "sqd-mock", Type: TypeStream, Name: "SQD 测试流", Endpoint: endpoint,
			TimeoutMS: 3000, MaxConcurrency: 2, RetryCount: 1, Enabled: false,
		}},
		{ConfigInput: ConfigInput{
			ID: "aws-mock", Type: TypeDataset, Name: "AWS 测试集", Endpoint: endpoint,
			Bucket: "mock", Region: "test", Prefix: "v1/", TimeoutMS: 3000,
			MaxConcurrency: 2, RetryCount: 1, Enabled: false,
		}},
	}}
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config", "datasources.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
}

func testRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(`E:\codex\bsc_analytics\validation`, fmt.Sprintf("datasource-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if strings.HasPrefix(strings.ToLower(root), strings.ToLower(`E:\codex\bsc_analytics\validation\datasource-`)) {
			_ = os.RemoveAll(root)
		}
	})
	return root
}
