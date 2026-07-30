package downloader

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func mergeChunks(destination string, paths []string, expectedSize int64) (string, error) {
	partial := destination + ".partial"
	output, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := io.MultiWriter(output, hash)
	var written int64
	for _, path := range paths {
		input, openErr := os.Open(path)
		if openErr != nil {
			output.Close()
			return "", openErr
		}
		count, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			output.Close()
			return "", copyErr
		}
		if closeErr != nil {
			output.Close()
			return "", closeErr
		}
		written += count
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	if written != expectedSize {
		return "", fmt.Errorf("合并文件大小不一致：得到 %d，预期 %d", written, expectedSize)
	}
	_ = os.Remove(destination)
	if err := os.Rename(partial, destination); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
