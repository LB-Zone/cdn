package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// The chain in the order cmd/main.go mounts it, with small budgets: three
// non-read requests and six reads a minute per client.
func limitedApp(t *testing.T) *fiber.App {
	t.Helper()
	t.Setenv("RATE_LIMIT", "3")
	t.Setenv("READ_RATE_LIMIT", "6")
	t.Setenv("TOKEN", "service-token")

	app := fiber.New()
	app.Use(cors.New(cors.Config{AllowOrigins: "*", AllowHeaders: "*", AllowMethods: "*", MaxAge: 86400}))
	app.Use(ReadRateLimiter())
	app.Use(DefaultAdvancedRateLimiter())
	app.Get("/img", func(c *fiber.Ctx) error { return c.SendString("img") })
	app.Post("/thing", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func send(t *testing.T, app *fiber.App, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The bypass: a different junk Authorization value per request used to open a
// new bucket every time.
func TestJunkAuthorizationDoesNotOpenNewBuckets(t *testing.T) {
	app := limitedApp(t)
	for i := 0; i < 3; i++ {
		send(t, app, "POST", "/thing", nil)
	}
	for i := 0; i < 5; i++ {
		res := send(t, app, "POST", "/thing", map[string]string{"Authorization": fmt.Sprintf("Bearer junk-%d", i)})
		if res.StatusCode != fiber.StatusTooManyRequests {
			t.Fatalf("junk token %d got %d: a made-up header bought a fresh budget", i, res.StatusCode)
		}
	}
}

// The API, proven by the real token, is one identity of its own: anonymous
// traffic from the same address does not spend its budget, nor it theirs.
func TestTheVerifiedServiceIsCountedApart(t *testing.T) {
	app := limitedApp(t)
	for i := 0; i < 3; i++ {
		send(t, app, "POST", "/thing", nil)
	}
	if res := send(t, app, "POST", "/thing", nil); res.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("anonymous client past its budget got %d", res.StatusCode)
	}
	service := map[string]string{"Authorization": "Bearer service-token"}
	if res := send(t, app, "POST", "/thing", service); res.StatusCode != fiber.StatusOK {
		t.Fatalf("the verified service was throttled by anonymous traffic: %d", res.StatusCode)
	}
}

// Several customers behind one carrier address: reads have their own, larger
// budget, so exhausting the write budget does not stop images loading, and
// browsing does not stop uploads.
func TestReadsAndOtherRequestsHaveSeparateBudgets(t *testing.T) {
	app := limitedApp(t)
	for i := 0; i < 3; i++ {
		send(t, app, "POST", "/thing", nil)
	}
	for client := 0; client < 6; client++ {
		if res := send(t, app, "GET", "/img", nil); res.StatusCode != fiber.StatusOK {
			t.Fatalf("read %d from the shared address got %d after the write budget was spent", client, res.StatusCode)
		}
	}
	if res := send(t, app, "GET", "/img", nil); res.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("the read budget is still a limit: read 7 got %d", res.StatusCode)
	}
}

// A 429 must be readable by a browser app: CORS headers present, and a
// preflight is answered, never counted or refused.
func TestA429CarriesCORSAndPreflightsAreNotLimited(t *testing.T) {
	app := limitedApp(t)
	origin := map[string]string{"Origin": "https://portal.lbzone.test"}
	for i := 0; i < 3; i++ {
		send(t, app, "POST", "/thing", origin)
	}

	res := send(t, app, "POST", "/thing", origin)
	if res.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("429 without Access-Control-Allow-Origin: the browser sees an opaque CORS error")
	}
	if res.Header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}

	for i := 0; i < 10; i++ {
		pre := send(t, app, "OPTIONS", "/thing", map[string]string{
			"Origin": "https://portal.lbzone.test", "Access-Control-Request-Method": "POST",
		})
		if pre.StatusCode == fiber.StatusTooManyRequests {
			t.Fatalf("preflight %d was rate limited", i)
		}
	}
}
