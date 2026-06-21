package dunetools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPLinkVerifier struct {
	Client *http.Client
}

func (v HTTPLinkVerifier) VerifyEmailLink(ctx context.Context, link string) error {
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return fmt.Errorf("build verification request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("open verification link: %w", err)
	}
	defer resp.Body.Close()
	finalURL := strings.ToLower(resp.Request.URL.String())
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("verification link returned HTTP %d", resp.StatusCode)
	}
	if strings.Contains(finalURL, "error") {
		return fmt.Errorf("verification link ended at error URL: %s", resp.Request.URL.String())
	}
	return nil
}
