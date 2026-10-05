package service

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"io"
	"os"

	"github.com/mstgnz/cdn/pkg/config"
	"gopkg.in/gographics/imagick.v3/imagick"
)

// Every decode is bounded by the dimensions an image declares, not by its
// size on disk. A PNG of a few hundred kilobytes can declare 30000x30000
// pixels; decoding that takes gigabytes, and a single upload of one used to
// get the cdn OOM-killed. So the declared size is read from the header first
// (no pixels are decoded) and anything over these limits is refused before a
// decoder sees it.
//
// The defaults leave room for any real product photograph: 40 megapixels is
// 8000x5000, and no side may exceed 10000.

// ErrImageTooLarge is returned for an image whose declared dimensions exceed
// the limits.
var ErrImageTooLarge = errors.New("image dimensions exceed the limit")

// ErrNotDecodable is returned when no header can be read at all.
var ErrNotDecodable = errors.New("not a decodable image")

// ImageLimits are the bounds every decode is checked against.
type ImageLimits struct {
	MaxSide   uint
	MaxPixels uint64
}

// CurrentImageLimits reads IMAGE_MAX_SIDE and IMAGE_MAX_PIXELS.
func CurrentImageLimits() ImageLimits {
	side := config.GetEnvAsIntOrDefault("IMAGE_MAX_SIDE", 10000)
	pixels := config.GetEnvAsIntOrDefault("IMAGE_MAX_PIXELS", 40_000_000)
	if side <= 0 {
		side = 10000
	}
	if pixels <= 0 {
		pixels = 40_000_000
	}
	return ImageLimits{MaxSide: uint(side), MaxPixels: uint64(pixels)}
}

func (l ImageLimits) check(width, height uint) error {
	if width == 0 || height == 0 {
		return ErrNotDecodable
	}
	if width > l.MaxSide || height > l.MaxSide || uint64(width)*uint64(height) > l.MaxPixels {
		return fmt.Errorf("%w: %dx%d (at most %d per side and %d pixels)", ErrImageTooLarge, width, height, l.MaxSide, l.MaxPixels)
	}
	return nil
}

// CheckImageDimensions refuses an image whose header declares more pixels
// than the limits allow, without decoding any of them.
func CheckImageDimensions(data []byte) error {
	width, height, err := declaredDimensions(data)
	if err != nil {
		return err
	}
	return CurrentImageLimits().check(width, height)
}

// CheckImageFileDimensions is CheckImageDimensions for a file on disk.
func CheckImageFileDimensions(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// The headers of every format checked here sit in the first few
	// kilobytes; a JPEG with large metadata before its frame header may need
	// more, which the ImageMagick ping below reads from the file itself.
	head := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, head)
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(head[:n])); err == nil {
		return CurrentImageLimits().check(uint(cfg.Width), uint(cfg.Height))
	}

	width, height, err := pingFile(path)
	if err != nil {
		return err
	}
	return CurrentImageLimits().check(width, height)
}

// declaredDimensions reads only the header: Go's own decoders for JPEG, PNG
// and GIF, the WebP header directly, ImageMagick's ping for the rest (BMP,
// ICO).
func declaredDimensions(data []byte) (uint, uint, error) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return uint(cfg.Width), uint(cfg.Height), nil
	}
	if width, height, ok := webpDimensions(data); ok {
		return width, height, nil
	}
	return pingBlob(data)
}

// webpDimensions reads the canvas size from a WebP header. Read here rather
// than through golang.org/x/image/webp, whose import would register a WebP
// decoder for image.Decode and change what the resize path does with WebP.
// https://developers.google.com/speed/webp/docs/riff_container
func webpDimensions(data []byte) (uint, uint, bool) {
	if len(data) < 30 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	le24 := func(b []byte) uint { return uint(b[0]) | uint(b[1])<<8 | uint(b[2])<<16 }
	switch string(data[12:16]) {
	case "VP8X": // extended: 24-bit canvas width-1 and height-1
		return le24(data[24:27]) + 1, le24(data[27:30]) + 1, true
	case "VP8L": // lossless: signature 0x2f, then 14-bit width-1 and height-1
		if data[20] != 0x2f {
			return 0, 0, false
		}
		bits := uint(data[21]) | uint(data[22])<<8 | uint(data[23])<<16 | uint(data[24])<<24
		return bits&0x3fff + 1, (bits>>14)&0x3fff + 1, true
	case "VP8 ": // lossy: start code 9d 01 2a, then 14-bit width and height
		if data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, false
		}
		return (uint(data[26]) | uint(data[27])<<8) & 0x3fff, (uint(data[28]) | uint(data[29])<<8) & 0x3fff, true
	}
	return 0, 0, false
}

func pingBlob(data []byte) (uint, uint, error) {
	if len(data) == 0 {
		return 0, 0, ErrNotDecodable
	}
	ensureImagickInitialized()
	mw := imagick.NewMagickWand()
	defer mw.Destroy()
	if err := mw.PingImageBlob(data); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrNotDecodable, err)
	}
	return mw.GetImageWidth(), mw.GetImageHeight(), nil
}

func pingFile(path string) (uint, uint, error) {
	ensureImagickInitialized()
	mw := imagick.NewMagickWand()
	defer mw.Destroy()
	if err := mw.PingImage(path); err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrNotDecodable, err)
	}
	return mw.GetImageWidth(), mw.GetImageHeight(), nil
}

// applyImagickResourceLimits caps ImageMagick for the whole process, as a
// second line behind the header check: what one decode may hold in memory,
// map and spill to disk, how many pixels it may have, and how long it may
// run. A limit can only be lowered from here, never raised above what the
// image's policy.xml allows.
func applyImagickResourceLimits() {
	limits := CurrentImageLimits()
	mw := imagick.NewMagickWand()
	defer mw.Destroy()

	const mib = 1 << 20
	for _, l := range []struct {
		resource imagick.ResourceType
		value    int64
	}{
		{imagick.RESOURCE_AREA, int64(limits.MaxPixels)},
		{imagick.RESOURCE_MEMORY, int64(config.GetEnvAsIntOrDefault("IMAGICK_MEMORY_MIB", 512)) * mib},
		{imagick.RESOURCE_MAP, int64(config.GetEnvAsIntOrDefault("IMAGICK_MAP_MIB", 1024)) * mib},
		{imagick.RESOURCE_DISK, int64(config.GetEnvAsIntOrDefault("IMAGICK_DISK_MIB", 1024)) * mib},
		{imagick.RESOURCE_TIME, int64(config.GetEnvAsIntOrDefault("IMAGICK_TIME_SECONDS", 60))},
	} {
		_ = mw.SetResourceLimit(l.resource, l.value)
	}
}
