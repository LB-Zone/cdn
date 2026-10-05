package service

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	stdimage "image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/gographics/imagick.v3/imagick"
)

func pngChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	return b.Bytes()
}

// bombPNG is a real, valid greyscale PNG of width x height whose pixels are
// all zero, so it compresses about a thousandfold: the shape of a
// decompression bomb. It is written row by row and never held decoded.
func bombPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var idat bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	row := make([]byte, width+1)
	for y := 0; y < height; y++ {
		if _, err := zw.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	_ = zw.Close()

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8], ihdr[9] = 8, 0 // 8-bit greyscale

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	out.Write(pngChunk("IHDR", ihdr))
	out.Write(pngChunk("IDAT", idat.Bytes()))
	out.Write(pngChunk("IEND", nil))
	return out.Bytes()
}

// pngDeclaring is just the signature and IHDR of a PNG claiming width x
// height: all the dimension check ever reads.
func pngDeclaring(width, height int) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8] = 8
	return append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
}

// webpDeclaring is the RIFF/VP8X header of a WebP claiming width x height.
func webpDeclaring(width, height int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00")
	for _, v := range []int{width - 1, height - 1} {
		b = append(b, byte(v), byte(v>>8), byte(v>>16))
	}
	return b
}

func photo(t *testing.T, width, height int, encode func(*bytes.Buffer, stdimage.Image) error) []byte {
	t.Helper()
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var b bytes.Buffer
	if err := encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegPhoto(t *testing.T, w, h int) []byte {
	return photo(t, w, h, func(b *bytes.Buffer, i stdimage.Image) error { return jpeg.Encode(b, i, &jpeg.Options{Quality: 85}) })
}

func pngPhoto(t *testing.T, w, h int) []byte {
	return photo(t, w, h, func(b *bytes.Buffer, i stdimage.Image) error { return png.Encode(b, i) })
}

// A checked-in WebP: Go has no WebP encoder, and encoding one through
// ImageMagick inside a test crashes on musl (the 128 KiB thread stack the
// dockerfile explains).
func webpPhoto(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "photo-160x120.webp"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRealPhotographsPassTheDimensionCheck(t *testing.T) {
	for name, data := range map[string][]byte{
		"jpeg 1200x800": jpegPhoto(t, 1200, 800),
		"png 640x480":   pngPhoto(t, 640, 480),
		"webp 160x120":  webpPhoto(t),
	} {
		if err := CheckImageDimensions(data); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOversizedImagesAreRefusedFromTheHeader(t *testing.T) {
	cases := map[string][]byte{
		// The header of a 900-megapixel image; the check reads nothing else.
		"30000x30000": pngDeclaring(30000, 30000),
		// Within the pixel budget, but one side is over the limit.
		"12000x100 strip": pngDeclaring(12000, 100),
		// Each side is allowed; together they are 81 megapixels.
		"9000x9000": pngDeclaring(9000, 9000),
		// A real, complete bomb: 49 megapixels of zeros in about 50 KB.
		"7000x7000 bomb": bombPNG(t, 7000, 7000),
		// The same limits for a WebP header (VP8X, 24-bit sizes).
		"webp 16000x16000": webpDeclaring(16000, 16000),
	}
	for name, data := range cases {
		if err := CheckImageDimensions(data); !errors.Is(err, ErrImageTooLarge) {
			t.Errorf("%s (%d bytes): got %v, want ErrImageTooLarge", name, len(data), err)
		}
	}
}

func TestTheLimitsComeFromTheEnvironment(t *testing.T) {
	t.Setenv("IMAGE_MAX_SIDE", "500")
	t.Setenv("IMAGE_MAX_PIXELS", "100000")
	if err := CheckImageDimensions(pngPhoto(t, 640, 480)); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("640x480 under a 500px limit: %v", err)
	}
	if err := CheckImageDimensions(pngPhoto(t, 300, 300)); err != nil {
		t.Errorf("300x300 under a 500px limit: %v", err)
	}
}

func TestMalformedAndNonImagesAreNotDecodable(t *testing.T) {
	truncated := bombPNG(t, 64, 64)[:20]
	for name, data := range map[string][]byte{
		"truncated png": truncated,
		"garbage jpeg":  append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("not a jpeg"), 40)...),
		"html":          []byte("<!doctype html><script>alert(1)</script>"),
		"empty":         nil,
	} {
		if err := CheckImageDimensions(data); !errors.Is(err, ErrNotDecodable) {
			t.Errorf("%s: got %v, want ErrNotDecodable", name, err)
		}
	}
}

// The decoders behind the check: a bomb is refused before any pixel is
// decoded, quickly, and real images still resize.
func TestResizeRefusesABombAndStillResizesPhotographs(t *testing.T) {
	s := &ImageService{}
	bomb := bombPNG(t, 7000, 7000)

	if _, _, _, err := s.ImagickResizeWithDimensions(bomb, 200, 0); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("stdlib resize of a bomb: %v", err)
	}
	if _, err := s.ResizeImage(bomb, 200, 0); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("ImageMagick resize of a bomb: %v", err)
	}
	if _, err := s.ProcessImage(bomb); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("ProcessImage of a bomb: %v", err)
	}

	path := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(path, bomb, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ImagickResizeFile(path, path+".out", 200, 0); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("file resize of a bomb: %v", err)
	}

	// The size of a bomb is still readable for a response header; that is a
	// header read, not a decode.
	if err, w, h := s.ImagickGetWidthHeight(bomb); err != nil || w != 7000 || h != 7000 {
		t.Errorf("ping of a bomb: %v %dx%d", err, w, h)
	}

	for name, data := range map[string][]byte{
		"jpeg": jpegPhoto(t, 1200, 800),
		"png":  pngPhoto(t, 640, 480),
	} {
		out, w, h, err := s.ImagickResizeWithDimensions(data, 200, 0)
		if err != nil || w != 200 || len(out) == 0 {
			t.Errorf("%s: resize to 200 wide: %v (%dx%d)", name, err, w, h)
		}
	}
}

// Asking for something enormous is not a way around the limits: output is
// never larger than the source.
func TestAnExcessiveResizeRequestIsCappedAtTheSource(t *testing.T) {
	s := &ImageService{}
	_, w, h, err := s.ImagickResizeWithDimensions(pngPhoto(t, 640, 480), 100000, 100000)
	if err != nil || w != 640 || h != 480 {
		t.Errorf("got %dx%d, %v; want the 640x480 source unchanged", w, h, err)
	}
}

func TestImagickResourceLimitsAreApplied(t *testing.T) {
	ensureImagickInitialized()
	if area := imagick.GetResourceLimit(imagick.RESOURCE_AREA); area <= 0 || area > 40_000_000 {
		t.Errorf("area limit %d, want at most 40 megapixels", area)
	}
	if mem := imagick.GetResourceLimit(imagick.RESOURCE_MEMORY); mem <= 0 || mem > 512<<20 {
		t.Errorf("memory limit %d, want at most 512 MiB", mem)
	}
	if disk := imagick.GetResourceLimit(imagick.RESOURCE_DISK); disk <= 0 || disk > 1<<30 {
		t.Errorf("disk limit %d, want at most 1 GiB", disk)
	}
}
