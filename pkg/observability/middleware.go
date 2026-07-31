package observability

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

// PrometheusMiddleware middleware for monitoring Fiber requests.
//
// Two things here are not obvious and both were bugs.
//
// **The label is the route pattern, not the path.** `c.Path()` on an image
// request is `/lbzone/w:200/brands/<uuid>/products/<uuid>.png` — a distinct
// value for every object at every size. As a Prometheus label that is one time
// series per image variant: unbounded cardinality, which is the standard way to
// take down a Prometheus server. `c.Route().Path` is the registered pattern
// (`/:bucket/w::width/*`), a set with about a dozen members, and it is what you
// actually want to aggregate on.
//
// **Retained strings must be copied.** Fiber returns zero-copy views into the
// fasthttp request buffer, valid only for the life of the handler; the buffer is
// then reused by the next request. A Prometheus label retains its string
// forever, so the two together mean the label value mutates underneath the
// registry. This was visibly happening — one scrape carried
// `…8d8e7c5530bd.pn`, `…8d8e7c5530b` and `…8d8e7c5530bd.png.png` as three
// separate series for the same request — and when the overwritten bytes were
// not valid UTF-8, `Gather()` failed and `/metrics` answered 500 with no
// explanation. `utils.CopyString` is the documented fix.
func PrometheusMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// Process request
		chainErr := c.Next()

		// Record metrics
		duration := time.Since(start).Seconds()
		method := utils.CopyString(c.Method())
		endpoint := routeLabel(c)
		status := strconv.Itoa(c.Response().StatusCode())

		RequestCounter.WithLabelValues(method, endpoint, status).Inc()
		RequestDuration.WithLabelValues(method, endpoint).Observe(duration)

		return chainErr
	}
}

// routeLabel returns the matched route pattern, copied out of the request
// buffer. Requests that matched nothing are bucketed together rather than
// labelled with the path they asked for — otherwise a scanner probing random
// URLs writes one new series per probe, which is cardinality growth an
// anonymous caller controls.
func routeLabel(c *fiber.Ctx) string {
	if c.Response().StatusCode() == fiber.StatusNotFound {
		return "unmatched"
	}

	route := c.Route()
	if route == nil || route.Path == "" {
		return "unmatched"
	}

	return utils.CopyString(route.Path)
}
