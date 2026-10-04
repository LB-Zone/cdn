package middleware

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// app.Test connects from 0.0.0.0, so "trusted" here means listing that address.
func ipApp(t *testing.T, trusted string) *fiber.App {
	t.Helper()
	t.Setenv("TRUSTED_PROXIES", trusted)
	app := fiber.New(ClientIPConfig(fiber.Config{}))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString(c.IP()) })
	return app
}

func ipFor(t *testing.T, app *fiber.App, forwarded string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/", nil)
	if forwarded != "" {
		req.Header.Set(fiber.HeaderXForwardedFor, forwarded)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestClientIPComesFromTheProxyHeaderBehindATrustedProxy(t *testing.T) {
	app := ipApp(t, "0.0.0.0")

	if got := ipFor(t, app, "203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("IP = %q, want the forwarded client address", got)
	}
	if got := ipFor(t, app, "203.0.113.7, 10.0.0.2"); got != "203.0.113.7" {
		t.Fatalf("IP = %q, want the first address of a forwarded chain", got)
	}
	// The app talks to the cdn directly; with no header it is keyed by its own
	// address rather than all landing in one empty-string bucket.
	if got := ipFor(t, app, ""); got != "0.0.0.0" {
		t.Fatalf("IP = %q, want the peer address when no header is sent", got)
	}
}

func TestClientIPIgnoresTheHeaderFromAnUntrustedPeer(t *testing.T) {
	app := ipApp(t, "10.0.0.0/8")

	if got := ipFor(t, app, "203.0.113.7"); got == "203.0.113.7" {
		t.Fatal("a direct client chose its own address by sending X-Forwarded-For")
	}
}

// The regression itself: two visitors behind the same proxy must not share a
// rate-limit bucket.
func TestVisitorsBehindOneProxyGetSeparateRateLimits(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "0.0.0.0")
	app := fiber.New(ClientIPConfig(fiber.Config{}))
	app.Use(NewAdvancedRateLimiter(2, time.Minute))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	status := func(ip string) int {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(fiber.HeaderXForwardedFor, ip)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}

	status("198.51.100.1")
	status("198.51.100.1")
	if got := status("198.51.100.1"); got != fiber.StatusTooManyRequests {
		t.Fatalf("third request from one visitor = %d, want 429", got)
	}
	if got := status("198.51.100.2"); got != fiber.StatusOK {
		t.Fatalf("first request from another visitor = %d, want 200 — they shared a bucket", got)
	}
}
