package api

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestValidateEd25519PEM_Valid(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	pub := priv.Public().(ed25519.PublicKey)

	derBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: derBytes})

	fp, err := validateEd25519PEM(string(pemBlock))
	if err != nil {
		t.Fatalf("validateEd25519PEM: %v", err)
	}
	if fp == "" {
		t.Error("expected non-empty fingerprint")
	}
	if len(fp) < 10 {
		t.Errorf("fingerprint too short: %s", fp)
	}
}

func TestValidateEd25519PEM_InvalidPEM(t *testing.T) {
	_, err := validateEd25519PEM("not-pem-data")
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

func TestValidateEd25519PEM_WrongKeyType(t *testing.T) {
	// Create an RSA-looking PEM block that's not Ed25519
	fakePEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("not-a-real-key")})
	_, err := validateEd25519PEM(string(fakePEM))
	if err == nil {
		t.Error("expected error for non-Ed25519 key")
	}
}
