// Package outbound builds the HTTP client used to deliver webhooks to
// customer destinations, with server-side request forgery (SSRF) protection.
//
// Destinations are user-controlled URLs, so without protection a user could
// make HookShield call internal services (databases, cloud metadata at
// 169.254.169.254, the Redis port…). The guard runs inside the dialer, on the
// IP address actually being connected to, so DNS tricks (a public name that
// resolves to 10.0.0.5, DNS rebinding) are blocked too. Redirects are never
// followed: a 3xx response counts as a failed delivery.
package outbound

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrBlockedDestination is returned when the destination resolves to a
// private, loopback, link-local or otherwise non-public address.
var ErrBlockedDestination = errors.New("outbound: destination address is not allowed")

// Options configure the client.
type Options struct {
	Timeout      time.Duration // whole request, default 15 s
	AllowPrivate bool          // allow non-public destinations (development / self-hosted LAN)
}

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), // link-local, cloud metadata
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("224.0.0.0/4"),   // multicast
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, broadcast
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),       // unique local
	netip.MustParsePrefix("fe80::/10"),      // link-local
	netip.MustParsePrefix("ff00::/8"),       // multicast
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 (well-known)
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 (local use)
	netip.MustParsePrefix("100::/64"),       // discard
	netip.MustParsePrefix("2001::/32"),      // Teredo (embeds an IPv4 address)
	netip.MustParsePrefix("2002::/16"),      // 6to4 (embeds an IPv4 address)
}

// AllowedPort reports whether deliveries may go to port: 80, 443 and every
// unprivileged port. Privileged ports host other protocols (SSH, SMTP, …) a
// webhook has no business talking to.
func AllowedPort(port int) bool {
	return port == 80 || port == 443 || (port >= 1024 && port <= 65535)
}

// IsBlocked reports whether ip is not a public unicast address.
func IsBlocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// NewClient returns the delivery client.
func NewClient(opts Options) *http.Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if opts.AllowPrivate {
				return nil
			}
			host, portStr, err := net.SplitHostPort(address)
			if err != nil {
				return ErrBlockedDestination
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || IsBlocked(ip) {
				return ErrBlockedDestination
			}
			if port, err := strconv.Atoi(portStr); err != nil || !AllowedPort(port) {
				return ErrBlockedDestination
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil, // never route customer traffic through an environment proxy
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          512,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: opts.Timeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Timeout:   opts.Timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ErrorCode classifies a delivery error into a short, non-sensitive code
// stored in delivery_attempts.error_code and shown in the debugger.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	var netErr net.Error
	var tlsErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	switch {
	case errors.Is(err, ErrBlockedDestination):
		return "blocked_destination"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.As(err, &tlsErr), errors.As(err, &recErr), strings.Contains(err.Error(), "tls:"), strings.Contains(err.Error(), "x509:"):
		return "tls"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	}
	return "network"
}
