package cryptodownload

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGUIOutputPathKeepsExcelPathUnderExcelizeLimit(t *testing.T) {
	outputDir := filepath.Join("E:", "codex", "etl", "backend", "data", "crypto_download", "test_exports")
	taskDir := guiTaskOutputDir(outputDir, "real_address_single_block", "1ccbef8ff9e0b2ac")
	path := buildGUIOutputPath(taskDir, "real_address_single_block", "0x5b43453fce04b92e190f391a83136bfbecedefd1", "ETH", 0, "rpc", false)

	if len(path) > 200 {
		t.Fatalf("path is too long for excelize: %d %s", len(path), path)
	}
	if strings.Contains(path, strings.ToLower("0x5b43453fce04b92e190f391a83136bfbecedefd1")) {
		t.Fatalf("path should not include the full address: %s", path)
	}
}
