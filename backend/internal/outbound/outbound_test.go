package outbound

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestIsBlocked(t *testing.T) {
	blockedIPs := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "224.0.0.1", "255.255.255.255", "::1", "fd00::1", "fe80::1", "::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
		// security review: IPv6 forms that reach IPv4 or local networks
		"64:ff9b:1::a00:1", "2002:a00:1::1", "2001:0:4136:e378::1", "100::1",
	}
	for _, s := range blockedIPs {
		if !IsBlocked(netip.MustParseAddr(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "172.32.0.1", "2606:4700:4700::1111"} {
		if IsBlocked(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
	if !IsBlocked(netip.Addr{}) {
		t.Error("invalid address must be blocked")
	}
}

func TestAllowedPort(t *testing.T) {
	for _, p := range []int{80, 443, 1024, 3000, 8080, 8443, 65535} {
		if !AllowedPort(p) {
			t.Errorf("port %d must be allowed", p)
		}
	}
	for _, p := range []int{0, 22, 25, 110, 444, 1023, 65536} {
		if AllowedPort(p) {
			t.Errorf("port %d must be blocked", p)
		}
	}
}

func TestClientBlocksPrivateDestinations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()

	_, err := NewClient(Options{}).Get(srv.URL) // 127.0.0.1
	if !errors.Is(err, ErrBlockedDestination) || ErrorCode(err) != "blocked_destination" {
		t.Fatalf("loopback destination not blocked: %v (%s)", err, ErrorCode(err))
	}

	resp, err := NewClient(Options{AllowPrivate: true}).Get(srv.URL)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("AllowPrivate must permit local targets: %v", err)
	}
	resp.Body.Close()
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redir.Close()

	resp, err := NewClient(Options{AllowPrivate: true}).Get(redir.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || followed {
		t.Fatalf("redirect followed (status %d)", resp.StatusCode)
	}
}

func TestErrorCodes(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	_, err := NewClient(Options{AllowPrivate: true, Timeout: 50 * time.Millisecond}).Get(slow.URL)
	if ErrorCode(err) != "timeout" {
		t.Errorf("timeout classified as %q (%v)", ErrorCode(err), err)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := closed.URL
	closed.Close()
	_, err = NewClient(Options{AllowPrivate: true}).Get(url)
	if ErrorCode(err) != "connection_refused" {
		t.Errorf("refused classified as %q (%v)", ErrorCode(err), err)
	}

	_, err = NewClient(Options{AllowPrivate: true}).Get("http://does-not-exist.invalid")
	if ErrorCode(err) != "dns" {
		t.Errorf("dns classified as %q (%v)", ErrorCode(err), err)
	}

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsSrv.Close()
	_, err = NewClient(Options{AllowPrivate: true}).Get(tlsSrv.URL) // self-signed
	if ErrorCode(err) != "tls" {
		t.Errorf("tls classified as %q (%v)", ErrorCode(err), err)
	}

	if ErrorCode(nil) != "" || ErrorCode(context.Canceled) != "network" {
		t.Error("nil / generic classification")
	}
}
