package cryptodownload

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCSVBrowserEmailHelperProcess(t *testing.T) {
	mode := ""
	for index, argument := range os.Args {
		if argument == "--" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
			break
		}
	}
	if mode == "" {
		return
	}
	_, _ = os.Stdin.Read(make([]byte, 4096))
	switch mode {
	case "success":
		_, _ = os.Stdout.WriteString(`{"code":0,"msg":""}`)
	case "business-error":
		_, _ = os.Stdout.WriteString(`{"code":50113,"msg":"owner@example.com 0x1111111111111111111111111111111111111111"}`)
	case "noisy":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), csvBrowserOutputLimit*2))
	case "missing-code":
		_, _ = os.Stdout.WriteString(`{}`)
	}
	os.Exit(0)
}

func TestPythonCSVBrowserEmailRequesterRejectsMissingCode(t *testing.T) {
	original := csvBrowserCommandContext
	t.Cleanup(func() { csvBrowserCommandContext = original })
	csvBrowserCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCSVBrowserEmailHelperProcess$", "--", "missing-code")
	}

	requester := &pythonCSVBrowserEmailRequester{pythonPath: "python", scriptPath: "bridge.py"}
	err := requester.Request(context.Background(), csvBrowserEmailRequest{})
	if err == nil || !strings.Contains(err.Error(), "缺少 code 字段") {
		t.Fatalf("Request() error = %v, want missing-code failure", err)
	}
}

func TestPythonCSVBrowserEmailRequesterUsesStrictJSONProtocol(t *testing.T) {
	original := csvBrowserCommandContext
	t.Cleanup(func() { csvBrowserCommandContext = original })
	csvBrowserCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCSVBrowserEmailHelperProcess$", "--", "success")
	}

	requester := &pythonCSVBrowserEmailRequester{pythonPath: "python", scriptPath: "bridge.py"}
	err := requester.Request(context.Background(), csvBrowserEmailRequest{
		URL:     "https://www.oklink.com/download/explorer/v1/bsc/normalTransaction/download/async?t=1",
		PageURL: "https://www.oklink.com/zh-hans/bsc/address/0x1",
		Body:    `{}`,
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
}

func TestPythonCSVBrowserEmailRequesterRedactsBusinessError(t *testing.T) {
	original := csvBrowserCommandContext
	t.Cleanup(func() { csvBrowserCommandContext = original })
	csvBrowserCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCSVBrowserEmailHelperProcess$", "--", "business-error")
	}

	requester := &pythonCSVBrowserEmailRequester{pythonPath: "python", scriptPath: "bridge.py"}
	err := requester.Request(context.Background(), csvBrowserEmailRequest{})
	if err == nil {
		t.Fatal("Request() error = nil, want business failure")
	}
	message := err.Error()
	if strings.Contains(message, "owner@example.com") || strings.Contains(message, "0x1111111111111111111111111111111111111111") {
		t.Fatalf("Request() leaked sensitive values: %s", message)
	}
	if !strings.Contains(message, "<email>") || !strings.Contains(message, "<evm-address>") {
		t.Fatalf("Request() missing redaction markers: %s", message)
	}
}

func TestCSVBrowserSubprocessEnvironmentExcludesServiceSecrets(t *testing.T) {
	t.Setenv("CSV_IMAP_PASSWORD", "mail-secret")
	t.Setenv("DEEPAML_API_KEY", "aml-secret")
	t.Setenv("PLAYWRIGHT_BROWSERS_PATH", `D:\browser-cache`)
	environment := strings.Join(csvBrowserSubprocessEnv(), "\n")
	if strings.Contains(environment, "mail-secret") || strings.Contains(environment, "aml-secret") {
		t.Fatalf("subprocess environment leaked a service secret: %s", environment)
	}
	if !strings.Contains(environment, `PLAYWRIGHT_BROWSERS_PATH=D:\browser-cache`) {
		t.Fatalf("subprocess environment omitted browser cache: %s", environment)
	}
}

func TestCSVCrawl4AIScriptMaterializationMatchesEmbeddedBytes(t *testing.T) {
	path, err := materializeCSVCrawl4AIScript()
	if err != nil {
		t.Fatalf("materializeCSVCrawl4AIScript() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if !bytes.Equal(got, embeddedCSVCrawl4AIScript) {
		t.Fatal("materialized Crawl4AI bridge does not match embedded bytes")
	}
}

func TestCappedCSVBrowserBufferLimitsStoredOutput(t *testing.T) {
	buffer := &cappedCSVBrowserBuffer{limit: 16}
	input := bytes.Repeat([]byte("a"), 128)
	written, err := buffer.Write(input)
	if err != nil || written != len(input) {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", written, err, len(input))
	}
	if buffer.buffer.Len() != 16 {
		t.Fatalf("stored output = %d bytes, want 16", buffer.buffer.Len())
	}
}

func TestUnknownCSVBrowserEngineFailsClosed(t *testing.T) {
	t.Setenv("OKLINK_CSV_BROWSER_ENGINE", "mystery")
	requester := newCSVBrowserEmailRequester(defaultBaseURL)
	pythonRequester, ok := requester.(*pythonCSVBrowserEmailRequester)
	if !ok || pythonRequester.setupErr == nil {
		t.Fatalf("requester = %#v, want setup failure", requester)
	}
}

func TestCrawl4AIRuntimeProbeWhenExplicitlyEnabled(t *testing.T) {
	if os.Getenv("ETL_TEST_CRAWL4AI") != "1" {
		t.Skip("set ETL_TEST_CRAWL4AI=1 for the installed Patchright runtime smoke test")
	}
	t.Setenv("OKLINK_CSV_BROWSER_ENGINE", "crawl4ai")
	requester := newCSVBrowserEmailRequester(defaultBaseURL)
	pythonRequester, ok := requester.(*pythonCSVBrowserEmailRequester)
	if !ok {
		t.Fatalf("requester = %T, want *pythonCSVBrowserEmailRequester", requester)
	}
	if pythonRequester.setupErr != nil {
		t.Fatalf("Crawl4AI runtime probe failed: %v", pythonRequester.setupErr)
	}
	if pythonRequester.pythonPath == "" || pythonRequester.scriptPath == "" {
		t.Fatalf("Crawl4AI requester paths are incomplete: %#v", pythonRequester)
	}
}
