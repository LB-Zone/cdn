package observability

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newApp wires the middleware onto the same route shapes cmd/main.go registers,
// so the labels under test are the ones the service actually produces.
func newApp() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(PrometheusMiddleware())
	app.Get("/metrics", MetricsHandler)
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/:bucket/w::width/*", func(c *fiber.Ctx) error { return c.SendString("img") })
	app.Get("/:bucket/s::preset/*", func(c *fiber.Ctx) error { return c.SendString("img") })
	app.Get("/:bucket/*", func(c *fiber.Ctx) error { return c.SendString("img") })
	return app
}

func scrape(t *testing.T, app *fiber.App) map[string]*dto.MetricFamily {
	t.Helper()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/metrics", nil), -1)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, fiber.StatusOK, resp.StatusCode,
		"a scrape must never fail; ContinueOnError exists so a bad collector degrades rather than blanks the endpoint")

	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(resp.Body)
	require.NoError(t, err, "the body is not valid Prometheus exposition format")

	return families
}

// endpointLabels returns every `endpoint` label value currently recorded.
func endpointLabels(t *testing.T, app *fiber.App) []string {
	t.Helper()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/metrics", nil), -1)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var values []string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "cdn_http_requests_total{") {
			continue
		}
		start := strings.Index(line, `endpoint="`)
		if start < 0 {
			continue
		}
		rest := line[start+len(`endpoint="`):]
		if end := strings.Index(rest, `"`); end >= 0 {
			values = append(values, rest[:end])
		}
	}
	return values
}

// The endpoint label must be the *route pattern*. Labelling by path gives one
// time series per image per size, which is unbounded cardinality — the standard
// way to take a Prometheus server down.
func TestEndpointLabelIsTheRoutePatternNotThePath(t *testing.T) {
	app := newApp()

	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(fiber.MethodGet,
			fmt.Sprintf("/lbzone/w:%d/brands/8c6dca37-6928-436e-a775-8d8e7c5530bd/products/%d.png", 100+i, i), nil)
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		resp.Body.Close()
	}

	labels := endpointLabels(t, app)
	require.NotEmpty(t, labels)

	unique := map[string]struct{}{}
	for _, l := range labels {
		unique[l] = struct{}{}
	}

	// 50 distinct URLs, one route. Before this was fixed the count was 50.
	assert.LessOrEqual(t, len(unique), 3,
		"50 distinct image URLs must not become 50 time series; got %v", unique)

	for _, l := range labels {
		assert.NotContains(t, l, "8c6dca37",
			"an object id in a label value means the path is being used, not the route")
	}
}

// The bug that made /metrics answer 500.
//
// Fiber hands out zero-copy views into the fasthttp request buffer. Retaining
// one in a Prometheus label means the registry holds a string whose bytes are
// later overwritten by another request — producing truncated values, duplicated
// suffixes, and eventually a byte sequence that is not valid UTF-8, at which
// point `Gather()` fails and the whole endpoint 500s.
//
// Concurrency is what surfaces it, so this test is concurrent on purpose.
func TestConcurrentTrafficProducesNoCorruptedLabels(t *testing.T) {
	app := newApp()

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				req := httptest.NewRequest(fiber.MethodGet,
					fmt.Sprintf("/lbzone/w:%d/brands/8c6dca37-6928-436e-a775-8d8e7c5530bd/products/obj-%d-%d.png", n+1, n, i), nil)
				resp, err := app.Test(req, -1)
				if err != nil {
					return
				}
				resp.Body.Close()
			}
		}(worker)
	}
	wg.Wait()

	// The scrape itself must survive, which it does not when a label value is
	// invalid UTF-8.
	families := scrape(t, app)
	assert.Contains(t, families, "cdn_http_requests_total")

	for _, label := range endpointLabels(t, app) {
		assert.True(t, utf8.ValidString(label),
			"label value is not valid UTF-8, which is what breaks Gather(): %q", label)
		assert.NotContains(t, label, ".png",
			"a route pattern carries no object name: %q", label)
	}
}

// A caller asking for URLs that match nothing must not be able to grow the
// series count at will.
func TestUnmatchedRoutesShareOneLabel(t *testing.T) {
	app := newApp()
	app.Use(func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNotFound) })

	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(fiber.MethodPost, fmt.Sprintf("/nope/%d", i), nil)
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		resp.Body.Close()
	}

	unique := map[string]struct{}{}
	for _, l := range endpointLabels(t, app) {
		unique[l] = struct{}{}
	}

	for i := 0; i < 20; i++ {
		assert.NotContains(t, unique, fmt.Sprintf("/nope/%d", i),
			"an unmatched path must not become its own series")
	}
}

// The endpoint's whole job: a body the official parser accepts.
func TestMetricsHandlerServesValidExpositionFormat(t *testing.T) {
	app := newApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/health", nil), -1)
	require.NoError(t, err)
	resp.Body.Close()

	families := scrape(t, app)

	// The service's own instrumentation and the Go runtime's, which together
	// prove the default registry is what is being served.
	assert.Contains(t, families, "cdn_http_requests_total")
	assert.Contains(t, families, "cdn_http_request_duration_seconds")
	assert.Contains(t, families, "go_goroutines")

	assert.Equal(t, "Total number of HTTP requests", families["cdn_http_requests_total"].GetHelp(),
		"# HELP must survive; the hand-rolled renderer used to mangle it")
}
