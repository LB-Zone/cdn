package middleware

import "github.com/gofiber/fiber/v2"

// SecurityHeaders is defence in depth for an origin that serves untrusted
// uploads. Even if something other than an image were ever sent:
//
//   - `nosniff` stops a browser second-guessing the declared type;
//   - `default-src 'none'; sandbox` gives any document rendered from this
//     origin no script, no plugins, no same-origin privileges and nothing it
//     may load.
//
// Neither changes how an <img> on another site renders the response: CSP
// applies to documents, and the images keep their declared image type.
func SecurityHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
		c.Set(fiber.HeaderContentSecurityPolicy, "default-src 'none'; sandbox")
		return c.Next()
	}
}
