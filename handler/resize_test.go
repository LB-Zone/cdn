package handler

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
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

// pngDeclaring is the start of a PNG whose header claims width x height. The
// dimension check reads only that header, so nothing more is needed to show
// an oversized image is refused before any decoder runs.
func pngDeclaring(width, height uint32) []byte {
	ihdr := []byte{0, 0, 0, 0, 0, 0, 0, 0, 8, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.WriteString("IHDR")
	b.Write(ihdr)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	return b.Bytes()
}

func TestResizeRefusesAnOversizedImageWith422(t *testing.T) {
	res, body := postToResize(t, "bomb.png", pngDeclaring(30000, 30000), map[string]string{"width": "200"})
	if res.StatusCode != fiber.StatusUnprocessableEntity || !strings.Contains(body, "image_too_large") {
		t.Errorf("status %d body %s; want 422 image_too_large", res.StatusCode, body)
	}
}
