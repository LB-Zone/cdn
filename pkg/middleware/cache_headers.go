package middleware

import "github.com/gofiber/fiber/v2"

// ImageCacheControl lets a served image be held for a month and nothing else.
//
// Image paths are content-addressed (a new upload is a new UUID), so a 200 is
// safe to keep. The header used to be added at the edge to every cdn response,
// which made a 404 or a 429 just as "immutable": a shopper who hit the rate
// limit once could keep a broken image for thirty days.
func ImageCacheControl() fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := c.Next()
		if err == nil && c.Response().StatusCode() == fiber.StatusOK {
			c.Set(fiber.HeaderCacheControl, "public, max-age=2592000, immutable")
		} else {
			c.Set(fiber.HeaderCacheControl, "no-store")
		}
		return err
	}
}
