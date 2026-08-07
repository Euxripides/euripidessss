package s3store

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStoreRoundtrip(t *testing.T) {
	root := t.TempDir()
	s := NewLocalStore(root)
	ctx := context.Background()
	key := "sqd-cloud/bsc/jobs/job1/chunk1/request.json"
	if err := s.Put(ctx, key, []byte(`{"job_id":"job1"}`)); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Exists(ctx, key)
	if err != nil || !ok {
		t.Fatalf("exists = %v, %v", ok, err)
	}
	body, err := s.Get(ctx, key)
	if err != nil || string(body) != `{"job_id":"job1"}` {
		t.Fatalf("get = %q, %v", body, err)
	}
	list, err := s.List(ctx, "sqd-cloud/bsc/jobs/")
	if err != nil || len(list) != 1 || list[0].Key != key {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	ok, _ = s.Exists(ctx, key)
	if ok {
		t.Fatal("delete did not remove object")
	}
}

func TestLocalStorePathTraversal(t *testing.T) {
	s := NewLocalStore(t.TempDir())
	if err := s.Put(context.Background(), "../../escape.txt", []byte("x")); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	if _, err := s.Get(context.Background(), "../../escape.txt"); err == nil {
		t.Fatal("path traversal read must be rejected")
	}
}

func TestLocalStoreListMissingPrefix(t *testing.T) {
	s := NewLocalStore(filepath.Join(t.TempDir(), "empty"))
	_ = os.MkdirAll(s.root, 0o755)
	list, err := s.List(context.Background(), "none/")
	if err != nil || len(list) != 0 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestCanonicalQueryString(t *testing.T) {
	got := canonicalQueryString("list-type=2&prefix=abc&prefix=xyz")
	want := "list-type=2&prefix=abc&prefix=xyz"
	if got != want {
		t.Fatalf("canonical query = %q, want %q", got, want)
	}
}

func TestS3StoreSigningShape(t *testing.T) {
	s := NewS3Store(Config{
		Endpoint: "https://example.r2.cloudflarestorage.com", Bucket: "test",
		AccessKey: "ak", SecretKey: "sk", Region: "auto",
	})
	req, err := s.newRequest(context.Background(), http.MethodGet, "sqd-cloud/a b/c.json", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") == "" {
		t.Fatal("missing authorization header")
	}
	if req.URL.EscapedPath() != "/test/sqd-cloud/a%20b/c.json" {
		t.Fatalf("unexpected path: %s", req.URL.EscapedPath())
	}
}
