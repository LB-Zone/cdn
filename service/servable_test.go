package service

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestServableImageTypeServesOnlyRasterImages(t *testing.T) {
	if typ, ok := ServableImageType(pngBytes(t)); !ok || typ != "image/png" {
		t.Errorf("a real PNG: got %q, servable=%v", typ, ok)
	}
	if typ, ok := ServableImageType([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")); !ok || typ != "image/jpeg" {
		t.Errorf("a JPEG header: got %q, servable=%v", typ, ok)
	}

	refused := map[string]string{
		"html named anything":  `<!doctype html><html><script>alert(1)</script></html>`,
		"html without doctype": `<script>alert(document.domain)</script>`,
		"svg with script":      `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"xml":                  `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"plain text":           "just text",
		"pdf":                  "%PDF-1.7\n",
		"empty":                "",
	}
	for name, content := range refused {
		if typ, ok := ServableImageType([]byte(content)); ok {
			t.Errorf("%s was servable as %q", name, typ)
		}
	}
}
