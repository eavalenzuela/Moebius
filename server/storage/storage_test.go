package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalBackend_SaveOpenDelete(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	content := "hello world"

	// Save
	path, err := b.Save(ctx, "test-file", strings.NewReader(content))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Open
	rc, err := b.Open(ctx, "test-file")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(data) != content {
		t.Errorf("got %q, want %q", string(data), content)
	}

	// Delete
	if err := b.Delete(ctx, "test-file"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Open after delete should fail
	_, err = b.Open(ctx, "test-file")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestLocalBackend_URL(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}

	url, err := b.URL(context.Background(), "my-file-id")
	if err != nil {
		t.Fatal(err)
	}
	if url != "/v1/files/data/my-file-id" {
		t.Errorf("URL = %q, want /v1/files/data/my-file-id", url)
	}
}

func TestLocalBackend_Name(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name() != "local" {
		t.Errorf("Name = %q, want local", b.Name())
	}
}

func TestLocalBackend_DeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should not error for non-existent file
	if err := b.Delete(context.Background(), "does-not-exist"); err != nil {
		t.Errorf("Delete non-existent: %v", err)
	}
}

func TestLocalBackend_SaveCreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.Save(context.Background(), "sub/dir/file", strings.NewReader("data"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	info, err := os.Stat(filepath.Join(dir, "sub", "dir", "file"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Size() != 4 {
		t.Errorf("size = %d, want 4", info.Size())
	}
}
