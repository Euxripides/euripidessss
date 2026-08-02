package analyticsapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── 安全回归：/analytics/address/{address}/... 路由注入防护（2026-08-02）──
//
// 路由层在调用 Service 之前对 address 做 validEVMLikeAddress 校验，
// 脏地址必须返回 400，不允许进入 profileSQL/Flows/Path 的 SQL 拼接。

func TestAddressRouteRejectsInvalidAddress(t *testing.T) {
	h := &Handler{} // 校验发生在 service 调用之前，无需真实 service/engine
	payloads := []string{
		// SQL 注入 payload（URL 编码后的单引号/空格）
		"0x%27%20OR%20%271%27%3D%271",
		"0x%27%3B%20DROP%20TABLE%20x--",
		// 格式非法
		"0x1234",
		"not-an-address",
		"0xzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"",
	}
	for _, payload := range payloads {
		for _, sub := range []string{"profile", "flows", "path", "risk"} {
			url := "/analytics/address/" + payload + "/" + sub
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: 期望 400，实际 %d，body=%s", url, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "不是合法的 EVM 地址") {
				t.Fatalf("%s: 响应应包含校验提示，实际 %s", url, rec.Body.String())
			}
		}
	}
}

// 合法地址必须放行到 service 层（Handler 无 service 时不应 panic，也不该 400）。
func TestAddressRouteAllowsValidAddress(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet,
		"/analytics/address/0x000000000000000000000000000000000000dead/profile", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("合法地址不应被 400 拦截：%s", rec.Body.String())
	}
}
