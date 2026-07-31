package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	baseURL = "http://localhost:9090"
	timeout = 10 * time.Second
)

// uploadToken is the shared bearer the server presents on the brokered upload
// path. It has to match the running service or the auth tests assert the wrong
// thing; the default is the all-in-one container's.
func uploadToken() string {
	if token := strings.TrimSpace(os.Getenv("CDN_TOKEN")); token != "" {
		return token
	}
	return "lbzone-local-dev-token"
}

// TestMain skips the whole package when the service is not running.
//
// These tests drive a live cdn over HTTP. Without one, every request returned a
// nil response and each test panicked on the nil dereference — which reads in CI
// as "the cdn is broken" rather than "the cdn is not started". A skip says the
// true thing.
//
//	docker run -d --name lbzone -p 9090:9090 lbzone-allinone:dev
//	go test ./test/integration/
func TestMain(m *testing.M) {
	client := &http.Client{Timeout: 2 * time.Second}
	if _, err := client.Get(baseURL + "/health"); err != nil {
		fmt.Printf("skipping integration tests: no cdn at %s (%v)\n", baseURL, err)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The health contract is the service's standard envelope — `{success, message,
// data}`, the same shape every other endpoint returns — with the per-dependency
// detail under `data`.
//
// This test used to assert a flat `{"status": true, "message": "Healthy"}`,
// which the endpoint has not returned since the envelope was adopted. It passed
// only because nothing ran it: the package skips unless a cdn is listening on
// :9090, and until the all-in-one container there rarely was one.
func TestHealthEndpoint(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	resp, err := client.Get(baseURL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Status    string            `json:"status"`
			Services  map[string]string `json:"services"`
			Timestamp time.Time         `json:"timestamp"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.True(t, body.Success)
	assert.Equal(t, "Health check", body.Message)
	assert.Equal(t, "healthy", body.Data.Status)

	// Every dependency is reported by name. `aws` is allowed to be
	// "not configured" — an optional cold-storage backend nobody set up is not
	// a degraded service, and treating it as one used to take the cdn out of
	// rotation on a stack where MinIO was serving every request.
	assert.Equal(t, "healthy", body.Data.Services["minio"])
	assert.Equal(t, "healthy", body.Data.Services["cache"])
	assert.Contains(t, []string{"healthy", "not configured"}, body.Data.Services["aws"])

	assert.WithinDuration(t, time.Now(), body.Data.Timestamp, time.Minute,
		"the timestamp should be from this check, not a cached one")
}

// A 200 with a body Prometheus cannot parse is the failure this endpoint shipped
// with for its whole life: it rendered the *protobuf text* form of each metric
// family, which looks plausible and is unscrapeable.
//
// So the assertion is not "it returns text" — it is "the official Prometheus
// parser accepts it, and the cdn's own metrics are in there".
func TestMetricsEndpoint(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	// Exercise a route first, so there is at least one request counted and the
	// scrape has something of ours to carry.
	warm, err := client.Get(baseURL + "/health")
	require.NoError(t, err)
	warm.Body.Close()

	resp, err := client.Get(baseURL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(resp.Body)
	require.NoError(t, err, "the body is not valid Prometheus exposition format")
	require.NotEmpty(t, families)

	// The service's own instrumentation, not just the runtime's freebies.
	for _, name := range []string{
		"cdn_http_requests_total",
		"cdn_http_request_duration_seconds",
	} {
		assert.Contains(t, families, name, "expected %s in the scrape", name)
	}

	// And the Go runtime collectors, which is how you know the default registry
	// is the one being served.
	assert.Contains(t, families, "go_goroutines")

	counter := families["cdn_http_requests_total"]
	require.NotNil(t, counter)
	assert.NotEmpty(t, counter.GetMetric(), "the counter should have at least one series")
	assert.Equal(t, "Total number of HTTP requests", counter.GetHelp())
}

// OpenMetrics is what a modern scraper negotiates. The handler advertises it, so
// asking for it must produce it rather than falling over.
func TestMetricsEndpointServesOpenMetrics(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest(http.MethodGet, baseURL+"/metrics", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/openmetrics-text; version=1.0.0; charset=utf-8")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "openmetrics-text")

	body := new(bytes.Buffer)
	_, err = body.ReadFrom(resp.Body)
	require.NoError(t, err)

	// OpenMetrics requires the terminator; its absence is how a scraper decides
	// the response was truncated.
	assert.True(t, strings.HasSuffix(strings.TrimRight(body.String(), "\n"), "# EOF"),
		"an OpenMetrics body must end with '# EOF'")
}

// Upload is token-gated (`M2-CDN-1`): the server brokers every upload so the
// browser never holds the token.
func TestUploadRejectsABadToken(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/upload", bytes.NewBufferString("not a form"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data")
	req.Header.Set("Authorization", "Bearer definitely-not-the-token")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.False(t, body.Success)
	assert.Contains(t, body.Message, "Token mismatch")
}

// The previous version of this test sent a bad token *and* a malformed body and
// asserted 400, so it could not tell which one it was measuring. With a valid
// token the 400 is unambiguously about the payload.
func TestUploadRejectsAMalformedBodyWithAValidToken(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/upload", bytes.NewBufferString("not a form"))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data")
	req.Header.Set("Authorization", "Bearer "+uploadToken())

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.False(t, body.Success)
	assert.NotContains(t, body.Message, "Token mismatch",
		"the token was valid, so the refusal must be about the payload")
}

// Missing the header entirely is a different path from presenting a wrong one.
func TestUploadRejectsAMissingToken(t *testing.T) {
	client := &http.Client{Timeout: timeout}

	resp, err := client.Post(baseURL+"/upload", "multipart/form-data", bytes.NewBufferString("x"))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body.Message, "no token provided")
}
