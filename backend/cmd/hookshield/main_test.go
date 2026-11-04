package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	cases := []struct {
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{nil, 0, "Usage:", ""},
		{[]string{"help"}, 0, "Commands:", ""},
		{[]string{"version"}, 0, "hookshield dev", ""},
		{[]string{"listen"}, 2, "", "--forward is required"},
		{[]string{"listen", "ep_x", "--forward", "ftp://x"}, 2, "", "not an http(s) URL"},
		{[]string{"trigger"}, 2, "", "which endpoint?"},
		{[]string{"trigger", "ep_x", "-H", "nocolon"}, 2, "", "use \"Name: value\""},
		{[]string{"login", "--bogus"}, 2, "", "flag provided but not defined"},
		{[]string{"nope"}, 2, "", `unknown command "nope"`},
		{[]string{"sink", "--status", "42"}, 2, "", "valid HTTP status"},
		{[]string{"sink", "--bogus"}, 2, "", "flag provided but not defined"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(context.Background(), tc.args, &out, &errOut)
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
			if !strings.Contains(out.String(), tc.wantStdout) {
				t.Errorf("stdout %q missing %q", out.String(), tc.wantStdout)
			}
			if !strings.Contains(errOut.String(), tc.wantStderr) {
				t.Errorf("stderr %q missing %q", errOut.String(), tc.wantStderr)
			}
		})
	}
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestSinkServesAndStops(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	var out bytes.Buffer
	go func() {
		done <- run(ctx, []string{"sink", "--addr", addr, "--fail-first", "1", "--quiet"}, &out, io.Discard)
	}()

	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = http.Post("http://"+addr+"/webhook", "application/json", strings.NewReader(`{}`))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("first request: %d, want 503", resp.StatusCode)
	}
	resp, _ = http.Post("http://"+addr+"/webhook", "application/json", strings.NewReader(`{}`))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("second request: %d", resp.StatusCode)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sink did not stop")
	}
}
