package handler

import (
	"bytes"
	stdimage "image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// ResizeImage must never send back what it was given unless it is an image:
// it used to echo any non-image with its sniffed type, so posting HTML made
// the cdn serve it as text/html from its own origin.

func postToResize(t *testing.T, filename string, content []byte, fields map[string]string) (*http.Response, string) {
	t.Helper()
	app := fiber.New()
	app.Post("/resize", (&image{}).ResizeImage)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	for k, v := range fields {
		_ = form.WriteField(k, v)
	}
	_ = form.Close()

	req := httptest.NewRequest("POST", "/resize", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(res.Body)
	return res, string(out)
}

func TestResizeRefusesToEchoAnything(t *testing.T) {
	payloads := map[string]string{
		"evil.png":  `<!doctype html><html><script>document.title="pwned"</script></html>`,
		"evil.html": `<script>alert(1)</script>`,
		"evil.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
	}
	for name, content := range payloads {
		res, body := postToResize(t, name, []byte(content), nil)
		if res.StatusCode != fiber.StatusUnsupportedMediaType {
			t.Errorf("%s: status %d, want 415", name, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") || strings.Contains(body, "<script>") {
			t.Errorf("%s: the payload was echoed back (%s)", name, ct)
		}
	}
}

func TestResizeReturnsARealImageWithItsOwnType(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, stdimage.NewRGBA(stdimage.Rect(0, 0, 2, 2)))

	// No dimensions: the image comes back unchanged, typed by its bytes.
	res, body := postToResize(t, "photo.html", buf.Bytes(), nil)
	if res.StatusCode != fiber.StatusOK || res.Header.Get("Content-Type") != "image/png" || body != buf.String() {
		t.Errorf("status %d type %q; want 200 image/png with the same bytes", res.StatusCode, res.Header.Get("Content-Type"))
	}
}
