package service

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// PublicHTTPClient fetches caller-supplied URLs. It refuses to connect to
// loopback, private, link-local or unspecified addresses, checked on the
// resolved IP at dial time so DNS tricks and redirects cannot reach minio,
// valkey or the cloud metadata endpoint.
func PublicHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !IsPublicIP(ip) {
				return fmt.Errorf("refusing to fetch from non-public address %s", host)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext, Proxy: nil},
	}
}

func IsPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast())
}
