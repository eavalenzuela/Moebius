package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all registered Prometheus metrics for the Moebius server.
var (
	AgentCheckinsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_checkins_total",
		Help: "Total check-ins received.",
	}, []string{"tenant_id"})

	AgentOnline = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "agent_online",
		Help: "Currently online agents.",
	}, []string{"tenant_id"})

	JobsEnqueuedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_enqueued_total",
		Help: "Jobs enqueued.",
	}, []string{"type", "tenant_id"})

	JobsCompletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_completed_total",
		Help: "Jobs completed.",
	}, []string{"type", "status", "tenant_id"})

	JobDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "job_duration_seconds",
		Help:    "Job execution duration.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12), // 0.1s to ~200s
	}, []string{"type"})

	JobQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "job_queue_depth",
		Help: "Current job queue depth.",
	}, []string{"status"})

	FileTransferBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "file_transfer_bytes_total",
		Help: "Bytes transferred.",
	}, []string{"direction", "tenant_id"})

	APIRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "api_request_duration_seconds",
		Help:    "API request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	DBQueryDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "db_query_duration_seconds",
		Help:    "Database query latency.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~2s
	}, []string{"query"})

	RateLimitRejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limit_rejections_total",
		Help: "Total requests rejected by rate limiting.",
	}, []string{"limiter"})

	AuditWriteFailuresTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "audit_write_failures_total",
		Help: "Total audit log writes that failed. A non-zero value indicates audit entries were lost — investigate the database and alert if this persists.",
	})
)
