package api

import (
	"strings"
	"testing"
)

func TestGenerateAPIKey_Format(t *testing.T) {
	key := generateAPIKey()

	if !strings.HasPrefix(key, "sk_") {
		t.Errorf("key %q missing sk_ prefix", key)
	}

	// sk_ (3) + 48 hex chars (24 bytes) = 51 total
	if len(key) != 51 {
		t.Errorf("key length = %d, want 51", len(key))
	}

	// hex portion should be valid hex
	hexPart := key[3:]
	for _, c := range hexPart {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("non-hex character %q in key suffix", c)
		}
	}
}

func TestGenerateAPIKey_Unique(t *testing.T) {
	k1 := generateAPIKey()
	k2 := generateAPIKey()
	if k1 == k2 {
		t.Error("two generated keys should not be equal")
	}
}
