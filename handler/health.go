package handler

import (
	"context"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/minio/minio-go/v7"
	"github.com/mstgnz/cdn/pkg/observability"
	"github.com/mstgnz/cdn/service"
)

// Status values reported per dependency. `notConfigured` is distinct from
// unhealthy on purpose: "we are not using this" and "this is broken" are
// different facts, and only one of them should take the service offline.
const (
	healthy       = "healthy"
	notConfigured = "not configured"
)

type HealthChecker struct {
	minioClient *minio.Client
	awsService  service.AwsService
	cache       service.CacheService
}

func NewHealthChecker(minioClient *minio.Client, awsService service.AwsService, cache service.CacheService) *HealthChecker {
	return &HealthChecker{
		minioClient: minioClient,
		awsService:  awsService,
		cache:       cache,
	}
}

// HealthCheck handles health check requests
func (h *HealthChecker) HealthCheck(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	minioHealth := h.checkMinioHealth(ctx)
	awsHealth := h.checkAwsHealth(ctx)
	cacheHealth := h.checkCacheHealth(ctx)

	overallStatus := "healthy"
	statusCode := fiber.StatusOK

	// AWS is an *optional* cold-storage backend (cdn.md: MinIO primary, S3 and
	// Glacier optional). A deployment that does not configure it is not
	// degraded, it simply is not using it — and this endpoint is what Traefik,
	// the container healthcheck and the deploy smoke test all poll. Counting an
	// unconfigured backend as a failure took the whole service out of rotation
	// on a stack where MinIO was serving every request perfectly.
	if minioHealth != healthy || cacheHealth != healthy ||
		(awsHealth != healthy && awsHealth != notConfigured) {
		overallStatus = "degraded"
		statusCode = fiber.StatusServiceUnavailable
	}

	data := map[string]any{
		"status": overallStatus,
		"services": map[string]any{
			"minio": minioHealth,
			"aws":   awsHealth,
			"cache": cacheHealth,
		},
		"timestamp": time.Now().UTC(),
	}

	return service.Response(c, statusCode, true, "Health check", data)
}

func (h *HealthChecker) checkMinioHealth(ctx context.Context) string {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		observability.ServiceHealthCheckDuration.WithLabelValues("minio").Observe(duration)
		observability.LastHealthCheckTimestamp.WithLabelValues("minio").Set(float64(time.Now().Unix()))
	}()

	if _, err := h.minioClient.ListBuckets(ctx); err != nil {
		observability.ServiceHealth.WithLabelValues("minio").Set(0)
		return "unhealthy: " + err.Error()
	}
	observability.ServiceHealth.WithLabelValues("minio").Set(1)
	return healthy
}

func (h *HealthChecker) checkAwsHealth(ctx context.Context) string {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		observability.ServiceHealthCheckDuration.WithLabelValues("aws").Observe(duration)
		observability.LastHealthCheckTimestamp.WithLabelValues("aws").Set(float64(time.Now().Unix()))
	}()

	// No credentials means no S3 backend was ever asked for. Calling out to AWS
	// anyway would fail slowly and report a problem that does not exist.
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		observability.ServiceHealth.WithLabelValues("aws").Set(1)
		return notConfigured
	}

	if _, err := h.awsService.ListBuckets(); err != nil {
		observability.ServiceHealth.WithLabelValues("aws").Set(0)
		return "unhealthy: " + err.Error()
	}
	observability.ServiceHealth.WithLabelValues("aws").Set(1)
	return healthy
}

func (h *HealthChecker) checkCacheHealth(ctx context.Context) string {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		observability.ServiceHealthCheckDuration.WithLabelValues("cache").Observe(duration)
		observability.LastHealthCheckTimestamp.WithLabelValues("cache").Set(float64(time.Now().Unix()))
	}()

	testKey := "health:test"
	testValue := []byte("test")

	// Try to set a test value
	if err := h.cache.Set(testKey, testValue, time.Second); err != nil {
		observability.ServiceHealth.WithLabelValues("cache").Set(0)
		return "unhealthy: set failed - " + err.Error()
	}

	// Try to get the test value
	if _, err := h.cache.Get(testKey); err != nil {
		observability.ServiceHealth.WithLabelValues("cache").Set(0)
		return "unhealthy: get failed - " + err.Error()
	}

	observability.ServiceHealth.WithLabelValues("cache").Set(1)
	return healthy
}
