package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/etl/backend/internal/pricing"
	"github.com/etl/backend/internal/smartdownload"
)

func TestPriceGapRepairRejectsUnboundedBlockRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/repair", handlePriceGapRepair)
	request := httptest.NewRequest(http.MethodPost, "/repair", strings.NewReader(`{"token":"0x1111111111111111111111111111111111111111","from_block":1,"to_block":5000002,"from":"2026-08-01T00:00:00Z","to":"2026-08-02T00:00:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unbounded repair returned %d: %s", response.Code, response.Body.String())
	}
}

func TestPriceDEXBatchErrorRejectsPartialAsQualityFailure(t *testing.T) {
	got := priceDEXBatchError(smartdownload.BatchPartial, "1 个地址数据不完整")
	if !strings.Contains(got, "PARTIAL") || !strings.Contains(got, "数据不完整") {
		t.Fatalf("partial batch error lost quality context: %q", got)
	}
}

func TestPriceDEXBatchErrorHasSafeFallback(t *testing.T) {
	if got := priceDEXBatchError(smartdownload.BatchFailed, ""); got != "DEX log backfill did not complete" {
		t.Fatalf("unexpected fallback: %q", got)
	}
}

func TestPriceDEXResultErrorRejectsFalseSuccess(t *testing.T) {
	for name, test := range map[string]struct {
		result pricing.RebuildResult
		want   string
	}{
		"no pool":  {result: pricing.RebuildResult{}, want: "no registered pools"},
		"no swaps": {result: pricing.RebuildResult{Pools: 1}, want: "no Swap events"},
		"no bars":  {result: pricing.RebuildResult{Pools: 1, Swaps: 2}, want: "no price bars"},
		"valid":    {result: pricing.RebuildResult{Pools: 1, Swaps: 2, Bars: 1}, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			got := priceDEXResultError(test.result)
			if test.want == "" && got != "" {
				t.Fatalf("valid result rejected: %q", got)
			}
			if test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("result error %q does not contain %q", got, test.want)
			}
		})
	}
}
