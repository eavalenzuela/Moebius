package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/eavalenzuela/Moebius/shared/models"
)

// SMTPConfig holds SMTP settings for email notifications.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Notifier dispatches alerts via configured channels.
type Notifier struct {
	smtp       *SMTPConfig
	httpClient *http.Client
	log        *slog.Logger
}

// New creates a Notifier. smtp may be nil if email is not configured.
func New(smtpCfg *SMTPConfig, log *slog.Logger) *Notifier {
	return &Notifier{
		smtp: smtpCfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		log: log,
	}
}

// AlertPayload is the JSON body posted to webhook URLs.
type AlertPayload struct {
	AlertRuleID   string `json:"alert_rule_id"`
	AlertRuleName string `json:"alert_rule_name"`
	TenantID      string `json:"tenant_id"`
	ConditionType string `json:"condition_type"`
	Message       string `json:"message"`
	FiredAt       string `json:"fired_at"`
}

// Send dispatches an alert notification to all configured channels.
func (n *Notifier) Send(ctx context.Context, rule *models.AlertRule, condType, message string) {
	if rule.Channels == nil {
		return
	}

	payload := AlertPayload{
		AlertRuleID:   rule.ID,
		AlertRuleName: rule.Name,
		TenantID:      rule.TenantID,
		ConditionType: condType,
		Message:       message,
		FiredAt:       time.Now().UTC().Format(time.RFC3339),
	}

	for _, url := range rule.Channels.Webhooks {
		n.sendWebhook(ctx, url, payload)
	}

	for _, addr := range rule.Channels.Emails {
		n.sendEmail(addr, payload)
	}
}

func (n *Notifier) sendWebhook(ctx context.Context, url string, payload AlertPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		n.log.Error("marshal webhook payload", slog.String("error", err.Error()))
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		n.log.Error("create webhook request", slog.String("url", url), slog.String("error", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		n.log.Warn("webhook delivery failed", slog.String("url", url), slog.String("error", err.Error()))
		return
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 300 {
		n.log.Warn("webhook non-2xx response", slog.String("url", url), slog.Int("status", resp.StatusCode))
	} else {
		n.log.Debug("webhook delivered", slog.String("url", url), slog.String("rule_id", payload.AlertRuleID))
	}
}

func (n *Notifier) sendEmail(to string, payload AlertPayload) {
	if n.smtp == nil || n.smtp.Host == "" {
		n.log.Warn("email notification skipped: SMTP not configured", slog.String("to", to))
		return
	}

	subject := fmt.Sprintf("Moebius Alert: %s", payload.AlertRuleName)
	body := fmt.Sprintf("Alert: %s\nCondition: %s\nMessage: %s\nFired at: %s\nTenant: %s",
		payload.AlertRuleName, payload.ConditionType, payload.Message, payload.FiredAt, payload.TenantID)

	msg := strings.Join([]string{
		"From: " + n.smtp.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")

	addr := fmt.Sprintf("%s:%d", n.smtp.Host, n.smtp.Port)
	var auth smtp.Auth
	if n.smtp.Username != "" {
		auth = smtp.PlainAuth("", n.smtp.Username, n.smtp.Password, n.smtp.Host)
	}

	if err := smtp.SendMail(addr, auth, n.smtp.From, []string{to}, []byte(msg)); err != nil {
		n.log.Warn("email delivery failed", slog.String("to", to), slog.String("error", err.Error()))
	} else {
		n.log.Debug("email delivered", slog.String("to", to), slog.String("rule_id", payload.AlertRuleID))
	}
}
