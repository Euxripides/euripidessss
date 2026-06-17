package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etl/backend/internal/config"
)

func TestNewRouterReturns404ForUnknownAPIRoute(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html></html>"), 0644); err != nil {
		t.Fatalf("write index fixture: %v", err)
	}
	previousConfig := cfg
	cfg = &config.Config{RootDir: t.TempDir(), FrontendDistDir: staticDir}
	t.Cleanup(func() { cfg = previousConfig })

	router := NewRouter()
	request := httptest.NewRequest(http.MethodPost, "/api/unknown-route", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown API route, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "api route not found") {
		t.Fatalf("expected API 404 body, got %q", response.Body.String())
	}
}
