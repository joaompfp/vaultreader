package main

import (
	"strings"
	"testing"
)

func TestSPAShellSecurityHeaders(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	h := s.handler()
	for _, target := range []string{"/", "/n/x/y.md"} {
		rec := do(t, h, "GET", target, "", nil)
		if rec.Code != 200 {
			t.Fatalf("%s = %d", target, rec.Code)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != spaCSP {
			t.Fatalf("%s CSP = %q", target, got)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s nosniff = %q", target, got)
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Fatalf("%s Referrer-Policy = %q", target, got)
		}
	}
	rec := do(t, h, "GET", "/api/vaults", "", nil)
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Fatalf("/api/vaults must not carry the shell CSP, got %q", got)
	}
}

func TestSPACSPDirectives(t *testing.T) {
	for _, d := range []string{"connect-src 'self'", "object-src 'none'", "base-uri 'self'", "form-action 'self'", "frame-ancestors 'self'", "img-src 'self' data: blob: https:"} {
		if !strings.Contains(spaCSP, d) {
			t.Fatalf("spaCSP missing %q", d)
		}
	}
}
