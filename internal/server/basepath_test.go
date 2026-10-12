// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBasePath(t *testing.T) {
	for _, c := range []struct {
		base string
		ok   bool
	}{
		{"", true},
		{"/vaultreader", true},
		{"/apps/vault-reader_2", true},
		{"vaultreader", false},   // must start with /
		{"/vaultreader/", false}, // no trailing /
		{"/", false},
		{"/a/../b", false},
		{"/..", false},
		{"/a//b", false},
		{"/a/./b", false},
		{`/a"b`, false}, // injected into HTML — keep to URL-safe characters
		{"/a b", false},
	} {
		if err := validateConfig(AdminConfig{BasePath: c.base}); (err == nil) != c.ok {
			t.Errorf("base_path %q: err=%v, want ok=%v", c.base, err, c.ok)
		}
	}
}

func newBaseServer(t *testing.T, cfg AdminConfig) *server {
	t.Helper()
	cfg.BasePath = "/vaultreader"
	s := newTestServer(t, cfg)
	writeFile(t, filepath.Join(s.vaultsDir, "team", "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	return s
}

func TestBasePathRoutes(t *testing.T) {
	s := newBaseServer(t, AdminConfig{RWPaths: []string{"team"}})
	h := s.handler()
	for _, c := range []struct {
		path string
		want int
	}{
		{"/vaultreader/health", 200},
		{"/vaultreader/api/vaults", 200},
		{"/vaultreader/style.css", 200},
		{"/vaultreader/logo.svg", 200},
		{"/vaultreader/", 200},
		{"/vaultreader/n/team/a.md", 200},
		// A proxy that already stripped the prefix: both forms accepted.
		{"/health", 200},
		{"/api/vaults", 200},
		{"/style.css", 200},
		// Not the prefix segment.
		{"/vaultreaderx/api/vaults", 404},
	} {
		if rec := do(t, h, "GET", c.path, "", nil); rec.Code != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, rec.Code, c.want)
		}
	}
	rec := do(t, h, "GET", "/vaultreader/api/vaults", "", nil)
	if !strings.Contains(rec.Body.String(), "team") {
		t.Errorf("/vaultreader/api/vaults body %q", rec.Body.String())
	}
	// The bare prefix redirects to the canonical trailing-slash root.
	rec = do(t, h, "GET", "/vaultreader?x=1", "", nil)
	if rec.Code != 301 || rec.Header().Get("Location") != "/vaultreader/?x=1" {
		t.Errorf("GET /vaultreader = %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestBasePathShellCarriesBase(t *testing.T) {
	s := newBaseServer(t, AdminConfig{})
	h := s.handler()
	for _, target := range []string{"/vaultreader/", "/vaultreader/n/team/a.md", "/n/team/a.md"} {
		body := do(t, h, "GET", target, "", nil).Body.String()
		for _, want := range []string{
			`<meta name="vr-base" content="/vaultreader">`,
			`<link rel="icon" href="/vaultreader/logo.svg"`,
			`<link rel="stylesheet" href="/vaultreader/style.css">`,
			`<script src="/vaultreader/purify.min.js">`,
			`<script src="/vaultreader/codemirror.bundle.js">`,
			`<script defer src="/vaultreader/alpine.min.js">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: shell lacks %s", target, want)
			}
		}
		if strings.Contains(body, ` src="/purify`) || strings.Contains(body, ` href="/style`) {
			t.Errorf("%s: unprefixed asset left in the shell", target)
		}
	}
}

func TestNoBasePathShellUnchanged(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	body := do(t, s.handler(), "GET", "/", "", nil).Body.String()
	if !strings.Contains(body, `<meta name="vr-base" content="">`) ||
		!strings.Contains(body, `<script src="/purify.min.js">`) ||
		!strings.Contains(body, `<link rel="stylesheet" href="/style.css">`) {
		t.Fatal("shell without base_path must keep root-absolute assets and an empty vr-base")
	}
}

func TestBasePathRequireUserExemptionsAfterStrip(t *testing.T) {
	s := newBaseServer(t, AdminConfig{RequireUser: true})
	h := s.handler()
	for _, c := range []struct {
		path string
		want int
	}{
		{"/vaultreader/health", 200},
		{"/vaultreader/share/x", 404}, // exempt → share handler (unknown token)
		{"/vaultreader/api/vaults", 401},
		{"/vaultreader/", 401},
		{"/vaultreader/sharefoo", 401},
	} {
		if rec := do(t, h, "GET", c.path, "", nil); rec.Code != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, rec.Code, c.want)
		}
	}
}

// Redirects issued by the mux (e.g. /webdav → /webdav/) keep the prefix.
func TestBasePathRedirectsKeepPrefix(t *testing.T) {
	s := newBaseServer(t, AdminConfig{})
	h := s.handler()
	for _, target := range []string{"/vaultreader/webdav", "/webdav"} {
		rec := do(t, h, "GET", target, "", nil)
		if loc := rec.Header().Get("Location"); rec.Code/100 != 3 || loc != "/vaultreader/webdav/" {
			t.Errorf("GET %s = %d Location %q, want /vaultreader/webdav/", target, rec.Code, loc)
		}
	}
}

func TestBasePathWebDAVHrefs(t *testing.T) {
	s := newBaseServer(t, AdminConfig{})
	h := s.handler()
	for _, target := range []string{"/vaultreader/webdav/team/", "/webdav/team/"} {
		rec := do(t, h, "PROPFIND", target, "", map[string]string{"Depth": "1"})
		if rec.Code != 207 {
			t.Fatalf("PROPFIND %s = %d", target, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<D:href>/vaultreader/webdav/team/a.md</D:href>") {
			t.Errorf("PROPFIND %s hrefs lack the prefix: %s", target, body)
		}
	}
	rec := do(t, h, "GET", "/vaultreader/webdav/team/a.md", "", nil)
	if rec.Code != 200 || rec.Body.String() != "# A\n" {
		t.Errorf("GET via webdav = %d %q", rec.Code, rec.Body.String())
	}
}

func TestBasePathInstanceAndShareURL(t *testing.T) {
	s := newBaseServer(t, AdminConfig{})
	rec := do(t, s.handler(), "GET", "http://demo.example.com/vaultreader/api/instance", "", nil)
	var got struct {
		BasePath     string `json:"base_path"`
		ShareBaseURL string `json:"share_base_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BasePath != "/vaultreader" || got.ShareBaseURL != "http://demo.example.com/vaultreader/share/" {
		t.Fatalf("instance = %+v", got)
	}
}

func TestBasePathSharePage(t *testing.T) {
	s := newBaseServer(t, AdminConfig{})
	writeFile(t, filepath.Join(s.vaultsDir, "team", "m.md"), "# M\n\n$$x^2$$\n")
	s.idx.buildAll(s.vaultsDir)
	e := s.shares.create("team", "m.md", false, 0, "", "test")
	h := s.handler()
	rec := do(t, h, "GET", "/vaultreader/share/"+e.Token, "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<base href="`+e.Token+`/">`) ||
		!strings.Contains(rec.Body.String(), `href="asset?name=katex.min.css"`) {
		t.Fatalf("share page = %d %q", rec.Code, rec.Body.String())
	}
	// KaTeX fonts must resolve relative to the stylesheet, so the CSS
	// works under any prefix (/share, /vaultreader/share, a proxy alias).
	css := do(t, h, "GET", "/vaultreader/share/"+e.Token+"/asset?name=katex.min.css", "", nil).Body.String()
	if strings.Contains(css, "url(/") || !strings.Contains(css, "url(asset?name=fonts/") {
		t.Fatalf("katex css font urls not relative")
	}
}

// Only <head> asset attributes and the codemirror bundle tag are rewritten;
// the inline application script must come through byte for byte.
func TestBasePathRewritesOnlyHeadAndBundle(t *testing.T) {
	raw, err := fs.ReadFile(staticFiles, "static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	headRaw, restRaw, _ := strings.Cut(string(raw), "</head>")
	wantRewrites := strings.Count(headRaw, ` src="/`) + strings.Count(headRaw, ` href="/`) + 1
	out := string(withBasePath(raw, "/vr"))
	got := strings.Count(out, ` src="/vr/`) + strings.Count(out, ` href="/vr/`)
	if got != wantRewrites {
		t.Fatalf("rewrote %d attributes, want %d", got, wantRewrites)
	}
	_, restOut, _ := strings.Cut(out, "</head>")
	if restOut != strings.Replace(restRaw, codemirrorTag, `<script src="/vr/codemirror.bundle.js">`, 1) {
		t.Fatal("body changed beyond the codemirror bundle tag")
	}
	if !strings.Contains(out, "<meta charset=\"UTF-8\">\n  <meta name=\"vr-base\" content=\"/vr\">") {
		t.Fatal("vr-base meta must directly follow <meta charset>")
	}
}
