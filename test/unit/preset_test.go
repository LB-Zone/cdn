package unit

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mstgnz/cdn/service"
)

// The named presets are the contract the mobile app and the portal build image
// URLs from (lb-zone M2-CDN-2). Changing a width is a deliberate design decision,
// so it has to break a test.
func TestPresetWidths(t *testing.T) {
	medium, ok := service.LookupPreset(service.PresetMedium)
	require.True(t, ok)
	assert.Equal(t, uint(400), medium.Width, "cards use the medium preset")
	assert.Equal(t, uint(0), medium.Height, "presets bound the width and keep the aspect ratio")

	xl, ok := service.LookupPreset(service.PresetXL)
	require.True(t, ok)
	assert.Equal(t, uint(1080), xl.Width, "the product gallery uses the xl preset")
	assert.Equal(t, uint(0), xl.Height)

	assert.Greater(t, xl.Width, medium.Width)
}

func TestLookupPresetIsCaseInsensitiveAndStrict(t *testing.T) {
	_, ok := service.LookupPreset("  XL ")
	assert.True(t, ok, "the lookup trims and lowercases")

	_, ok = service.LookupPreset("huge")
	assert.False(t, ok, "an unknown preset must be rejected, not silently defaulted")

	_, ok = service.LookupPreset("")
	assert.False(t, ok)
}

// The route parameter is what actually reaches the handler, so resolve it the way
// the handler does.
func TestGetPresetDimensionsFromRoute(t *testing.T) {
	app := fiber.New()

	var (
		resized bool
		width   uint
		height  uint
	)
	app.Get("/:bucket/s::preset/*", func(c *fiber.Ctx) error {
		resized, width, height = service.GetPresetDimensions(c)
		return c.SendStatus(fiber.StatusOK)
	})

	cases := []struct {
		path        string
		wantResized bool
		wantWidth   uint
	}{
		{path: "/lbzone/s:medium/brands/x/products/y.jpg", wantResized: true, wantWidth: 400},
		{path: "/lbzone/s:xl/brands/x/products/y.jpg", wantResized: true, wantWidth: 1080},
		{path: "/lbzone/s:bogus/brands/x/products/y.jpg", wantResized: false, wantWidth: 0},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resized, width, height = false, 0, 0

			res, err := app.Test(httptest.NewRequest(fiber.MethodGet, tc.path, nil))
			require.NoError(t, err)
			require.Equal(t, fiber.StatusOK, res.StatusCode)

			assert.Equal(t, tc.wantResized, resized)
			assert.Equal(t, tc.wantWidth, width)
			assert.Equal(t, uint(0), height)
		})
	}
}

// No upscaling: a preset is an upper bound, so a small original is served at its
// own size rather than being blown up.
func TestPresetsNeverUpscale(t *testing.T) {
	const (
		originalWidth  = 300
		originalHeight = 200
	)

	xl, ok := service.LookupPreset(service.PresetXL)
	require.True(t, ok)

	width, height := service.RatioWidthHeight(originalWidth, originalHeight, xl.Width, xl.Height)

	assert.LessOrEqual(t, width, uint(xl.Width))
	assert.Equal(t, uint(originalWidth), width, "a 300px original must not be upscaled to 1080px")
	assert.Equal(t, uint(originalHeight), height)
}

func TestPublicReadPolicyGrantsOnlyReads(t *testing.T) {
	// The policy document is what makes a fresh environment serve images without
	// manual MinIO console configuration (lb-zone M2-CDN-3).
	document := service.PublicReadPolicyDocument("lbzone")

	var parsed struct {
		Version   string `json:"Version"`
		Statement []struct {
			Effect   string   `json:"Effect"`
			Action   []string `json:"Action"`
			Resource []string `json:"Resource"`
		} `json:"Statement"`
	}
	require.NoError(t, json.Unmarshal([]byte(document), &parsed))
	require.Len(t, parsed.Statement, 1)

	statement := parsed.Statement[0]
	assert.Equal(t, "Allow", statement.Effect)
	assert.Equal(t, []string{"s3:GetObject"}, statement.Action,
		"writes and deletes must stay token-gated")
	assert.Equal(t, []string{"arn:aws:s3:::lbzone/*"}, statement.Resource,
		"the grant covers objects only, so the bucket cannot be listed anonymously")
}

func TestPublicReadBucketsParsing(t *testing.T) {
	t.Setenv("PUBLIC_READ_BUCKETS", "lbzone, staging-images")
	assert.Equal(t, []string{"lbzone", "staging-images"}, service.PublicReadBuckets())

	t.Setenv("PUBLIC_READ_BUCKETS", "")
	assert.Equal(t, []string{"lbzone"}, service.PublicReadBuckets(), "the default is the image bucket")
}

// Downscaling still works for both preset sizes, and the aspect ratio is kept.
func TestPresetsDownscalePreservingAspectRatio(t *testing.T) {
	const (
		originalWidth  = 2400
		originalHeight = 3200
	)

	for name, expectedWidth := range map[string]uint{
		service.PresetMedium: 400,
		service.PresetXL:     1080,
	} {
		t.Run(name, func(t *testing.T) {
			preset, ok := service.LookupPreset(name)
			require.True(t, ok)

			width, height := service.RatioWidthHeight(
				originalWidth, originalHeight, preset.Width, preset.Height,
			)

			assert.Equal(t, expectedWidth, width)

			originalRatio := float64(originalWidth) / float64(originalHeight)
			resizedRatio := float64(width) / float64(height)
			assert.InDelta(t, originalRatio, resizedRatio, 0.01,
				"the aspect ratio must survive the resize")
		})
	}
}
