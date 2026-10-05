package service

import (
	"strings"
	"testing"
)

var hostileRedisPasswords = []string{
	"qa/Pa+ss&wo#rd%41@x:y",
	"base64/like+this==",
	"p@ss:wo/rd?x=1&y=2#frag",
	"%zz%",
	"plain",
}

// The deploy stack used to put the password inside REDIS_URL unencoded. Any
// password must now reach the client intact through REDIS_PASSWORD, whatever
// it contains.
func TestRedisPasswordTravelsIntact(t *testing.T) {
	for _, password := range hostileRedisPasswords {
		t.Setenv("REDIS_URL", "redis://valkey:6379/0")
		t.Setenv("REDIS_PASSWORD", password)

		opt, err := RedisOptions()
		if err != nil {
			t.Fatalf("%q: %v", password, err)
		}
		if opt.Password != password || opt.Addr != "valkey:6379" || opt.DB != 0 {
			t.Errorf("%q: got password %q addr %q db %d", password, opt.Password, opt.Addr, opt.DB)
		}
	}
}

// A URL that does not parse must not put its contents in the error: the error
// is logged, and the old one quoted the URL with the password in it.
func TestABadRedisURLIsReportedWithoutItsValue(t *testing.T) {
	for _, password := range hostileRedisPasswords[:3] {
		t.Setenv("REDIS_URL", "redis://:"+password+"@valkey:6379")
		t.Setenv("REDIS_PASSWORD", "")

		_, err := RedisOptions()
		if err == nil {
			// Parsed after all; then it must not have lost the password silently.
			continue
		}
		msg := err.Error()
		for _, fragment := range []string{password, strings.SplitN(password, "#", 2)[0], "valkey"} {
			if strings.Contains(msg, fragment) {
				t.Errorf("%q: the error leaks %q: %s", password, fragment, msg)
			}
		}
	}
}

// Existing deployments with a plain password inside the URL keep working.
func TestAPasswordInTheURLStillWorksWithoutREDIS_PASSWORD(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://:plainsecret@valkey:6379")
	t.Setenv("REDIS_PASSWORD", "")

	opt, err := RedisOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opt.Password != "plainsecret" {
		t.Errorf("password %q", opt.Password)
	}
}
