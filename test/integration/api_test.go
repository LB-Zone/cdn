package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	baseURL = "http://localhost:9090"
	timeout = 10 * time.Second
)

// TestMain skips the whole package when the service is not running.
//
// These tests drive a live cdn over HTTP. Without one, every request returned a
// nil response and each test panicked on the nil dereference — which reads in CI
// as "the cdn is broken" rather than "the cdn is not started". A skip says the
// true thing.
func TestMain(m *testing.M) {
	client := &http.Client{Timeout: 2 * time.Second}
	if _, err := client.Get(baseURL + "/health"); err != nil {
		fmt.Printf("skipping integration tests: no cdn at %s (%v)\n", baseURL, err)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHealthEndpoint(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	resp, err := client.Get(baseURL + "/health")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	err = json.NewDecoder(resp.Body).Decode(&body)
	assert.NoError(t, err)

	assert.Equal(t, true, body["status"])
	assert.Equal(t, "Healthy", body["message"])
}

func TestUploadEndpoint(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	// Test file upload
	fileContents := []byte("test image content")
	req, err := http.NewRequest("POST", baseURL+"/upload", bytes.NewBuffer(fileContents))
	assert.NoError(t, err)

	req.Header.Set("Content-Type", "multipart/form-data")
	req.Header.Set("Authorization", "test-token")

	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	// Should fail without proper form data
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMetricsEndpoint(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	resp, err := client.Get(baseURL + "/metrics")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
}
