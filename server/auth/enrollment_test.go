package auth

import (
	"testing"
)

func TestHashToken_Deterministic(t *testing.T) {
	h1 := hashToken("test-token-123")
	h2 := hashToken("test-token-123")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
}

func TestHashToken_DifferentInputs(t *testing.T) {
	h1 := hashToken("token-a")
	h2 := hashToken("token-b")
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestGenerateRawToken(t *testing.T) {
	t1, err := generateRawToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t2, err := generateRawToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 32 bytes hex-encoded = 64 chars
	if len(t1) != 64 {
		t.Errorf("token length = %d, want 64", len(t1))
	}
	if t1 == t2 {
		t.Error("tokens should be unique")
	}
}
