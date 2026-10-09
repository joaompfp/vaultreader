// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http/httptest"
	"testing"
)

func TestUserFrom(t *testing.T) {
	for _, c := range []struct{ hdr, want string }{
		{"alice", "alice"},
		{"  joao  ", "joao"},
		{"", "unknown"},
		{"   ", "unknown"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.hdr != "" {
			r.Header.Set("Remote-User", c.hdr)
		}
		if got := userFrom(r); got != c.want {
			t.Errorf("userFrom(%q) = %q, want %q", c.hdr, got, c.want)
		}
	}
}

func TestRequireUser(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}, RequireUser: true})
	h := s.handler()
	user := map[string]string{"Remote-User": "alice"}
	blank := map[string]string{"Remote-User": "   "}

	cases := []struct {
		path    string
		headers map[string]string
		want    int
	}{
		{"/api/vaults", nil, 401},
		{"/api/vaults", blank, 401},
		{"/api/vaults", user, 200},
		{"/", nil, 401},
		{"/health", nil, 200},
		{"/share/doesnotexist", nil, 404},                 // exempt → reaches the share handler
		{"/share/tok/asset?name=katex.min.css", nil, 404}, // exempt (token unknown → 404, not 401)
		{"/share", nil, 401},                              // not the /share/ prefix
		{"/sharefoo", nil, 401},
	}
	for _, c := range cases {
		if rec := do(t, h, "GET", c.path, "", c.headers); rec.Code != c.want {
			t.Errorf("GET %s %v = %d, want %d", c.path, c.headers, rec.Code, c.want)
		}
	}
}

func TestRequireUserOffByDefault(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	if rec := do(t, s.handler(), "GET", "/api/vaults", "", nil); rec.Code != 200 {
		t.Fatalf("default instance must not require a user, got %d", rec.Code)
	}
}

// Pins path-shape and method edge cases of the require_user gate.
func TestRequireUserEdgeCases(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}, RequireUser: true})
	h := s.handler()
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "//api/vaults", 401},
		{"GET", "/share/../api/vaults", 301}, // ServeMux cleans to /api/vaults via redirect, never serves it
		{"POST", "/api/vaults", 401},
		{"PUT", "/api/note?vault=jll&path=a.md", 401},
	} {
		if rec := do(t, h, c.method, c.path, "", nil); rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}
