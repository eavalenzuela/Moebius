package natsutil

import "testing"

func TestSubjectJobDispatch(t *testing.T) {
	got := SubjectJobDispatch("ten_abc123", "dev_xyz789")
	want := "jobs.dispatch.ten_abc123.dev_xyz789"
	if got != want {
		t.Errorf("SubjectJobDispatch() = %q, want %q", got, want)
	}
}

func TestSubjectResult(t *testing.T) {
	got := SubjectResult("ten_abc123", "job_xyz789")
	want := "results.ten_abc123.job_xyz789"
	if got != want {
		t.Errorf("SubjectResult() = %q, want %q", got, want)
	}
}

func TestSubjectLog(t *testing.T) {
	got := SubjectLog("ten_abc123", "dev_xyz789")
	want := "logs.ten_abc123.dev_xyz789"
	if got != want {
		t.Errorf("SubjectLog() = %q, want %q", got, want)
	}
}
