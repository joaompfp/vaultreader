// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/tls"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnabledDefaultsToTrue(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	for _, f := range knownFeatures {
		if !s.enabled(f) {
			t.Errorf("%s should default to enabled", f)
		}
	}
	s.cfg.Features = map[string]bool{"webdav": false}
	if s.enabled("webdav") || !s.enabled("shares") {
		t.Fatal("explicit false must disable only that feature")
	}
}

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  AdminConfig
		ok   bool
	}{
		{"empty", AdminConfig{}, true},
		{"legacy rw_paths only", AdminConfig{RWPaths: []string{"notes/agents/skills"}}, true},
		{"known features", AdminConfig{Features: map[string]bool{"shares": true, "trash_empty": false}}, true},
		{"typo feature", AdminConfig{Features: map[string]bool{"share": false}}, false},
		{"default note ok", AdminConfig{DefaultNote: "notes/inbox/todo.md"}, true},
		{"default note no vault", AdminConfig{DefaultNote: "todo.md"}, false},
		{"base url ok", AdminConfig{ShareBaseURL: "https://team.example.com/share/"}, true},
		{"base url no slash", AdminConfig{ShareBaseURL: "https://team.example.com/share"}, false},
	}
	for _, c := range cases {
		if err := validateConfig(c.cfg); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestLoadSaveConfigRoundTrip(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	if err := os.WriteFile(filepath.Join(s.appdataDir, "config.json"), []byte(`{"rw_paths":["team"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s.loadConfig()
	if len(s.cfg.RWPaths) != 1 || !s.enabled("shares") || s.cfg.ShareBaseURL != "" {
		t.Fatalf("legacy config changed behaviour: %+v", s.cfg)
	}
	s.cfg.RequireUser = true
	s.cfg.ShareBaseURL = "https://team.example.com/share/"
	s.cfg.DefaultNote = "team/a.md"
	s.cfg.Features = map[string]bool{"webdav": false}
	if err := s.saveConfig(); err != nil {
		t.Fatal(err)
	}
	s.cfg = AdminConfig{}
	s.loadConfig()
	if !s.cfg.RequireUser || s.cfg.ShareBaseURL != "https://team.example.com/share/" || s.cfg.DefaultNote != "team/a.md" || s.enabled("webdav") {
		t.Fatalf("new fields not persisted: %+v", s.cfg)
	}
}

func TestDisabledFeatureRoutes404(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}, Features: map[string]bool{
		"shares": false, "webdav": false, "sync_status": false, "trash_empty": false,
	}})
	h := s.handler()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/shares"},
		{"POST", "/api/shares/create"},
		{"DELETE", "/api/shares/revoke?token=x"},
		{"DELETE", "/api/shares/revoke-all"},
		{"GET", "/share/abc"},
		{"PROPFIND", "/webdav/"},
		{"GET", "/api/sync-status"},
		{"DELETE", "/api/trash/empty?vault=team"},
	} {
		if rec := do(t, h, c.method, c.path, "", nil); rec.Code != 404 {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
	// Enabled neighbours still work.
	if rec := do(t, h, "GET", "/api/trash?vault=team", "", nil); rec.Code != 200 {
		t.Errorf("/api/trash = %d, want 200", rec.Code)
	}
}

func TestShareWritableDisabled(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}, Features: map[string]bool{"share_writable": false}})
	h := s.handler()
	rec := do(t, h, "POST", "/api/shares/create", `{"vault":"team","path":"a.md","writable":true}`, nil)
	if rec.Code != 403 {
		t.Fatalf("writable create = %d, want 403", rec.Code)
	}
	rec = do(t, h, "POST", "/api/shares/create", `{"vault":"team","path":"a.md","writable":false}`, nil)
	if rec.Code != 200 {
		t.Fatalf("read-only create = %d, want 200", rec.Code)
	}
	// A pre-existing writable share must not accept writes either.
	e := s.shares.create("team", "a.md", true, 0, "legacy", "test")
	rec = do(t, h, "PUT", "/share/"+e.Token, `{"raw":"x"}`, nil)
	if rec.Code != 403 {
		t.Fatalf("share PUT = %d, want 403", rec.Code)
	}
}

// A typo in config.json must fail startup instead of silently dropping a
// security setting (e.g. "require_usr" leaving the team instance open).
func TestParseConfigRejectsUnknownKeys(t *testing.T) {
	for _, c := range []struct {
		json string
		ok   bool
	}{
		{`{"rw_paths":["team"],"require_user":true,"features":{"webdav":false}}`, true},
		{`{"rw_paths":["team"],"require_usr":true}`, false},
		{`{"rw_paths":["team"],"features":{"share":false}}`, false}, // validateConfig still applies
		{`{"rw_paths":["team"]} {"x":1}`, false},                    // trailing data
	} {
		if _, err := parseConfig([]byte(c.json)); (err == nil) != c.ok {
			t.Errorf("parseConfig(%s): err=%v, want ok=%v", c.json, err, c.ok)
		}
	}
}

// With no share_base_url configured, the share prefix is derived from the
// request: scheme from TLS state or (only from a trusted proxy peer)
// X-Forwarded-Proto, host from the Host header.
func TestShareBaseURLDerivedFromRequest(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	cases := []struct {
		name, remote, proto string
		tls                 bool
		want                string
	}{
		{"direct plain http", "203.0.113.9:5000", "", false, "http://notes.example.com/share/"},
		{"direct TLS", "203.0.113.9:5000", "", true, "https://notes.example.com/share/"},
		{"proxy says https", "172.18.0.5:5000", "https", false, "https://notes.example.com/share/"},
		{"proxy says http", "10.0.0.2:5000", "http", true, "http://notes.example.com/share/"},
		{"untrusted peer cannot spoof proto", "203.0.113.9:5000", "https", false, "http://notes.example.com/share/"},
		{"bogus proto ignored", "172.18.0.5:5000", "javascript", false, "http://notes.example.com/share/"},
		{"proto list uses first hop", "172.18.0.5:5000", "https, http", false, "https://notes.example.com/share/"},
		{"IPv6 loopback proxy", "[::1]:5000", "https", false, "https://notes.example.com/share/"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "http://notes.example.com/api/instance", nil)
		r.RemoteAddr = c.remote
		if c.proto != "" {
			r.Header.Set("X-Forwarded-Proto", c.proto)
		}
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := s.shareBaseURL(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestShareBaseURLExplicitWins(t *testing.T) {
	s := newTestServer(t, AdminConfig{ShareBaseURL: "https://example.com/notes/"})
	r := httptest.NewRequest("GET", "http://notes.example.com/api/instance", nil)
	if got := s.shareBaseURL(r); got != "https://example.com/notes/" {
		t.Fatalf("got %q", got)
	}
}
