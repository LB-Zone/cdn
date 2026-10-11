package service

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicHTTPClientRefusesInternalHosts(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer internal.Close()

	if _, err := PublicHTTPClient(time.Second).Get(internal.URL); err == nil {
		t.Fatal("fetched a loopback URL")
	}
	for _, ip := range []string{"10.0.0.1", "172.18.0.5", "192.168.1.1", "169.254.169.254", "::1", "0.0.0.0"} {
		if IsPublicIP(net.ParseIP(ip)) {
			t.Errorf("%s treated as public", ip)
		}
	}
	if !IsPublicIP(net.ParseIP("8.8.8.8")) {
		t.Error("8.8.8.8 treated as private")
	}
}
