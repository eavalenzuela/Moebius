// Package storage provides a pluggable file storage backend.
// The "local" backend stores files on disk. An S3 backend can be added later.
package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Backend abstracts file storage operations.
type Backend interface {
	// Save writes data to the given key. Returns the storage path.
	Save(ctx context.Context, key string, r io.Reader) (string, error)

	// Open returns a reader for the given key.
	Open(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the stored file.
	Delete(ctx context.Context, key string) error

	// URL returns a download URL for the given key.
	// For local backends, this returns a server-relative path.
	// For S3, this would return a pre-signed URL.
	URL(ctx context.Context, key string) (string, error)

	// Name returns the backend name ("local" or "s3").
	Name() string
}

// LocalBackend stores files on the local filesystem.
type LocalBackend struct {
	baseDir string
}

// NewLocalBackend creates a local filesystem storage backend.
func NewLocalBackend(baseDir string) (*LocalBackend, error) {
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &LocalBackend{baseDir: baseDir}, nil
}

func (b *LocalBackend) Name() string { return "local" }

func (b *LocalBackend) Save(_ context.Context, key string, r io.Reader) (string, error) {
	path := b.path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.Create(path) //nolint:gosec // server-controlled path
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, r); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write file: %w", err)
	}

	return path, nil
}

func (b *LocalBackend) Open(_ context.Context, key string) (io.ReadCloser, error) {
	f, err := os.Open(b.path(key)) //nolint:gosec // server-controlled path
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	return f, nil
}

func (b *LocalBackend) Delete(_ context.Context, key string) error {
	err := os.Remove(b.path(key))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

func (b *LocalBackend) URL(_ context.Context, key string) (string, error) {
	// For local backend, URL is a server-relative path.
	// The server will serve the file directly.
	return "/v1/files/data/" + key, nil
}

func (b *LocalBackend) path(key string) string {
	return filepath.Join(b.baseDir, filepath.FromSlash(key))
}

// BaseDir returns the base directory for testing.
func (b *LocalBackend) BaseDir() string {
	return b.baseDir
}
