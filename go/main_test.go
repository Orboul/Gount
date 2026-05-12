package main

import (
	"net/http"
	"testing"
)

func TestRealIPIgnoresForwardedHeadersFromUntrustedClients(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/t", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "198.51.100.10:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.Header.Set("X-Real-IP", "203.0.113.51")

	got := realIP(req, nil)
	if got != "198.51.100.10" {
		t.Fatalf("realIP() = %q, want %q", got, "198.51.100.10")
	}
}

func TestRealIPUsesForwardedHeaderFromTrustedProxy(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/t", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "10.0.0.5:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.5")

	trusted, err := parseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("parseTrustedProxies(): %v", err)
	}

	got := realIP(req, trusted)
	if got != "203.0.113.50" {
		t.Fatalf("realIP() = %q, want %q", got, "203.0.113.50")
	}
}

func TestParseTrustedProxiesRejectsInvalidEntries(t *testing.T) {
	if _, err := parseTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("parseTrustedProxies() error = nil, want non-nil")
	}
}
