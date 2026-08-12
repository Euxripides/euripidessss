//go:build windows

package rpcmanager

import (
	"bytes"
	"context"
	"database/sql"
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
)

func TestCreateMasksSecretAndSurvivesRestart(t *testing.T) {
	var requests atomic.Int64
	server := rpcServer(t, func(method string) (int, any) {
		requests.Add(1)
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()
	root := testRoot(t)
	manager, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	item, err := manager.Create(context.Background(), endpointInput("主节点", server.URL+"/secret-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(item.EndpointMasked, "secret-api-key") || !item.SecretConfigured {
		t.Fatalf("endpoint secret leaked: %+v", item)
	}
	dbContent, err := os.ReadFile(filepath.Join(root, "config", "rpc_control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(dbContent), "secret-api-key") {
		t.Fatal("plaintext endpoint exists in SQLite")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	result, err := manager.TestEndpoint(context.Background(), item.ID)
	if err != nil || !result.Success || requests.Load() < 4 {
		t.Fatalf("restart decrypt test failed: result=%+v requests=%d err=%v", result, requests.Load(), err)
	}
}

func TestBatchCreateReportsPerItemAndNeverEchoesSecrets(t *testing.T) {
	server := rpcServer(t, func(method string) (int, any) {
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first := endpointInput("账号一", server.URL+"/private-key-one")
	duplicate := endpointInput("重复账号", server.URL+"/private-key-one")
	second := endpointInput("账号二", server.URL+"/private-key-two")
	body, err := json.Marshal(BatchCreateInput{Items: []EndpointInput{first, duplicate, second}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc/endpoints/batch", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	NewHandler(manager).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response BatchCreateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 3 || response.CreatedCount != 2 || response.FailureCount != 1 || len(response.Created) != 2 || len(response.Failures) != 1 {
		t.Fatalf("batch response mismatch: %+v", response)
	}
	if response.Failures[0].Index != 1 || !strings.Contains(response.Failures[0].Detail, "重复") {
		t.Fatalf("duplicate failure mismatch: %+v", response.Failures)
	}
	responseText := recorder.Body.String()
	if strings.Contains(responseText, "private-key-one") || strings.Contains(responseText, "private-key-two") {
		t.Fatalf("batch response leaked endpoint secret: %s", responseText)
	}
	items, err := manager.Endpoints()
	if err != nil || len(items) != 2 {
		t.Fatalf("persisted batch mismatch: items=%d err=%v", len(items), err)
	}
	retry, err := manager.CreateBatch(context.Background(), []EndpointInput{first})
	if err != nil || retry.CreatedCount != 0 || retry.FailureCount != 1 || !strings.Contains(retry.Failures[0].Detail, "已存在") {
		t.Fatalf("persisted duplicate must be rejected: %+v err=%v", retry, err)
	}
}

func TestBatchCreateAllSuccessSerializesEmptyFailuresArray(t *testing.T) {
	server := rpcServer(t, func(method string) (int, any) {
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	result, err := manager.CreateBatch(context.Background(), []EndpointInput{
		endpointInput("账号一", server.URL+"/first"),
		endpointInput("账号二", server.URL+"/second"),
	})
	if err != nil || result.CreatedCount != 2 || result.FailureCount != 0 || result.Failures == nil || len(result.Failures) != 0 {
		t.Fatalf("all-success batch mismatch: %+v err=%v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"failures":[]`) {
		t.Fatalf("failures must serialize as [], got %s", encoded)
	}
}

func TestRejectsChainMismatch(t *testing.T) {
	server := rpcServer(t, func(method string) (int, any) {
		if method == "eth_chainId" {
			return http.StatusOK, "0x1"
		}
		return http.StatusOK, "0x64"
	})
	defer server.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = manager.Create(context.Background(), endpointInput("错误链", server.URL))
	if err == nil || !strings.Contains(err.Error(), "CHAIN_ID_MISMATCH") {
		t.Fatalf("expected chain mismatch, got %v", err)
	}
}

func TestDedicatedTestEndpointNeverEntersProductionRouting(t *testing.T) {
	var primaryCalls, testCalls atomic.Int64
	primary := rpcServer(t, func(method string) (int, any) {
		primaryCalls.Add(1)
		return http.StatusOK, rpcResult(method)
	})
	defer primary.Close()
	testEndpoint := rpcServer(t, func(method string) (int, any) {
		testCalls.Add(1)
		return http.StatusOK, rpcResult(method)
	})
	defer testEndpoint.Close()

	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	input := endpointInput("独立测试节点", primary.URL+"/primary-secret")
	input.TestEndpointURL = testEndpoint.URL + "/test-secret"
	item, err := manager.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !item.TestEndpointConfigured || strings.Contains(item.TestEndpointMasked, "test-secret") {
		t.Fatalf("test endpoint was not safely exposed: %+v", item)
	}
	result, err := manager.TestEndpoint(context.Background(), item.ID)
	if err != nil || !result.Success || result.EndpointRole != "TEST" {
		t.Fatalf("dedicated endpoint test mismatch: %+v err=%v", result, err)
	}
	if _, _, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err != nil {
		t.Fatal(err)
	}
	manager.RefreshHealth(context.Background())
	if testCalls.Load() != 4 {
		t.Fatalf("manual tests must use dedicated endpoint, calls=%d", testCalls.Load())
	}
	if primaryCalls.Load() != 5 {
		t.Fatalf("production call and health check must use primary endpoint, calls=%d", primaryCalls.Load())
	}
}

func TestEndpointTestFallsBackToPrimary(t *testing.T) {
	var calls atomic.Int64
	primary := rpcServer(t, func(method string) (int, any) {
		calls.Add(1)
		return http.StatusOK, rpcResult(method)
	})
	defer primary.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	item, err := manager.Create(context.Background(), endpointInput("正常节点", primary.URL))
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.TestEndpoint(context.Background(), item.ID)
	if err != nil || !result.Success || result.EndpointRole != "PRIMARY" || calls.Load() != 4 {
		t.Fatalf("primary endpoint fallback mismatch: %+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestCallPreflightsOnlyWhenHealthIsStale(t *testing.T) {
	if healthCheckInterval != 30*time.Minute {
		t.Fatalf("unexpected health interval: %s", healthCheckInterval)
	}
	var calls atomic.Int64
	primary := rpcServer(t, func(method string) (int, any) {
		calls.Add(1)
		return http.StatusOK, rpcResult(method)
	})
	defer primary.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	item, err := manager.Create(context.Background(), endpointInput("按需预检节点", primary.URL))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("fresh health must not trigger extra tests, calls=%d", calls.Load())
	}
	health := manager.store.health(item.ID)
	stale := time.Now().UTC().Add(-31 * time.Minute)
	health.CheckedAt = &stale
	if err := manager.store.saveHealth(health); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 7 {
		t.Fatalf("stale health must run one preflight before the request, calls=%d", calls.Load())
	}
}

func TestTurboUsesDisabledEndpointsWithoutEnablingThem(t *testing.T) {
	var firstCalls, secondCalls atomic.Int64
	first := rpcServer(t, func(method string) (int, any) {
		if method == "eth_getBalance" {
			firstCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer first.Close()
	second := rpcServer(t, func(method string) (int, any) {
		if method == "eth_getBalance" {
			secondCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer second.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for index, server := range []*httptest.Server{first, second} {
		input := endpointInput(fmt.Sprintf("Turbo 禁用节点 %d", index+1), server.URL)
		input.Enabled = false
		if _, err := manager.Create(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if manager.HasConfigured("bsc") || !manager.HasAnyConfigured("bsc") {
		t.Fatal("normal routing must remain disabled while Turbo sees configured endpoints")
	}
	if !manager.HasTurboAvailable("bsc") {
		t.Fatal("healthy disabled endpoints must remain available to Turbo")
	}
	if _, _, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err == nil {
		t.Fatal("normal RPC call unexpectedly used a disabled endpoint")
	}
	for range 2 {
		if _, _, err := manager.CallTurbo(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err != nil {
			t.Fatal(err)
		}
	}
	if firstCalls.Load() == 0 || secondCalls.Load() == 0 {
		t.Fatalf("Turbo did not rotate over all configured endpoints: first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
	items, err := manager.Endpoints()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Enabled {
			t.Fatalf("Turbo permanently enabled endpoint %s", item.ID)
		}
	}
}

func TestTurboAvailabilityExcludesKnownMisconfiguredEndpoint(t *testing.T) {
	server := rpcServer(t, func(method string) (int, any) {
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	endpoint, err := manager.Create(context.Background(), endpointInput("Turbo misconfigured", server.URL))
	if err != nil {
		t.Fatal(err)
	}
	openUntil := time.Now().UTC().Add(time.Hour)
	if err = manager.store.saveHealth(Health{
		EndpointID: endpoint.ID, Status: StatusMisconfigured,
		CircuitState: CircuitOpen, CircuitOpenUntil: &openUntil,
	}); err != nil {
		t.Fatal(err)
	}
	if manager.HasTurboAvailable("bsc") {
		t.Fatal("known misconfigured endpoint must not reserve a Turbo RPC lane")
	}
}

func TestNormalAvailabilityMatchesRoutingEligibility(t *testing.T) {
	server := rpcServer(t, func(method string) (int, any) {
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()

	newManager := func(t *testing.T) (*Manager, Endpoint) {
		t.Helper()
		manager, err := New(testRoot(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { manager.Close() })
		endpoint, err := manager.Create(context.Background(), endpointInput("availability", server.URL))
		if err != nil {
			t.Fatal(err)
		}
		return manager, endpoint
	}

	t.Run("healthy enabled", func(t *testing.T) {
		manager, _ := newManager(t)
		if !manager.HasAvailable("bsc") {
			t.Fatal("healthy enabled endpoint must be routable")
		}
	})
	t.Run("known misconfigured", func(t *testing.T) {
		manager, endpoint := newManager(t)
		if err := manager.store.saveHealth(Health{EndpointID: endpoint.ID, Status: StatusMisconfigured, CircuitState: CircuitClosed}); err != nil {
			t.Fatal(err)
		}
		if manager.HasAvailable("bsc") {
			t.Fatal("misconfigured endpoint must not be advertised")
		}
	})
	t.Run("active open circuit", func(t *testing.T) {
		manager, endpoint := newManager(t)
		until := time.Now().UTC().Add(time.Minute)
		if err := manager.store.saveHealth(Health{EndpointID: endpoint.ID, Status: StatusUnavailable, CircuitState: CircuitOpen, CircuitOpenUntil: &until}); err != nil {
			t.Fatal(err)
		}
		if manager.HasAvailable("bsc") {
			t.Fatal("active open circuit must not be advertised")
		}
	})
	t.Run("expired open circuit becomes half open", func(t *testing.T) {
		manager, endpoint := newManager(t)
		until := time.Now().UTC().Add(-time.Minute)
		if err := manager.store.saveHealth(Health{EndpointID: endpoint.ID, Status: StatusUnavailable, CircuitState: CircuitOpen, CircuitOpenUntil: &until}); err != nil {
			t.Fatal(err)
		}
		if !manager.HasAvailable("bsc") {
			t.Fatal("expired circuit must be eligible for half-open recovery")
		}
		if got := manager.store.health(endpoint.ID).CircuitState; got != CircuitHalfOpen {
			t.Fatalf("circuit state=%s, want %s", got, CircuitHalfOpen)
		}
	})
	t.Run("disabled remains excluded from normal routing", func(t *testing.T) {
		manager, endpoint := newManager(t)
		disabled := false
		if _, err := manager.Update(context.Background(), endpoint.ID, EndpointPatch{Enabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		if manager.HasAvailable("bsc") {
			t.Fatal("disabled endpoint must not be advertised to normal routing")
		}
	})
}

func TestFailoverAfterRateLimit(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int64
	primary := rpcServer(t, func(method string) (int, any) {
		if method == "eth_chainId" || method == "eth_blockNumber" {
			return http.StatusOK, rpcResult(method)
		}
		primaryCalls.Add(1)
		return http.StatusTooManyRequests, map[string]string{"error": "rate limit"}
	})
	defer primary.Close()
	backup := rpcServer(t, func(method string) (int, any) {
		if method != "eth_chainId" && method != "eth_blockNumber" {
			backupCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer backup.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first := endpointInput("主节点", primary.URL)
	first.Priority = 10
	second := endpointInput("备用节点", backup.URL)
	second.Priority = 20
	if _, err := manager.Create(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	result, endpointID, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"})
	if err != nil || string(result) != `"0xde0b6b3a7640000"` || endpointID == "" {
		t.Fatalf("failover failed: result=%s endpoint=%s err=%v", result, endpointID, err)
	}
	if primaryCalls.Load() != 2 || backupCalls.Load() != 1 {
		t.Fatalf("unexpected calls primary=%d backup=%d", primaryCalls.Load(), backupCalls.Load())
	}
}

func TestEndpointAIMDIsolationAfterRateLimit(t *testing.T) {
	var rateLimitedCalls atomic.Int64
	rateLimited := rpcServer(t, func(method string) (int, any) {
		if method == "eth_chainId" || method == "eth_blockNumber" {
			return http.StatusOK, rpcResult(method)
		}
		rateLimitedCalls.Add(1)
		return http.StatusTooManyRequests, map[string]string{"error": "rate limit"}
	})
	defer rateLimited.Close()
	backupB := rpcServer(t, func(method string) (int, any) { return http.StatusOK, rpcResult(method) })
	defer backupB.Close()
	backupC := rpcServer(t, func(method string) (int, any) { return http.StatusOK, rpcResult(method) })
	defer backupC.Close()

	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ids := make([]string, 0, 3)
	for index, server := range []*httptest.Server{rateLimited, backupB, backupC} {
		input := endpointInput(fmt.Sprintf("AIMD 节点 %d", index+1), server.URL)
		input.Priority = (index + 1) * 10
		item, createErr := manager.Create(context.Background(), input)
		if createErr != nil {
			t.Fatal(createErr)
		}
		ids = append(ids, item.ID)
	}
	if _, endpointID, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err != nil || endpointID != ids[1] {
		t.Fatalf("expected B failover, endpoint=%s err=%v", endpointID, err)
	}
	if rateLimitedCalls.Load() != 2 {
		t.Fatalf("expected two bounded attempts on A, calls=%d", rateLimitedCalls.Load())
	}
	snapshot, err := manager.PoolSnapshot("bsc")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EndpointCount != 3 || len(snapshot.Endpoints) != 3 {
		t.Fatalf("unexpected pool size: %+v", snapshot)
	}
	if snapshot.TodayRequests < 3 {
		t.Fatalf("persisted daily request usage missing: %+v", snapshot)
	}
	byID := make(map[string]EndpointPoolSnapshot, len(snapshot.Endpoints))
	for _, endpoint := range snapshot.Endpoints {
		byID[endpoint.EndpointID] = endpoint
	}
	if byID[ids[0]].WorkerLimit >= 4 || byID[ids[0]].CurrentRPS >= 100 || byID[ids[0]].RateLimitedCount != 2 || byID[ids[0]].Rate429 <= 0 {
		t.Fatalf("A did not multiplicatively decrease: %+v", byID[ids[0]])
	}
	if byID[ids[1]].WorkerLimit != 4 || byID[ids[2]].WorkerLimit != 4 {
		t.Fatalf("AIMD leaked across endpoints: B=%+v C=%+v", byID[ids[1]], byID[ids[2]])
	}
}

func TestLatencySpikeAIMDDecreaseAndAdditiveRecovery(t *testing.T) {
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	item := endpointRecord{Endpoint: Endpoint{
		ID: "latency-aimd", ChainID: 56, MaxRPS: 8, MaxConcurrency: 8, RequestTimeoutMS: 1000,
	}}
	runtime := &endpointRuntime{
		currentRPS: 8, maxWorkers: 8, workerLimit: 8, workerNotify: make(chan struct{}),
		latencies: []float64{50, 55, 60}, outcomes: []bool{true, true, true},
	}
	manager.onSuccess(item, runtime, "eth_getBalance", 900*time.Millisecond)
	if runtime.workerLimit != 4 || runtime.currentRPS != 4 {
		t.Fatalf("latency spike did not halve endpoint AIMD: workers=%d rps=%v", runtime.workerLimit, runtime.currentRPS)
	}
	manager.onSuccess(item, runtime, "eth_getBalance", 50*time.Millisecond)
	if runtime.workerLimit != 5 || runtime.currentRPS <= 4 || runtime.currentRPS >= 4.2 {
		t.Fatalf("stable success did not additively recover: workers=%d rps=%v", runtime.workerLimit, runtime.currentRPS)
	}
}

func TestMethodCapabilityRouting(t *testing.T) {
	var balanceCalls, archiveCalls, traceCalls atomic.Int64
	balance := rpcServer(t, func(method string) (int, any) {
		if method != "eth_chainId" && method != "eth_blockNumber" {
			balanceCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer balance.Close()
	archive := rpcServer(t, func(method string) (int, any) {
		if method != "eth_chainId" && method != "eth_blockNumber" {
			archiveCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer archive.Close()
	trace := rpcServer(t, func(method string) (int, any) {
		if method != "eth_chainId" && method != "eth_blockNumber" {
			traceCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer trace.Close()

	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	inputs := []EndpointInput{
		endpointInput("balance-only", balance.URL),
		endpointInput("archive", archive.URL),
		endpointInput("trace", trace.URL),
	}
	inputs[0].SupportedMethods = []string{"eth_getBalance"}
	inputs[1].SupportedMethods, inputs[1].ArchiveCapability = []string{"eth_getLogs"}, true
	inputs[2].SupportedMethods, inputs[2].TraceCapability = []string{"debug_traceTransaction"}, true
	for index := range inputs {
		inputs[index].Priority = (index + 1) * 10
		if _, err := manager.Create(context.Background(), inputs[index]); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := manager.Call(context.Background(), "bsc", "eth_getLogs", []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x2"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CallTurbo(context.Background(), "bsc", "debug_traceTransaction", []any{"0xabc"}); err != nil {
		t.Fatal(err)
	}
	if balanceCalls.Load() != 0 || archiveCalls.Load() != 1 || traceCalls.Load() != 1 {
		t.Fatalf("capability route mismatch balance=%d archive=%d trace=%d", balanceCalls.Load(), archiveCalls.Load(), traceCalls.Load())
	}
}

func TestLegacyDatabaseMigratesAndKeepsCommonMethodRouting(t *testing.T) {
	var balanceCalls atomic.Int64
	server := rpcServer(t, func(method string) (int, any) {
		if method == "eth_getBalance" {
			balanceCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()
	root := testRoot(t)
	secure, err := openSecureStore(filepath.Join(root, "config", "secure"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := secure.encrypt(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "config", "rpc_control.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE rpc_endpoints (
endpoint_id TEXT PRIMARY KEY, provider TEXT NOT NULL, chain_key TEXT NOT NULL, chain_id INTEGER NOT NULL,
display_name TEXT NOT NULL, endpoint_host TEXT NOT NULL, endpoint_encrypted BLOB NOT NULL,
test_endpoint_encrypted BLOB, priority INTEGER NOT NULL, enabled INTEGER NOT NULL, max_rps REAL NOT NULL,
max_concurrency INTEGER NOT NULL, request_timeout_ms INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO rpc_endpoints VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-rpc", "CUSTOM", "bsc", 56, "legacy", "127.0.0.1", encrypted, nil,
		10, 1, 100, 4, 1500, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	manager, err := New(root)
	if err != nil {
		t.Fatalf("legacy migration failed: %v", err)
	}
	defer manager.Close()
	items, err := manager.Endpoints()
	if err != nil || len(items) != 1 || len(items[0].SupportedMethods) != 0 {
		t.Fatalf("legacy endpoint changed during migration: items=%+v err=%v", items, err)
	}
	if _, endpointID, err := manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"}); err != nil || endpointID != "legacy-rpc" {
		t.Fatalf("legacy common method route failed endpoint=%s err=%v", endpointID, err)
	}
	if balanceCalls.Load() != 1 {
		t.Fatalf("legacy endpoint was not called, calls=%d", balanceCalls.Load())
	}
	snapshot, err := manager.PoolSnapshot("bsc")
	if err != nil || len(snapshot.Endpoints) != 1 || !snapshot.Endpoints[0].LegacyCompatibility {
		t.Fatalf("legacy compatibility missing from snapshot: %+v err=%v", snapshot, err)
	}
}

func TestAddressCacheAvoidsDuplicateRPC(t *testing.T) {
	var enrichmentCalls atomic.Int64
	server := rpcServer(t, func(method string) (int, any) {
		if method == "eth_getCode" || method == "eth_getBalance" {
			enrichmentCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer server.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Create(context.Background(), endpointInput("缓存节点", server.URL)); err != nil {
		t.Fatal(err)
	}
	address := "0x0000000000000000000000000000000000000001"
	first, err := manager.Address(context.Background(), "bsc", address, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Address(context.Background(), "bsc", address, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || !second.Cached || enrichmentCalls.Load() != 2 {
		t.Fatalf("cache mismatch first=%v second=%v calls=%d", first.Cached, second.Cached, enrichmentCalls.Load())
	}
}

func TestTimeoutFallsBackToBackup(t *testing.T) {
	primary := rpcServer(t, func(method string) (int, any) {
		if method == "eth_getBalance" {
			time.Sleep(1300 * time.Millisecond)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer primary.Close()
	var backupCalls atomic.Int64
	backup := rpcServer(t, func(method string) (int, any) {
		if method == "eth_getBalance" {
			backupCalls.Add(1)
		}
		return http.StatusOK, rpcResult(method)
	})
	defer backup.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first := endpointInput("超时主节点", primary.URL)
	first.RequestTimeoutMS = 1000
	second := endpointInput("正常备用节点", backup.URL)
	second.Priority = 20
	firstItem, err := manager.Create(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	_, _, err = manager.Call(context.Background(), "bsc", "eth_getBalance", []any{"0x0000000000000000000000000000000000000000", "latest"})
	if err != nil || backupCalls.Load() != 1 {
		t.Fatalf("timeout failover failed: backup=%d err=%v", backupCalls.Load(), err)
	}
	snapshot, err := manager.PoolSnapshot("bsc")
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range snapshot.Endpoints {
		if endpoint.EndpointID == firstItem.ID && (endpoint.WorkerLimit >= first.MaxConcurrency || endpoint.TimeoutRate <= 0) {
			t.Fatalf("timeout did not reduce endpoint AIMD: %+v", endpoint)
		}
	}
}

func TestBadCredentialIsRejected(t *testing.T) {
	server := rpcServer(t, func(string) (int, any) {
		return http.StatusUnauthorized, map[string]string{"error": "invalid api key"}
	})
	defer server.Close()
	manager, err := New(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, err = manager.Create(context.Background(), endpointInput("无效密钥", server.URL+"/private-key"))
	if err == nil || !strings.Contains(err.Error(), "AUTH") || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("expected redacted auth rejection, got %v", err)
	}
}

func TestRejectsSystemDriveRoot(t *testing.T) {
	if _, err := New(`C:\rpc-control`); err == nil || !strings.Contains(err.Error(), "系统盘") {
		t.Fatalf("expected system drive rejection, got %v", err)
	}
}

func endpointInput(name, endpoint string) EndpointInput {
	return EndpointInput{
		Provider: "CUSTOM", ChainKey: "bsc", DisplayName: name, EndpointURL: endpoint,
		Priority: 10, Enabled: true, MaxRPS: 100, MaxConcurrency: 4, RequestTimeoutMS: 1500,
	}
}

func rpcServer(t *testing.T, responder func(method string) (int, any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload rpcWireRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		status, result := responder(payload.Method)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		if status >= 200 && status < 300 {
			_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
		} else {
			_ = json.NewEncoder(writer).Encode(result)
		}
	}))
}

func rpcResult(method string) any {
	switch method {
	case "eth_chainId":
		return "0x38"
	case "eth_blockNumber":
		return "0x1234"
	case "eth_getCode":
		return "0x"
	case "eth_getBalance":
		return "0xde0b6b3a7640000"
	default:
		return "0x1"
	}
}

func testRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(`E:\codex\bsc_analytics\validation`, fmt.Sprintf("rpcmanager-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !strings.HasPrefix(strings.ToLower(root), strings.ToLower(`E:\codex\bsc_analytics\validation\rpcmanager-`)) {
			t.Errorf("unsafe test cleanup path: %s", root)
			return
		}
		_ = os.RemoveAll(root)
	})
	return root
}
