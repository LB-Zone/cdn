package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendString("x") })

	for _, path := range []string{"/ok", "/missing"} {
		res, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := res.Header.Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
			t.Errorf("%s: Content-Security-Policy = %q", path, got)
		}
	}
}
