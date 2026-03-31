package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eavalenzuela/Moebius/shared/models"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSendWebhook(t *testing.T) {
	var received AlertPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(nil, testLogger())

	rule := &models.AlertRule{
		ID:       "alr_test",
		TenantID: "ten_test",
		Name:     "Test Rule",
		Channels: &models.AlertChannels{
			Webhooks: []string{srv.URL},
		},
	}

	n.Send(context.Background(), rule, "agent_offline", "2 devices offline")

	if received.AlertRuleID != "alr_test" {
		t.Errorf("AlertRuleID = %q, want %q", received.AlertRuleID, "alr_test")
	}
	if received.ConditionType != "agent_offline" {
		t.Errorf("ConditionType = %q, want %q", received.ConditionType, "agent_offline")
	}
	if received.Message != "2 devices offline" {
		t.Errorf("Message = %q, want %q", received.Message, "2 devices offline")
	}
}

func TestSendNilChannels(t *testing.T) {
	n := New(nil, testLogger())
	rule := &models.AlertRule{
		ID:       "alr_test",
		Channels: nil,
	}
	// Should not panic
	n.Send(context.Background(), rule, "agent_offline", "test")
}

func TestSendEmailSkippedWithoutSMTP(t *testing.T) {
	n := New(nil, testLogger())
	rule := &models.AlertRule{
		ID:       "alr_test",
		TenantID: "ten_test",
		Name:     "Test Rule",
		Channels: &models.AlertChannels{
			Emails: []string{"test@example.com"},
		},
	}
	// Should not panic, just logs a warning
	n.Send(context.Background(), rule, "agent_offline", "test")
}

func TestWebhookNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := New(nil, testLogger())
	rule := &models.AlertRule{
		ID:       "alr_test",
		TenantID: "ten_test",
		Name:     "Test Rule",
		Channels: &models.AlertChannels{
			Webhooks: []string{srv.URL},
		},
	}
	// Should not panic, just logs a warning
	n.Send(context.Background(), rule, "agent_offline", "test")
}
