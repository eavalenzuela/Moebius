package scheduler

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func TestCronParserValid(t *testing.T) {
	tests := []struct {
		expr string
	}{
		{"0 2 * * *"},   // daily at 2am
		{"*/5 * * * *"}, // every 5 minutes
		{"0 0 1 * *"},   // first of month
		{"30 8 * * 1"},  // Monday at 8:30
	}

	for _, tt := range tests {
		sched, err := cronParser.Parse(tt.expr)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.expr, err)
			continue
		}
		next := sched.Next(time.Now().UTC())
		if next.IsZero() {
			t.Errorf("Parse(%q) returned zero next time", tt.expr)
		}
	}
}

func TestCronParserInvalid(t *testing.T) {
	tests := []string{
		"",
		"not-a-cron",
		"60 * * * *",
		"* * * *", // only 4 fields
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	for _, expr := range tests {
		_, err := parser.Parse(expr)
		if err == nil {
			t.Errorf("Parse(%q) should have failed", expr)
		}
	}
}

func TestTruncateList(t *testing.T) {
	tests := []struct {
		items []string
		n     int
		want  string
	}{
		{[]string{"a", "b"}, 5, "a, b"},
		{[]string{"a", "b", "c"}, 2, "a, b, ..."},
		{nil, 5, ""},
		{[]string{"x"}, 1, "x"},
	}
	for _, tt := range tests {
		got := truncateList(tt.items, tt.n)
		if got != tt.want {
			t.Errorf("truncateList(%v, %d) = %q, want %q", tt.items, tt.n, got, tt.want)
		}
	}
}

func TestNewSchedulerDefaults(t *testing.T) {
	s := New(nil, nil, nil, nil, Config{})
	if s.tick != 30*time.Second {
		t.Errorf("default tick = %v, want 30s", s.tick)
	}
	if s.dispatchedTimeout != 300*time.Second {
		t.Errorf("default dispatchedTimeout = %v, want 300s", s.dispatchedTimeout)
	}
	if s.inflightTimeout != 3600*time.Second {
		t.Errorf("default inflightTimeout = %v, want 3600s", s.inflightTimeout)
	}

	s2 := New(nil, nil, nil, nil, Config{
		TickSeconds:                60,
		ReaperDispatchedTimeoutSec: 120,
		ReaperInflightTimeoutSec:   240,
	})
	if s2.tick != 60*time.Second {
		t.Errorf("tick = %v, want 60s", s2.tick)
	}
	if s2.dispatchedTimeout != 120*time.Second {
		t.Errorf("dispatchedTimeout = %v, want 120s", s2.dispatchedTimeout)
	}
	if s2.inflightTimeout != 240*time.Second {
		t.Errorf("inflightTimeout = %v, want 240s", s2.inflightTimeout)
	}
}
