package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestOnlyAServedImageIsCacheable(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "0.0.0.0")
	app := fiber.New(ClientIPConfig(fiber.Config{}))
	app.Use(NewAdvancedRateLimiter(1, time.Minute))
	app.Get("/ok", ImageCacheControl(), func(c *fiber.Ctx) error { return c.SendString("img") })
	app.Get("/gone", ImageCacheControl(), func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusNotFound) })

	header := func(path, ip string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header.Get(fiber.HeaderCacheControl)
	}

	if code, cc := header("/ok", "198.51.100.1"); code != 200 || cc != "public, max-age=2592000, immutable" {
		t.Fatalf("served image: %d %q, want 200 with a month's immutable caching", code, cc)
	}
	if code, cc := header("/gone", "198.51.100.2"); code != 404 || cc != "no-store" {
		t.Fatalf("missing image: %d %q, want 404 no-store", code, cc)
	}
	// A rate-limited request carries no freshness at all, so nothing keeps it.
	header("/ok", "198.51.100.3")
	if code, cc := header("/ok", "198.51.100.3"); code != 429 || cc != "" {
		t.Fatalf("rate-limited: %d %q, want 429 without caching", code, cc)
	}
}
