package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mstgnz/cdn/pkg/config"
)

// defaultTrustedProxies are the private ranges a reverse proxy on the same
// host or container network answers from. Traefik in `deploy/compose.yaml` and
// nginx in `deploy/allinone` both reach the cdn from one of these.
const defaultTrustedProxies = "127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"

// ClientIPConfig makes `c.IP()` the visitor's address rather than the proxy's.
//
// The rate limiters key on `c.IP()`. Behind Traefik that was Traefik's own
// container address for every request, so the whole platform shared one
// 100-per-minute bucket: a handful of shoppers scrolling a product grid
// exhausted it and every image after that answered 429.
//
// The forwarded header is believed only when the connection comes from a
// trusted proxy (TRUSTED_PROXIES, comma separated IPs or CIDRs). A client that
// reaches the cdn directly cannot pick its own rate-limit key by sending the
// header itself.
func ClientIPConfig(cfg fiber.Config) fiber.Config {
	cfg.ProxyHeader = fiber.HeaderXForwardedFor
	cfg.EnableTrustedProxyCheck = true
	cfg.TrustedProxies = trustedProxies(config.GetEnvOrDefault("TRUSTED_PROXIES", defaultTrustedProxies))
	// With validation on, `c.IP()` returns the first valid address in the
	// header instead of the raw comma-separated value.
	cfg.EnableIPValidation = true
	return cfg
}

func trustedProxies(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
