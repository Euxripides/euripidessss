package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/etl/backend/internal/config"
	"github.com/gin-gonic/gin"
)

func TestRequireLocalSystemAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		remoteAddr string
		header     string
		want       bool
		status     int
	}{
		{name: "loopback", remoteAddr: "127.0.0.1:1234", header: "local-console", want: true, status: http.StatusOK},
		{name: "ipv6 loopback", remoteAddr: "[::1]:1234", header: "local-console", want: true, status: http.StatusOK},
		{name: "missing marker", remoteAddr: "127.0.0.1:1234", want: false, status: http.StatusPreconditionRequired},
		{name: "remote", remoteAddr: "192.0.2.10:1234", header: "local-console", want: false, status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodPatch, "/api/system/settings", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-System-Settings-Action", tc.header)
			ctx.Request = req
			if got := requireLocalSystemAction(ctx); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if !tc.want && w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
		})
	}
}

func TestCleanupCandidatesAreFixedScopedAndNonDestructive(t *testing.T) {
	root := t.TempDir()
	outputs := filepath.Join(root, "outputs")
	logs := filepath.Join(root, "logs")
	if err := os.MkdirAll(outputs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	oldOutput := filepath.Join(outputs, "old.csv")
	oldLog := filepath.Join(logs, "old.log")
	protectedLog := filepath.Join(logs, "app.log")
	newOutput := filepath.Join(outputs, "new.csv")
	for _, path := range []string{oldOutput, oldLog, protectedLog, newOutput} {
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-72 * time.Hour)
	for _, path := range []string{oldOutput, oldLog, protectedLog} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	previous := cfg
	cfg = &config.Config{OutputDir: outputs, LogDir: logs}
	defer func() { cfg = previous }()
	candidates, _, err := cleanupCandidates(cleanupRequest{Categories: []string{"logs", "outputs"}, OlderThanDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates=%v", candidates)
	}
	for _, path := range []string{oldOutput, oldLog, protectedLog, newOutput} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preview changed %s: %v", path, err)
		}
	}
	first := cleanupPreview(cleanupRequest{Categories: []string{"logs", "outputs"}, OlderThanDays: 1}, candidates, nil)
	second := cleanupPreview(cleanupRequest{Categories: []string{"logs", "outputs"}, OlderThanDays: 1}, candidates, nil)
	if first["preview_id"] != second["preview_id"] {
		t.Fatal("preview id must be deterministic while candidates are unchanged")
	}
}

func TestSettingsBackupIDRejectsTraversal(t *testing.T) {
	for _, id := range []string{"../settings-20260101T000000-deadbeef", "settings-20260101T000000-deadbeef.json", "settings-bad"} {
		if settingsBackupIDRE.MatchString(id) {
			t.Fatalf("unsafe backup id accepted: %q", id)
		}
	}
}
