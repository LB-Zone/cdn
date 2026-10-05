package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// checkWith runs CheckToken for one Authorization header under one server token.
func checkWith(t *testing.T, serverToken, header string) error {
	t.Helper()
	t.Setenv("TOKEN", serverToken)

	app := fiber.New()
	var result error
	app.Get("/", func(c *fiber.Ctx) error {
		result = CheckToken(c)
		return nil
	})
	req := httptest.NewRequest("GET", "/", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCheckTokenFailsClosedWithoutAServerToken(t *testing.T) {
	// The bypass: `Bearer ` plus a whitespace the header parser keeps but
	// TrimSpace removes, compared with an empty server token.
	for _, header := range []string{"", "Bearer", "Bearer  ", "Bearer \u0085", "Bearer x"} {
		if err := checkWith(t, "", header); err == nil {
			t.Errorf("TOKEN unset: %q was authorised", header)
		}
		if err := checkWith(t, "   ", header); err == nil {
			t.Errorf("TOKEN blank: %q was authorised", header)
		}
	}
}

func TestCheckTokenAcceptsOnlyTheExactToken(t *testing.T) {
	const secret = "s3cret-token"
	cases := map[string]bool{
		"":                      false,
		"Bearer":                false,
		"Bearer  ":              false,
		"Bearer wrong":          false,
		"Bearer s3cret-toke":    false,
		"Bearer s3cret-token2":  false,
		"Basic s3cret-token":    false,
		"s3cret-token":          false,
		"Bearer s3cret-token":   true,
		"bearer s3cret-token":   true,
		"Bearer  s3cret-token ": true,
	}
	for header, want := range cases {
		if got := checkWith(t, secret, header) == nil; got != want {
			t.Errorf("%q: authorised=%v, want %v", header, got, want)
		}
	}
}

func TestUsableServerToken(t *testing.T) {
	for token, want := range map[string]bool{"": false, "  ": false, "CHANGE_ME": false, "real-secret": true} {
		t.Setenv("TOKEN", token)
		if got := UsableServerToken(); got != want {
			t.Errorf("TOKEN=%q: usable=%v, want %v", token, got, want)
		}
	}
}
