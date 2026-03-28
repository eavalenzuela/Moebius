package models

import (
	"strings"
	"testing"
)

func TestNewID_Prefix(t *testing.T) {
	tests := []struct {
		fn     func() string
		prefix string
	}{
		{NewTenantID, "ten_"},
		{NewDeviceID, "dev_"},
		{NewJobID, "job_"},
		{NewUserID, "usr_"},
		{NewFileID, "fil_"},
		{NewEnrollmentTokenID, "enr_"},
	}
	for _, tt := range tests {
		id := tt.fn()
		if !strings.HasPrefix(id, tt.prefix) {
			t.Errorf("expected prefix %q, got %q", tt.prefix, id)
		}
		// prefix + 16 hex chars
		if len(id) != len(tt.prefix)+16 {
			t.Errorf("expected length %d, got %d for %q", len(tt.prefix)+16, len(id), id)
		}
	}
}

func TestNewID_Unique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := NewDeviceID()
		if seen[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestValidPrefix(t *testing.T) {
	id := NewDeviceID()
	if !ValidPrefix(id, "dev") {
		t.Errorf("expected valid prefix 'dev' for %q", id)
	}
	if ValidPrefix(id, "job") {
		t.Errorf("expected invalid prefix 'job' for %q", id)
	}
}
