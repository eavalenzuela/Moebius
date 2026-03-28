package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/moebius-oss/moebius/server/pki"
	"github.com/moebius-oss/moebius/shared/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println("moebius-api", version.FullVersion())
			return
		case "migrate":
			fmt.Println("TODO: run database migrations")
			return
		case "generate-ca":
			runGenerateCA()
			return
		case "create-admin":
			fmt.Println("TODO: create initial admin user")
			return
		}
	}

	fmt.Println("moebius-api", version.FullVersion())
	fmt.Println("TODO: start API server")
}

func runGenerateCA() {
	outDir := "keys"
	if len(os.Args) > 2 {
		outDir = os.Args[2]
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil { //nolint:gosec // CLI arg from operator, not user input
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	// Generate Root CA
	fmt.Println("Generating Root CA...")
	rootCertPEM, rootKeyPEM, err := pki.GenerateCA("Moebius Root CA", true, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating root CA: %v\n", err)
		os.Exit(1)
	}
	rootCA, err := pki.ParseCA(rootCertPEM, rootKeyPEM)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing root CA: %v\n", err)
		os.Exit(1)
	}

	// Generate Intermediate CA signed by Root
	fmt.Println("Generating Intermediate CA...")
	intCertPEM, intKeyPEM, err := pki.GenerateCA("Moebius Intermediate CA", false, rootCA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating intermediate CA: %v\n", err)
		os.Exit(1)
	}

	// Write files
	files := map[string][]byte{
		"root-ca.crt":         rootCertPEM,
		"root-ca.key":         rootKeyPEM,
		"intermediate-ca.crt": intCertPEM,
		"intermediate-ca.key": intKeyPEM,
	}
	for name, data := range files {
		path := filepath.Join(outDir, name)
		perm := os.FileMode(0o644)
		if filepath.Ext(name) == ".key" {
			perm = 0o600
		}
		if err := os.WriteFile(path, data, perm); err != nil { //nolint:gosec // CLI arg from operator, not user input
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("  wrote %s\n", path)
	}

	fmt.Println("Done. Set CA_CERT_PATH and CA_KEY_PATH to the intermediate CA files.")
}
