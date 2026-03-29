package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/moebius-oss/moebius/shared/protocol"
)

// Executor receives jobs from the poller and runs them.
type Executor struct {
	serverURL string
	client    *http.Client
	log       *slog.Logger
}

// New creates an Executor.
func New(serverURL string, client *http.Client, log *slog.Logger) *Executor {
	return &Executor{
		serverURL: strings.TrimRight(serverURL, "/"),
		client:    client,
		log:       log,
	}
}

// HandleJob is the poller.JobHandler callback. It runs in a new goroutine per job.
func (e *Executor) HandleJob(job protocol.JobDispatch) {
	go e.runJob(context.Background(), job)
}

func (e *Executor) runJob(ctx context.Context, job protocol.JobDispatch) {
	e.log.Info("job received", slog.String("job_id", job.JobID), slog.String("type", job.Type))

	// Acknowledge
	if err := e.acknowledge(ctx, job.JobID); err != nil {
		e.log.Error("failed to acknowledge job",
			slog.String("job_id", job.JobID), slog.String("error", err.Error()))
		return
	}

	// Execute
	startedAt := time.Now().UTC()
	result := e.execute(ctx, job)
	completedAt := time.Now().UTC()
	result.StartedAt = &startedAt
	result.CompletedAt = &completedAt

	// Report result
	if err := e.submitResult(ctx, job.JobID, result); err != nil {
		e.log.Error("failed to submit job result",
			slog.String("job_id", job.JobID), slog.String("error", err.Error()))
		return
	}

	e.log.Info("job completed",
		slog.String("job_id", job.JobID),
		slog.String("status", result.Status),
	)
}

func (e *Executor) execute(ctx context.Context, job protocol.JobDispatch) protocol.JobResultSubmission {
	switch job.Type {
	case "exec":
		return e.executeExec(ctx, job.Payload)
	default:
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: fmt.Sprintf("unsupported job type: %s", job.Type),
		}
	}
}

func (e *Executor) executeExec(ctx context.Context, payload json.RawMessage) protocol.JobResultSubmission {
	var p protocol.ExecPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "invalid exec payload: " + err.Error(),
		}
	}

	if p.Command == "" {
		return protocol.JobResultSubmission{
			Status:  "failed",
			Message: "empty command",
		}
	}

	if p.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", p.Command) //nolint:gosec // server-dispatched command
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", p.Command) //nolint:gosec // server-dispatched command
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	status := "completed"
	var message string

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			status = "timed_out"
			message = "command timed out"
		} else {
			status = "failed"
			message = err.Error()
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != context.DeadlineExceeded {
			exitCode = -1
		}
	}

	return protocol.JobResultSubmission{
		Status:   status,
		ExitCode: &exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Message:  message,
	}
}

func (e *Executor) acknowledge(ctx context.Context, jobID string) error {
	url := e.serverURL + "/v1/agents/jobs/" + jobID + "/acknowledge"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST acknowledge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("acknowledge failed (HTTP %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

func (e *Executor) submitResult(ctx context.Context, jobID string, result protocol.JobResultSubmission) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	url := e.serverURL + "/v1/agents/jobs/" + jobID + "/result"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST result: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("submit result failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}
