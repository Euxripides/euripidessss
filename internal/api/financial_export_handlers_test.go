package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/etl/backend/internal/financialanalytics"
	"github.com/etl/backend/internal/financialintegration"
	"github.com/gin-gonic/gin"
)

type financialExportStub struct {
	csv   string
	err   error
	query string
}

func (s *financialExportStub) QueryJSON(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (s *financialExportStub) QueryCSV(_ context.Context, query string) (io.ReadCloser, error) {
	s.query = query
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(strings.NewReader(s.csv)), nil
}

func TestFinancialExportRouteStripsCSVAndHonorsAllWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &financialExportStub{csv: "56,0x1111111111111111111111111111111111111111\n"}
	restoreFinancialExportServices(t, stub)
	router := gin.New()
	registerFinancialV1Routes(router.Group("/api"))

	request := httptest.NewRequest(http.MethodGet, "/api/v2/financial-export/bsc/address/0x1111111111111111111111111111111111111111.csv?window=ALL", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Body.Len() == 0 || !strings.Contains(response.Body.String(), "historical_price") {
		t.Fatalf("export is empty or missing CSV header: %q", response.Body.String())
	}
	if !strings.Contains(stub.query, "a.address='0x1111111111111111111111111111111111111111'") {
		t.Fatalf("route suffix leaked into address filter: %s", stub.query)
	}
	if !strings.Contains(stub.query, "1970-01-01T00:00:00Z") {
		t.Fatalf("ALL window did not export full history: %s", stub.query)
	}
}

func TestFinancialExportQueryFailureReturnsJSONError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &financialExportStub{err: errors.New("clickhouse unavailable")}
	restoreFinancialExportServices(t, stub)
	router := gin.New()
	registerFinancialV1Routes(router.Group("/api"))

	request := httptest.NewRequest(http.MethodGet, "/api/v2/financial-export/bsc/address/0x1111111111111111111111111111111111111111.csv?window=ALL", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Content-Type"), "application/json") || strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("query failure retained download headers: %v", response.Header())
	}
}

func restoreFinancialExportServices(t *testing.T, stub *financialExportStub) {
	t.Helper()
	previousAnalytics, previousExporter := financialAnalyticsV1, financialExporterV1
	financialAnalyticsV1 = financialanalytics.NewRepository(stub)
	financialExporterV1 = financialintegration.NewExporter(stub)
	t.Cleanup(func() {
		financialAnalyticsV1 = previousAnalytics
		financialExporterV1 = previousExporter
	})
}
