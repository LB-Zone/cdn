package service

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Named size presets (lb-zone M2-CDN-2).
//
// The apps must not embed raw width literals: a product card asks for the
// `medium` preset and the product gallery asks for `xl`, so a design change is a
// change here rather than a sweep through two client codebases.
//
//	GET /:bucket/s:medium/brands/<sellerId>/products/<uuid>.jpg
//	GET /:bucket/s:xl/brands/<sellerId>/products/<uuid>.jpg
//
// Presets are expressed as a width only. The existing resize path preserves the
// aspect ratio and never upscales (the scale factor is capped at 1.0), so a
// preset is an upper bound: a 300 px original stays 300 px wide under `s:medium`.
//
// Widths are chosen for the mocks at a 2–3× device pixel ratio:
//
//	medium — 400 px. Product cards render ~150–170 pt wide in the 2-up grid on
//	         `ui/Home.png` and `ui/Brand profile.png`; 400 px covers that at 2.5×
//	         without shipping gallery-sized bytes down a mobile connection.
//	xl     — 1080 px. The gallery on `ui/Product detail.png` is full-bleed, so on
//	         a 375 pt viewport at 3× the useful ceiling is ~1125 px; 1080 is the
//	         nearest standard width and matches the 2000 px processing cap with
//	         room to spare.
const (
	PresetMedium = "medium"
	PresetXL     = "xl"
)

// Preset is a named target size.
type Preset struct {
	Name  string
	Width uint
	// Height stays 0: presets bound the width and let the aspect ratio decide
	// the height, which is what both a card and a gallery want.
	Height uint
}

var presets = map[string]Preset{
	PresetMedium: {Name: PresetMedium, Width: 400},
	PresetXL:     {Name: PresetXL, Width: 1080},
}

// LookupPreset resolves a preset name. Unknown names are rejected rather than
// silently falling back, so a typo in a client shows up immediately as an
// unresized image instead of a wrong-sized one shipped to production.
func LookupPreset(name string) (Preset, bool) {
	preset, ok := presets[strings.ToLower(strings.TrimSpace(name))]
	return preset, ok
}

// Presets returns every preset, for the docs endpoint and tests.
func Presets() map[string]Preset {
	out := make(map[string]Preset, len(presets))
	for name, preset := range presets {
		out[name] = preset
	}
	return out
}

// GetPresetDimensions reads the `:preset` route parameter and resolves it.
func GetPresetDimensions(c *fiber.Ctx) (bool, uint, uint) {
	name := c.Params("preset")
	if name == "" {
		return false, 0, 0
	}

	preset, ok := LookupPreset(name)
	if !ok {
		return false, 0, 0
	}

	return true, preset.Width, preset.Height
}
