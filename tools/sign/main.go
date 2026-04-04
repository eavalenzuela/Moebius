// Command sign produces an Ed25519 signature for a file.
// Used by the release pipeline to sign agent artifacts.
//
// Usage:
//
//	go run ./tools/sign -key private.key -file artifact.tar.gz -out artifact.tar.gz.sig
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	keyPath := flag.String("key", "", "path to Ed25519 private key (raw 64-byte seed+key)")
	filePath := flag.String("file", "", "path to file to sign")
	outPath := flag.String("out", "", "path to write base64 signature")
	flag.Parse()

	if *keyPath == "" || *filePath == "" || *outPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	if err := run(*keyPath, *filePath, *outPath); err != nil {
		fmt.Fprintf(os.Stderr, "sign: %v\n", err)
		os.Exit(1)
	}
}

func run(keyPath, filePath, outPath string) error {
	// Read private key (raw 64-byte Ed25519 private key)
	keyPath = filepath.Clean(keyPath)
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid key size: got %d bytes, want %d", len(keyBytes), ed25519.PrivateKeySize)
	}
	privKey := ed25519.PrivateKey(keyBytes)

	// Hash the file with SHA-256
	filePath = filepath.Clean(filePath)
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening file: %w", err)
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		return fmt.Errorf("hashing file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing file: %w", err)
	}
	digest := h.Sum(nil)

	// Sign the hash
	sig := ed25519.Sign(privKey, digest)

	// Write base64-encoded signature
	outPath = filepath.Clean(outPath)
	encoded := base64.StdEncoding.EncodeToString(sig)
	if err := os.WriteFile(outPath, []byte(encoded+"\n"), 0o600); err != nil { //nolint:gosec // CLI tool: output path comes from flag
		return fmt.Errorf("writing signature: %w", err)
	}

	fmt.Printf("Signed %s -> %s\n", filePath, outPath)
	return nil
}
