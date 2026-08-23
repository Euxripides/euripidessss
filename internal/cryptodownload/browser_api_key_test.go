package cryptodownload

import (
	"encoding/base64"
	"strings"
	"testing"
)

// decodeBrowserXAPIKey decodes the x-apiKey header back to its
// rotated-key|timestamp payload for assertions.
func decodeBrowserXAPIKey(encoded string) string {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	return string(raw)
}

func TestBrowserXAPIKeyUsesHardcodedByDefault(t *testing.T) {
	t.Setenv("OKLINK_API_KEY", "")
	payload := decodeBrowserXAPIKey(browserXAPIKey())
	wantRotated := browserPublicAPIKey[8:] + browserPublicAPIKey[:8]
	if !strings.HasPrefix(payload, wantRotated+"|") {
		t.Fatalf("x-apiKey payload %q does not start with rotated default key %q", payload, wantRotated)
	}
}

func TestBrowserXAPIKeyOverriddenByEnv(t *testing.T) {
	const customKey = "my-own-oklink-api-key-123456"
	t.Setenv("OKLINK_API_KEY", customKey)
	payload := decodeBrowserXAPIKey(browserXAPIKey())
	wantRotated := customKey[8:] + customKey[:8]
	if !strings.HasPrefix(payload, wantRotated+"|") {
		t.Fatalf("x-apiKey payload %q does not start with rotated custom key %q", payload, wantRotated)
	}
}

func TestBrowserXAPIKeyIgnoresShortEnvKey(t *testing.T) {
	t.Setenv("OKLINK_API_KEY", "short")
	payload := decodeBrowserXAPIKey(browserXAPIKey())
	wantRotated := browserPublicAPIKey[8:] + browserPublicAPIKey[:8]
	if !strings.HasPrefix(payload, wantRotated+"|") {
		t.Fatalf("x-apiKey payload %q should fall back to the hardcoded key %q", payload, wantRotated)
	}
}
