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
)

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

	// Test cases
	tests := []struct {
		name           string
		expectedStatus int
		expectedBody   map[string]any
	}{
		{
			name:           "Success Response",
			expectedStatus: fiber.StatusOK,
			expectedBody: map[string]any{
				"success": true,
				"message": "Healthy",
				"data": map[string]any{
					"minio": "Connected",
					"aws":   "Connected",
					"redis": "Connected",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/health", nil)
			resp, err := app.Test(req)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, resp.StatusCode)

			var body map[string]any
			err = json.NewDecoder(resp.Body).Decode(&body)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedBody, body)
		})
	}
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
			name:           "Invalid Request",
			payload:        []byte(`{}`),
			expectedStatus: fiber.StatusBadRequest,
			expectedError:  "Invalid request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/upload", bytes.NewBuffer(tt.payload))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)

			assert.NoError(t, err)
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
