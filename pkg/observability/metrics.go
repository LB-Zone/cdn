package observability

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

var (
	// RequestCounter counts all HTTP requests
	RequestCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cdn_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	// RequestDuration measures request duration
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cdn_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	// ImageProcessingDuration measures image processing duration
	ImageProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cdn_image_processing_duration_seconds",
			Help:    "Image processing duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	// StorageOperationDuration measures storage operation duration
	StorageOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cdn_storage_operation_duration_seconds",
			Help:    "Storage operation duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "provider"},
	)

	// Health Check Metrics
	ServiceHealth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "service_health_status",
			Help: "Current health status of services (1 for healthy, 0 for unhealthy)",
		},
		[]string{"service"},
	)

	ServiceHealthCheckDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "service_health_check_duration_seconds",
			Help:    "Duration of health checks in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"service"},
	)

	LastHealthCheckTimestamp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "service_last_health_check_timestamp",
			Help: "Timestamp of the last health check",
		},
		[]string{"service"},
	)

	// Worker Pool Metrics
	WorkerPoolQueueSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "worker_pool_queue_size",
			Help: "Current number of jobs in the worker pool queue",
		},
	)

	WorkerPoolActiveWorkers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "worker_pool_active_workers",
			Help: "Current number of active workers in the pool",
		},
	)

	WorkerJobProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "worker_job_processing_duration_seconds",
			Help:    "Duration of job processing in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"status"},
	)

	WorkerJobRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_job_retries_total",
			Help: "Total number of job retries",
		},
		[]string{"job_type"},
	)

	// Batch Processor Metrics
	BatchProcessorQueueSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "batch_processor_queue_size",
			Help: "Current number of items in the batch processor queue",
		},
	)

	BatchProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "batch_processing_duration_seconds",
			Help:    "Duration of batch processing in seconds",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"status"},
	)

	BatchItemsProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "batch_items_processed_total",
			Help: "Total number of items processed by the batch processor",
		},
		[]string{"status"},
	)

	BatchRetries = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "batch_retries_total",
			Help: "Total number of batch retries",
		},
	)

	// Cache Metrics
	CacheOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cache_operations_total",
			Help: "The total number of cache operations",
		},
		[]string{"operation", "status"},
	)

	CacheOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cache_operation_duration_seconds",
			Help:    "Duration of cache operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "status"},
	)

	CacheSize = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cache_size_bytes",
			Help: "Current size of cached data in bytes",
		},
		[]string{"type"},
	)

	CacheHitRatio = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cache_hit_ratio",
			Help: "Cache hit ratio",
		},
		[]string{"operation"},
	)

	// Circuit Breaker metrics
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_state",
			Help: "Current state of circuit breaker (0: Closed, 1: Open, 2: Half-Open)",
		},
		[]string{"name"},
	)

	CircuitBreakerFailures = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_failures",
			Help: "Number of consecutive failures in circuit breaker",
		},
		[]string{"name"},
	)

	CircuitBreakerSuccesses = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "circuit_breaker_successes",
			Help: "Number of consecutive successes in circuit breaker",
		},
		[]string{"name"},
	)

	CircuitBreakerRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "circuit_breaker_requests_total",
			Help: "Total number of requests handled by circuit breaker",
		},
		[]string{"name", "result"},
	)
)

// promHandler renders the default registry in Prometheus exposition format.
//
// This used to be hand-rolled as `mf.String()` over the gathered families, which
// produces the *protobuf text* representation — `name:"cdn_http_requests_total"
// help:"..." type:COUNTER metric:{...}`. It looks plausible in a browser and no
// Prometheus can parse a byte of it, so the endpoint had never actually been
// scraped despite existing since the service was written.
//
// `promhttp` is the canonical renderer: it negotiates the format with the
// scraper via `Accept`, emits the text exposition format by default, and gets
// escaping, `# HELP` / `# TYPE` and histogram bucket layout right — all things
// the hand-rolled version got wrong.
//
// ContinueOnError rather than the default HTTPErrorOnError: one misbehaving
// collector used to turn the whole endpoint into a 500 with the body
// "Error collecting metrics" and no indication of which collector or why. A
// scrape that returns most of the metrics plus a logged error is strictly more
// useful than one that returns nothing at all — and an alert on a *missing*
// metric still fires.
var promHandler = fasthttpadaptor.NewFastHTTPHandler(
	promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		ErrorLog:          gatherErrorLogger{},
		ErrorHandling:     promhttp.ContinueOnError,
		EnableOpenMetrics: true,
	}),
)

// gatherErrorHandling adapts promhttp's logger interface onto zerolog, so a
// collector that starts failing is visible instead of silently swallowed.
type gatherErrorLogger struct{}

func (gatherErrorLogger) Println(v ...any) {
	logger := Logger()
	logger.Error().Msgf("prometheus gather: %s", strings.TrimSpace(fmt.Sprintln(v...)))
}

// MetricsHandler serves the Prometheus scrape endpoint.
func MetricsHandler(c *fiber.Ctx) error {
	promHandler(c.Context())
	return nil
}
