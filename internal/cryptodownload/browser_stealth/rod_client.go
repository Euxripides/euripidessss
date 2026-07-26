package browserstealth

// RodClient creates and manages a headless Chrome instance via the Rod
// library (github.com/go-rod/rod).
//
// Usage (requires `go get github.com/go-rod/rod`):
//
//	client := NewRodClient()
//	defer client.Close()
//	page := client.MustPage("https://example.com")
//
// This stub documents the intended interface; actual implementation requires
// the Rod dependency.
type RodClient struct {
	// browser is the underlying Rod browser connection.
	// browser *rod.Browser
}

// NewRodClient launches a headless Chrome instance with standard server-side
// flags: --no-sandbox, --disable-gpu, --disable-blink-features=AutomationControlled.
func NewRodClient() *RodClient {
	return &RodClient{}
}

// Close releases the browser process and all associated pages.
func (c *RodClient) Close() error {
	return nil
}
