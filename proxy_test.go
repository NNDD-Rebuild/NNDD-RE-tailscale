package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckUpstream(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:12345", "http://[::1]:5"} {
		if _, err := checkUpstream(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://192.168.1.5:80", "http://example.com:80", "http://localhost:1", "https://127.0.0.1:1", "http://127.0.0.1", "://x"} {
		if _, err := checkUpstream(bad); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
}

func TestProxyHeadersAndForwarding(t *testing.T) {
	var got http.Header
	var gotHost, gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		gotHost = r.Host
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello")
	}))
	defer up.Close()
	u, _ := checkUpstream(strings.Replace(up.URL, "127.0.0.1", "127.0.0.1", 1))

	whois := func(addr string) (*Identity, error) {
		return &Identity{Login: "alice@example.com", Name: "Alice", Node: "phone"}, nil
	}
	h := newProxy(u, "s3cret-value-1234", "https", whois)
	front := httptest.NewServer(h)
	defer front.Close()

	req, _ := http.NewRequest("GET", front.URL+"/library?x=1", nil)
	req.Host = "pc.tail1234.ts.net"
	// なりすましのヘッダー
	req.Header.Set("Tailscale-User-Login", "evil@example.com")
	req.Header.Set("tailscale-extra", "x")
	req.Header.Set(secretHeader, "guess")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "hello" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if gotPath != "/library?x=1" {
		t.Errorf("path=%q", gotPath)
	}
	if gotHost != "pc.tail1234.ts.net" {
		t.Errorf("host not preserved: %q", gotHost)
	}
	if got.Get("Tailscale-User-Name") != "" {
		t.Errorf("display name must not be forwarded: %q", got.Get("Tailscale-User-Name"))
	}
	if got.Get("Tailscale-User-Login") != "alice@example.com" {
		t.Errorf("login=%q", got.Get("Tailscale-User-Login"))
	}
	if got.Get("Tailscale-Extra") != "" {
		t.Errorf("client tailscale-* header leaked")
	}
	if got.Get(secretHeader) != "s3cret-value-1234" {
		t.Errorf("secret=%q", got.Get(secretHeader))
	}
	if got.Get("X-Forwarded-Proto") != "https" || got.Get("X-Forwarded-For") == "" {
		t.Errorf("forwarded headers: %v", got)
	}
}

func TestProxyRejectsUnknownPeer(t *testing.T) {
	called := false
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer up.Close()
	u, _ := checkUpstream(up.URL)
	h := newProxy(u, "secret-secret-secret", "http", func(string) (*Identity, error) { return nil, errors.New("unknown") })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("code=%d called=%v", rec.Code, called)
	}
}

func TestProxyBadGateway(t *testing.T) {
	u, _ := checkUpstream("http://127.0.0.1:1") // 閉じたポート
	h := newProxy(u, "secret-secret-secret", "http", func(string) (*Identity, error) { return &Identity{}, nil })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code=%d", rec.Code)
	}
}
