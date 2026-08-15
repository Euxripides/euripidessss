package cryptodownload

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleResultFileServesOnlyJobResult(t *testing.T) {
	tempDir := t.TempDir()
	resultPath := filepath.Join(tempDir, "result.xlsx")
	if err := os.WriteFile(resultPath, []byte("valid workbook placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &GUIManager{jobs: map[string]*GUIJob{
		"job-1": {ID: "job-1", Results: []string{resultPath}},
	}}

	request := httptest.NewRequest(http.MethodGet, "/file?id=job-1&path="+resultPath, nil)
	response := httptest.NewRecorder()
	manager.handleResultFile(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, "result.xlsx") {
		t.Fatalf("missing attachment filename: %q", disposition)
	}
	if response.Body.String() != "valid workbook placeholder" {
		t.Fatalf("unexpected body %q", response.Body.String())
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/file?id=job-1&path="+filepath.Join(tempDir, "secret.txt"), nil)
	unauthorizedResponse := httptest.NewRecorder()
	manager.handleResultFile(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthorized path, got %d", unauthorizedResponse.Code)
	}
}

func TestFinishRunFailsWhenErrorsWereRecorded(t *testing.T) {
	job := &GUIJob{
		Status:  "running",
		Running: true,
		Errors:  []string{"summary ETH: HTTP 403"},
	}
	job.finishRun()
	if job.Status != "failed" {
		t.Fatalf("expected failed status, got %q", job.Status)
	}
	if job.Message != "下载失败：共 1 条错误" {
		t.Fatalf("unexpected message %q", job.Message)
	}
	if job.Running {
		t.Fatal("job should no longer be running")
	}
}

func TestFailAddressWithResultKeepsDiagnosticFileButMarksFailure(t *testing.T) {
	job := &GUIJob{
		Status: "running",
		Total:  1,
		Addresses: []GUIAddressProgress{{
			Index: 0, Status: "running",
			Parts: []GUIAddressDownloadPart{{Key: "BSC|transactions", Status: "running"}},
		}},
	}
	job.failAddressWithResult(0, `C:\exports\001_ETH.xlsx`, "地址下载失败")
	address := job.Addresses[0]
	if address.Status != "failed" {
		t.Fatalf("expected failed address, got %q", address.Status)
	}
	if address.Result == "" || len(job.Results) != 1 {
		t.Fatal("diagnostic result should remain downloadable")
	}
	if address.Parts[0].Status != "failed" {
		t.Fatalf("expected failed part status, got %q", address.Parts[0].Status)
	}
}

func TestCSVEmailAliasRotatesGmailAliases(t *testing.T) {
	client := &CSVExportClient{mail: CSVMailConfig{Email: "user@gmail.com"}}
	first := client.emailExportAlias("bsc", "transactions")
	second := client.emailExportAlias("bsc", "transactions")
	if first == second {
		t.Fatalf("expected rotated aliases, got identical %q", first)
	}
	for _, alias := range []string{first, second} {
		if !strings.HasPrefix(alias, "user+okl") || !strings.HasSuffix(alias, "@gmail.com") {
			t.Fatalf("unexpected alias format %q", alias)
		}
	}
}

func TestCSVEmailAliasGooglemailDomainRotates(t *testing.T) {
	client := &CSVExportClient{mail: CSVMailConfig{Email: "user@googlemail.com"}}
	if got := client.emailExportAlias("bsc", "transactions"); !strings.HasSuffix(got, "@googlemail.com") || !strings.Contains(got, "+okl") {
		t.Fatalf("expected googlemail alias, got %q", got)
	}
}

func TestCSVEmailAliasNonGmailUnchanged(t *testing.T) {
	client := &CSVExportClient{mail: CSVMailConfig{Email: "user@example.com"}}
	if got := client.emailExportAlias("bsc", "transactions"); got != "user@example.com" {
		t.Fatalf("expected configured email unchanged, got %q", got)
	}
}
