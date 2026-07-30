package writer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONAtomicReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := WriteJSONAtomic(path, map[string]any{"status": "running"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONAtomic(path, map[string]any{"status": "done"}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	if value["status"] != "done" {
		t.Fatalf("unexpected atomic content: %s", content)
	}
	matches, err := filepath.Glob(path + ".*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files were not cleaned: %v", matches)
	}
}

func TestComputeChecksums(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(path, []byte("stable-output"), 0644); err != nil {
		t.Fatal(err)
	}
	items, err := ComputeChecksums([]string{path, path})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Algorithm != "SHA256" ||
		items[0].Value != "b5aa61a59c9c75b907251835055f18cb96ff0d4a6f816e7aaab35458cc3cbe8f" {
		t.Fatalf("unexpected checksums: %+v", items)
	}
}
