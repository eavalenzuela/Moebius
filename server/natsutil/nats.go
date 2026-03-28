package natsutil

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Stream and subject constants.
const (
	StreamJobs    = "jobs"
	StreamResults = "results"
	StreamLogs    = "logs"

	// Subject patterns — use SubjectJobDispatch / SubjectResult / SubjectLog
	// helpers to fill in tenant and device/job IDs.
	subjectJobDispatchPattern = "jobs.dispatch.%s.%s" // tenant_id, device_id
	subjectResultPattern      = "results.%s.%s"       // tenant_id, job_id
	subjectLogPattern         = "logs.%s.%s"          // tenant_id, device_id
)

// SubjectJobDispatch returns the subject for dispatching a job to a device.
func SubjectJobDispatch(tenantID, deviceID string) string {
	return fmt.Sprintf(subjectJobDispatchPattern, tenantID, deviceID)
}

// SubjectResult returns the subject for a job result.
func SubjectResult(tenantID, jobID string) string {
	return fmt.Sprintf(subjectResultPattern, tenantID, jobID)
}

// SubjectLog returns the subject for agent log shipping.
func SubjectLog(tenantID, deviceID string) string {
	return fmt.Sprintf(subjectLogPattern, tenantID, deviceID)
}

// Client wraps a NATS connection and JetStream context.
type Client struct {
	Conn      *nats.Conn
	JetStream jetstream.JetStream
}

// Connect establishes a NATS connection and obtains a JetStream context.
func Connect(natsURL string) (*Client, error) {
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create JetStream context: %w", err)
	}

	return &Client{Conn: nc, JetStream: js}, nil
}

// EnsureStreams creates the three required JetStream streams if they don't
// already exist. Existing streams are not modified.
func (c *Client) EnsureStreams(ctx context.Context) error {
	streams := []jetstream.StreamConfig{
		{
			Name:      StreamJobs,
			Subjects:  []string{"jobs.dispatch.>"},
			Retention: jetstream.WorkQueuePolicy,
		},
		{
			Name:      StreamResults,
			Subjects:  []string{"results.>"},
			Retention: jetstream.InterestPolicy,
		},
		{
			Name:      StreamLogs,
			Subjects:  []string{"logs.>"},
			Retention: jetstream.LimitsPolicy,
			MaxAge:    7 * 24 * time.Hour,
		},
	}

	for _, cfg := range streams {
		_, err := c.JetStream.CreateOrUpdateStream(ctx, cfg)
		if err != nil {
			return fmt.Errorf("ensure stream %q: %w", cfg.Name, err)
		}
	}
	return nil
}

// Close drains and closes the NATS connection.
func (c *Client) Close() {
	_ = c.Conn.Drain()
}
