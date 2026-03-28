package version

import (
	"strings"
	"testing"
)

func TestFullVersion_Default(t *testing.T) {
	v := FullVersion()
	if !strings.Contains(v, "dev") {
		t.Errorf("expected default version to contain 'dev', got %q", v)
	}
	if !strings.Contains(v, "unknown") {
		t.Errorf("expected default version to contain 'unknown', got %q", v)
	}
}
