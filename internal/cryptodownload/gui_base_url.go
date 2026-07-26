package cryptodownload

import (
	"os"
	"strings"
)

func guiBaseURL() string {
	return strings.TrimRight(firstNonEmpty(strings.TrimSpace(os.Getenv("WALLET_EXPORTER_GUI_BASE_URL")), defaultBaseURL), "/")
}
