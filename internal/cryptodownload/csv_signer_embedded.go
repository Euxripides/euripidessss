package cryptodownload

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed tools/oklink_csv_signer.mjs tools/oklink_signer_runtime.mjs tools/oklink_signer_discovery.mjs tools/oklink_signer_executor.mjs tools/oklink_signer_worker.mjs
var embeddedCSVSignerRuntime embed.FS

var embeddedCSVSignerFiles = []string{
	"oklink_csv_signer.mjs",
	"oklink_signer_runtime.mjs",
	"oklink_signer_discovery.mjs",
	"oklink_signer_executor.mjs",
	"oklink_signer_worker.mjs",
}

func materializeEmbeddedCSVSigner() (string, error) {
	hasher := sha256.New()
	contents := make(map[string][]byte, len(embeddedCSVSignerFiles))
	for _, name := range embeddedCSVSignerFiles {
		data, err := embeddedCSVSignerRuntime.ReadFile("tools/" + name)
		if err != nil {
			return "", fmt.Errorf("读取内嵌 CSV signer %s: %w", name, err)
		}
		contents[name] = data
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write(data)
	}
	fingerprint := fmt.Sprintf("%x", hasher.Sum(nil)[:12])
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("定位 CSV signer 缓存目录: %w", err)
	}
	dir := filepath.Join(cacheRoot, "wallet-exporter", "signer-runtime", fingerprint)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建 CSV signer 缓存目录: %w", err)
	}
	for _, name := range embeddedCSVSignerFiles {
		target := filepath.Join(dir, name)
		if current, readErr := os.ReadFile(target); readErr == nil && string(current) == string(contents[name]) {
			continue
		}
		temp, err := os.CreateTemp(dir, ".signer-*.tmp")
		if err != nil {
			return "", err
		}
		tempName := temp.Name()
		if err := temp.Chmod(0o600); err == nil {
			_, err = temp.Write(contents[name])
		}
		closeErr := temp.Close()
		if err != nil || closeErr != nil {
			_ = os.Remove(tempName)
			if err != nil {
				return "", err
			}
			return "", closeErr
		}
		if err := os.Rename(tempName, target); err != nil {
			_ = os.Remove(tempName)
			return "", fmt.Errorf("发布 CSV signer %s: %w", name, err)
		}
	}
	return filepath.Join(dir, "oklink_csv_signer.mjs"), nil
}
