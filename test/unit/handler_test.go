package unit

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gofiber/fiber/v2"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mstgnz/cdn/handler"
	"github.com/mstgnz/cdn/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Fiber's `app.Test` defaults to a 1000ms deadline. The health handler
// drives a *real* `*minio.Client` at a MinIO that is not running, and
// minio-go retries with backoff before giving up — comfortably under a
// second on a developer machine, over it on a loaded CI runner. The suite
// failed there and nowhere else, which is the worst kind of flake.
const testTimeoutMs = 10_000

func setupMockMinio() *minio.Client {
	client, err := minio.New("localhost:9000", &minio.Options{
		Creds:  credentials.NewStaticV4("minioadmin", "minioadmin", ""),
		Secure: false,
	})
	if err != nil {
		return nil
	}
	return client
}

type MockAwsService struct {
	mock.Mock
	service.AwsService
}

// ListBuckets must be declared explicitly. The embedded service.AwsService is a nil
// interface, so without this the promoted method panics as soon as the health check
// reaches the AWS probe.
func (m *MockAwsService) ListBuckets() ([]s3types.Bucket, error) {
	args := m.Called()

	buckets, _ := args.Get(0).([]s3types.Bucket)
	return buckets, args.Error(1)
}

type MockCacheService struct {
	mock.Mock
	service.CacheService
}

// Set and Get must be declared explicitly for the same reason as
// MockAwsService.ListBuckets: the embedded interface is nil, so a promoted method
// panics. These two are what the health check probes.
func (m *MockCacheService) Set(key string, value []byte, expiration time.Duration) error {
	return m.Called(key, value, expiration).Error(0)
}

func (m *MockCacheService) Get(key string) ([]byte, error) {
	args := m.Called(key)

	value, _ := args.Get(0).([]byte)
	return value, args.Error(1)
}

func TestHealthCheck(t *testing.T) {
	// Setup
	app := fiber.New()
	mockMinio := setupMockMinio()
	mockAws := &MockAwsService{}
	mockAws.On("ListBuckets").Return([]s3types.Bucket{}, nil)
	mockCache := &MockCacheService{}
	mockCache.On("Set", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockCache.On("Get", mock.Anything).Return([]byte("test"), nil)

	healthChecker := handler.NewHealthChecker(mockMinio, mockAws, mockCache)
	app.Get("/health", healthChecker.HealthCheck)

	// `minioClient` is a concrete `*minio.Client`, not an interface, so it
	// cannot be substituted — this test drives a real client at a MinIO that is
	// not running. That makes it a test of the *shape* of the response and of
	// how a dependency's state maps to the status code, which is what the
	// container healthcheck and the blackbox probe depend on.
	t.Run("reports one entry per dependency", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		resp, err := app.Test(req, testTimeoutMs)
		require.NoError(t, err)

		var body map[string]any
		assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

		data, ok := body["data"].(map[string]any)
		assert.True(t, ok, "the response envelope carries a data object")

		services, ok := data["services"].(map[string]any)
		assert.True(t, ok, "every dependency reports its own state")
		assert.Contains(t, services, "minio")
		assert.Contains(t, services, "aws")
		assert.Contains(t, services, "cache")
		assert.NotEmpty(t, data["timestamp"])
	})

	// The regression this exists for: AWS is an optional cold-storage backend
	// (cdn.md — MinIO primary, S3/Glacier optional). Reporting an unconfigured
	// backend as unhealthy answered 503 forever on a stack that was serving
	// every request perfectly, which took the service out of Traefik's rotation
	// and failed every deployment's smoke test.
	t.Run("an unconfigured AWS backend is not a failure", func(t *testing.T) {
		t.Setenv("AWS_ACCESS_KEY_ID", "")

		req := httptest.NewRequest("GET", "/health", nil)
		resp, err := app.Test(req, testTimeoutMs)
		require.NoError(t, err)

		var body map[string]any
		assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

		services := body["data"].(map[string]any)["services"].(map[string]any)
		assert.Equal(t, "not configured", services["aws"],
			"not in use is not the same as broken")
	})
}

func TestUploadImage(t *testing.T) {
	// Setup
	app := fiber.New()
	mockMinio := setupMockMinio()
	mockAws := &MockAwsService{}
	mockImageService := &service.ImageService{}

	imageHandler := handler.NewImage(mockMinio, mockAws, mockImageService)
	app.Post("/upload", imageHandler.UploadImage)

	// Test cases
	tests := []struct {
		name           string
		payload        []byte
		expectedStatus int
		expectedError  string
	}{
		{
			// A JSON body carries no file. The handler answers on the missing
			// file rather than on the content type, which is the more useful
			// message for whoever is holding the failing request.
			name:           "Invalid Request",
			payload:        []byte(`{}`),
			expectedStatus: fiber.StatusBadRequest,
			expectedError:  "File Not Found!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/upload", bytes.NewBuffer(tt.payload))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req, testTimeoutMs)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, resp.StatusCode)

			if tt.expectedError != "" {
				var body map[string]any
				err = json.NewDecoder(resp.Body).Decode(&body)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedError, body["message"])
			}
		})
	}
}
