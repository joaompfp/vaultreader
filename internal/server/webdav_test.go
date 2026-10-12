// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"os"
	"path/filepath"
	"testing"
)

// WebDAV streams vault bytes on the main origin; a team-written .md or
// extensionless file starting with <div> must never be sniffed into HTML.
func TestWebDAVNoSniff(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	dir := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(dir, "x.md"), "<div>hi</div><script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "noext"), "<div>hi</div>")
	writeFile(t, filepath.Join(dir, "a.png"), "<html><script>alert(1)</script>")
	h := s.handler()

	for _, c := range []struct{ method, url, ct string }{
		{"GET", "/webdav/team/x.md", "text/plain; charset=utf-8"},
		{"HEAD", "/webdav/team/x.md", "text/plain; charset=utf-8"},
		{"GET", "/webdav/team/noext", "application/octet-stream"},
		{"GET", "/webdav/team/a.png", "image/png"},
		{"PROPFIND", "/webdav/team/", ""},
		{"PUT", "/webdav/team/y.md", ""},
	} {
		rec := do(t, h, c.method, c.url, "", nil)
		if c.ct != "" {
			if rec.Code != 200 {
				t.Errorf("%s %s: code %d", c.method, c.url, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != c.ct {
				t.Errorf("%s %s: Content-Type = %q, want %q", c.method, c.url, got, c.ct)
			}
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s %s: X-Content-Type-Options = %q", c.method, c.url, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox" {
			t.Errorf("%s %s: CSP = %q, want sandbox", c.method, c.url, got)
		}
	}
}

func TestVaultIconNoSniff(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	if err := os.MkdirAll(filepath.Join(s.appdataDir, "icons"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.appdataDir, "icons", "team.png"), "x")
	rec := do(t, s.handler(), "GET", "/api/vault-icon?vault=team", "", nil)
	if rec.Code != 200 || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("vault icon: code %d, nosniff %q", rec.Code, rec.Header().Get("X-Content-Type-Options"))
	}
}
