package logging

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := NewWithWriter(&buf, "json", "info", "moebius-api")

	logger.Info("hello", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}
	if entry["msg"] != "hello" {
		t.Errorf("msg = %v, want %q", entry["msg"], "hello")
	}
	if entry["service"] != "moebius-api" {
		t.Errorf("service = %v, want %q", entry["service"], "moebius-api")
	}
	if _, ok := entry["version"]; !ok {
		t.Error("missing version field")
	}
	if entry["key"] != "value" {
		t.Errorf("key = %v, want %q", entry["key"], "value")
	}
}

func TestNewText(t *testing.T) {
	var buf bytes.Buffer
	logger := NewWithWriter(&buf, "text", "debug", "moebius-worker")

	logger.Debug("test debug")

	out := buf.String()
	if len(out) == 0 {
		t.Fatal("expected output for debug level message")
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := NewWithWriter(&buf, "json", "warn", "test")

	logger.Info("should be filtered")

	if buf.Len() != 0 {
		t.Errorf("expected no output for info message at warn level, got: %s", buf.String())
	}
}
