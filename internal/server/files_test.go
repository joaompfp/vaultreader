// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Raw vault files are attacker-controlled on the team instance: an .svg or
// .html served same-origin would run script with the owner's session.
func TestRawFileHeaders(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	for _, n := range []string{"a.svg", "a.html", "a.htm", "a.xhtml", "a.xml", "a.png", "a.pdf"} {
		writeFile(t, filepath.Join(s.vaultsDir, "team", n), "x")
	}
	writeFile(t, filepath.Join(s.vaultsDir, "team", "evil.svg"), "x")
	share := s.shares.create("team", "n.md", false, 0, "", "alice")
	direct := s.shares.create("team", "evil.svg", false, 0, "", "alice")
	h := s.handler()

	cases := []struct {
		name, url string
		sandbox   bool
	}{
		{"api svg", "/api/file?vault=team&path=a.svg", true},
		{"api html", "/api/file?vault=team&path=a.html", true},
		{"api htm", "/api/file?vault=team&path=a.htm", true},
		{"api xhtml", "/api/file?vault=team&path=a.xhtml", true},
		{"api xml", "/api/file?vault=team&path=a.xml", true},
		{"api png", "/api/file?vault=team&path=a.png", false},
		{"api pdf", "/api/file?vault=team&path=a.pdf", false},
		{"share file svg", "/share/" + share.Token + "/file?path=a.svg", true},
		{"share file png", "/share/" + share.Token + "/file?path=a.png", false},
		{"share file pdf", "/share/" + share.Token + "/file?path=a.pdf", false},
		{"share of svg file", "/share/" + direct.Token, true},
	}
	for _, c := range cases {
		rec := do(t, h, "GET", c.url, "", nil)
		if rec.Code != 200 {
			t.Errorf("%s: code %d", c.name, rec.Code)
			continue
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", c.name, got)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if c.sandbox && csp != "sandbox" {
			t.Errorf("%s: CSP = %q, want sandbox", c.name, csp)
		}
		if !c.sandbox && strings.Contains(csp, "sandbox") {
			t.Errorf("%s: CSP = %q, want no sandbox", c.name, csp)
		}
	}
}

// The share page inlines note HTML server-side, so script in a note must
// not run there: only the page's own nonce'd scripts are allowed.
func TestSharePageCSP(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	writeFile(t, filepath.Join(s.vaultsDir, "team", "n.md"),
		"# <img src=x onerror=alert(1)>\n\n<script>window.x=1</script>\n\n```mermaid\ngraph TD; A-->B\n```\n\n$$x$$\n")
	e := s.shares.create("team", "n.md", false, 0, "", "alice")
	rec := do(t, s.handler(), "GET", "/share/"+e.Token, "", nil)
	if rec.Code != 200 {
		t.Fatalf("share page = %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	m := regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9+/=_-]+)'`).FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("CSP %q lacks a script-src nonce", csp)
	}
	if !strings.Contains(csp, "object-src 'none'") || !strings.Contains(csp, "base-uri 'self'") {
		t.Errorf("CSP %q missing object-src/base-uri", csp)
	}
	body := rec.Body.String()
	nonced := strings.Count(body, `<script nonce="`+m[1]+`"`)
	all := strings.Count(body, "<script")
	if nonced == 0 || all-nonced != 1 { // exactly one un-nonced script: the note's own
		t.Errorf("scripts: %d total, %d with page nonce; want all but the note's", all, nonced)
	}
	if strings.Contains(body, "<title><img") || strings.Contains(body, "<strong><img") {
		t.Errorf("title not escaped")
	}
}

// A legacy writable share on an instance with share_writable off is served
// read-only (PUT 403s), so the page must not advertise it as editable.
func TestSharePageBadgeHonoursShareWritable(t *testing.T) {
	for _, c := range []struct {
		features map[string]bool
		want     string
	}{
		{nil, "Editable"},
		{map[string]bool{"share_writable": false}, "Read-only"},
	} {
		s := newTestServer(t, AdminConfig{Features: c.features})
		writeFile(t, filepath.Join(s.vaultsDir, "team", "n.md"), "# N\n")
		e := s.shares.create("team", "n.md", true, 0, "", "alice")
		body := do(t, s.handler(), "GET", "/share/"+e.Token, "", nil).Body.String()
		if !strings.Contains(body, `">`+c.want+`</span>`) {
			t.Errorf("features %v: badge is not %q", c.features, c.want)
		}
	}
}

// Every raw-file response must carry a Content-Type we chose: without one
// http.ServeFile sniffs, and a .md / extensionless file starting with
// `<!--` or `<div` would be served as text/html on the app origin.
func TestRawFileContentType(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	dir := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(dir, "evil.md"), "<!-- --><script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "noext"), "<div><script>alert(1)</script></div>")
	writeFile(t, filepath.Join(dir, "evil.txt"), "<html><script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "a.unknownext"), "<script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "a.html"), "<script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "a.svg"), "<svg xmlns='http://www.w3.org/2000/svg'/>")
	writeFile(t, filepath.Join(dir, "a.png"), "<html><script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "a.pdf"), "%PDF-1.4")
	writeFile(t, filepath.Join(dir, "a.mp4"), "x")
	share := s.shares.create("team", "n.md", false, 0, "", "alice")
	direct := s.shares.create("team", "evil.txt", false, 0, "", "alice")
	h := s.handler()

	cases := []struct {
		name, url, ct string
		sandbox       bool
	}{
		{"api md", "/api/file?vault=team&path=evil.md", "text/plain; charset=utf-8", true},
		{"api noext", "/api/file?vault=team&path=noext", "application/octet-stream", true},
		{"api txt", "/api/file?vault=team&path=evil.txt", "text/plain; charset=utf-8", true},
		{"api unknown", "/api/file?vault=team&path=a.unknownext", "application/octet-stream", true},
		{"api html", "/api/file?vault=team&path=a.html", "text/plain; charset=utf-8", true},
		{"api svg", "/api/file?vault=team&path=a.svg", "image/svg+xml", true},
		{"api png", "/api/file?vault=team&path=a.png", "image/png", false},
		{"api pdf", "/api/file?vault=team&path=a.pdf", "application/pdf", false},
		{"api mp4", "/api/file?vault=team&path=a.mp4", "video/mp4", false},
		{"share file svg", "/share/" + share.Token + "/file?path=a.svg", "image/svg+xml", true},
		{"share file png", "/share/" + share.Token + "/file?path=a.png", "image/png", false},
		{"share file pdf", "/share/" + share.Token + "/file?path=a.pdf", "application/pdf", false},
		{"share of txt file", "/share/" + direct.Token, "text/plain; charset=utf-8", true},
	}
	for _, c := range cases {
		rec := do(t, h, "GET", c.url, "", nil)
		if rec.Code != 200 {
			t.Errorf("%s: code %d", c.name, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Type"); got != c.ct {
			t.Errorf("%s: Content-Type = %q, want %q", c.name, got, c.ct)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", c.name, got)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if c.sandbox && csp != "sandbox" {
			t.Errorf("%s: CSP = %q, want sandbox", c.name, csp)
		}
		if !c.sandbox && csp != "" {
			t.Errorf("%s: CSP = %q, want none", c.name, csp)
		}
	}
}

// A shared note must not be able to frame /api/file (or anything else).
func TestSharePageForbidsFrames(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	writeFile(t, filepath.Join(s.vaultsDir, "team", "n.md"), "<iframe src=\"/api/file?vault=team&path=x.md\"></iframe>\n")
	e := s.shares.create("team", "n.md", false, 0, "", "alice")
	csp := do(t, s.handler(), "GET", "/share/"+e.Token, "", nil).Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src 'none'") || !strings.Contains(csp, "child-src 'none'") {
		t.Errorf("share CSP %q lacks frame-src/child-src 'none'", csp)
	}
}
