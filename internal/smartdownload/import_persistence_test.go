package smartdownload

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportFilePersistsAddressAssets(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, Options{}, NewJSONLPartWriter(root))
	called := false
	handler := NewHandler(service, nil, func(_ context.Context, chainKey, sourceName string, addresses []string) (int, error) {
		called = true
		if chainKey != "eth" || sourceName != "addresses.txt" || len(addresses) != 2 {
			t.Fatalf("unexpected persistence input chain=%s source=%s addresses=%v", chainKey, sourceName, addresses)
		}
		return len(addresses), nil
	})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chain_key", "eth"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "addresses.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(part, strings.NewReader("address\n0x0000000000000000000000000000000000000011\n0x0000000000000000000000000000000000000012\n"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"persisted":2`) || !strings.Contains(response.Body.String(), `"chain_key":"eth"`) {
		t.Fatalf("missing persistence evidence: %s", response.Body.String())
	}
}
