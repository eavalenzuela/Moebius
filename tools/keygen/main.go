// Command keygen generates an Ed25519 keypair for release signing.
//
// Usage:
//
//	go run ./tools/keygen -out-pub keys/release.pub -out-priv release.key
//
// The private key is written as raw 64 bytes. For use in GitHub Actions,
// base64-encode it and store as the RELEASE_SIGNING_KEY secret:
//
//	base64 < release.key | tr -d '\n'
//
// The public key is written as base64-encoded 32 bytes suitable for
// committing to the repository and registering on the management server.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	outPub := flag.String("out-pub", "keys/release.pub", "path to write public key (base64)")
	outPriv := flag.String("out-priv", "release.key", "path to write private key (raw bytes)")
	flag.Parse()

	if err := run(*outPub, *outPriv); err != nil {
		fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
		os.Exit(1)
	}
}

func run(outPub, outPriv string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}

	// Write private key as raw bytes
	if dir := filepath.Dir(outPriv); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}
	if err := os.WriteFile(outPriv, []byte(priv), 0o600); err != nil {
		return fmt.Errorf("writing private key: %w", err)
	}
	fmt.Printf("Private key written to: %s\n", outPriv)
	fmt.Println("  For GitHub Actions, base64-encode and set as RELEASE_SIGNING_KEY secret:")
	fmt.Printf("    base64 < %s | tr -d '\\n'\n", outPriv)

	// Write public key as base64
	if dir := filepath.Dir(outPub); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(outPub, []byte(encoded+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing public key: %w", err)
	}
	fmt.Printf("Public key written to:  %s\n", outPub)
	fmt.Println("  Commit this file to the repository and register on the management server.")

	return nil
}
