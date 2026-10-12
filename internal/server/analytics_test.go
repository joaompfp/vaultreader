// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"strings"
	"testing"
)

const testSiteID = "4d3811b6-5af4-4bee-8660-f47d260f365c"

func TestValidateAnalytics(t *testing.T) {
	for _, c := range []struct {
		name string
		a    *AnalyticsConfig
		ok   bool
	}{
		{"absent", nil, true},
		{"site-root path", &AnalyticsConfig{UmamiScript: "/js/u/script.js", UmamiWebsiteID: testSiteID}, true},
		{"https url", &AnalyticsConfig{UmamiScript: "https://cloud.umami.is/script.js", UmamiWebsiteID: testSiteID}, true},
		{"https url with port", &AnalyticsConfig{UmamiScript: "https://stats.example.com:8443/u/script.js", UmamiWebsiteID: testSiteID}, true},
		{"missing script", &AnalyticsConfig{UmamiWebsiteID: testSiteID}, false},
		{"missing id", &AnalyticsConfig{UmamiScript: "/js/u/script.js"}, false},
		{"id not a uuid", &AnalyticsConfig{UmamiScript: "/js/u/script.js", UmamiWebsiteID: "joao.date"}, false},
		{"id with quote", &AnalyticsConfig{UmamiScript: "/js/u/script.js", UmamiWebsiteID: testSiteID + `"`}, false},
		{"relative path", &AnalyticsConfig{UmamiScript: "js/u/script.js", UmamiWebsiteID: testSiteID}, false},
		{"protocol-relative", &AnalyticsConfig{UmamiScript: "//evil.example/script.js", UmamiWebsiteID: testSiteID}, false},
		{"http url", &AnalyticsConfig{UmamiScript: "http://stats.example.com/script.js", UmamiWebsiteID: testSiteID}, false},
		{"javascript url", &AnalyticsConfig{UmamiScript: "javascript:alert(1)", UmamiWebsiteID: testSiteID}, false},
		{"quote in path", &AnalyticsConfig{UmamiScript: `/js/u/script.js"onload="x`, UmamiWebsiteID: testSiteID}, false},
		{"https no host", &AnalyticsConfig{UmamiScript: "https:///script.js", UmamiWebsiteID: testSiteID}, false},
		{"https userinfo", &AnalyticsConfig{UmamiScript: "https://u@stats.example.com/script.js", UmamiWebsiteID: testSiteID}, false},
	} {
		if err := validateConfig(AdminConfig{Analytics: c.a}); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestParseConfigAnalytics(t *testing.T) {
	cfg, err := parseConfig([]byte(`{"analytics":{"umami_script":"/js/u/script.js","umami_website_id":"` + testSiteID + `"}}`))
	if err != nil || cfg.Analytics == nil || cfg.Analytics.UmamiScript != "/js/u/script.js" || cfg.Analytics.UmamiWebsiteID != testSiteID {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	if _, err := parseConfig([]byte(`{"analytics":{"umami_script":"/s.js","umami_website_id":"` + testSiteID + `","typo":1}}`)); err == nil {
		t.Fatal("unknown analytics key must fail startup")
	}
	if _, err := parseConfig([]byte(`{"analytics":{"umami_script":"/s.js"}}`)); err == nil {
		t.Fatal("analytics without website id must fail startup")
	}
}

const umamiTag = `<script defer src="/js/u/script.js" data-website-id="` + testSiteID + `"></script>`

func TestAnalyticsTagInShell(t *testing.T) {
	s := newTestServer(t, AdminConfig{Analytics: &AnalyticsConfig{UmamiScript: "/js/u/script.js", UmamiWebsiteID: testSiteID}})
	h := s.handler()
	for _, target := range []string{"/", "/n/team/a.md"} {
		body := do(t, h, "GET", target, "", nil).Body.String()
		if strings.Count(body, umamiTag) != 1 {
			t.Errorf("%s: want the umami tag exactly once in the shell", target)
		}
		head, _, _ := strings.Cut(body, "</head>")
		if !strings.Contains(head, umamiTag) {
			t.Errorf("%s: umami tag must be inside <head>", target)
		}
	}
}

// A leading-/ script is site-root, not app-relative: base_path must not
// be prepended to it (the shell's asset rewrite would otherwise do so).
func TestAnalyticsTagNotBasePrefixed(t *testing.T) {
	s := newBaseServer(t, AdminConfig{Analytics: &AnalyticsConfig{UmamiScript: "/js/u/script.js", UmamiWebsiteID: testSiteID}})
	h := s.handler()
	for _, target := range []string{"/vaultreader/", "/vaultreader/n/team/a.md"} {
		body := do(t, h, "GET", target, "", nil).Body.String()
		if strings.Count(body, umamiTag) != 1 {
			t.Errorf("%s: want the unprefixed umami tag exactly once", target)
		}
		if strings.Contains(body, `/vaultreader/js/u/script.js`) {
			t.Errorf("%s: umami script got the base_path prefix", target)
		}
		// The bundled assets are still prefixed.
		if !strings.Contains(body, `<script src="/vaultreader/purify.min.js">`) {
			t.Errorf("%s: base_path rewrite of bundled assets broke", target)
		}
	}
}

func TestNoAnalyticsNoTag(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	body := do(t, s.handler(), "GET", "/", "", nil).Body.String()
	if strings.Contains(body, "data-website-id") {
		t.Fatal("no analytics config → no tracking tag")
	}
}

func TestAnalyticsCSP(t *testing.T) {
	// Same-origin script: 'self' already covers it; CSP unchanged.
	s := newTestServer(t, AdminConfig{Analytics: &AnalyticsConfig{UmamiScript: "/js/u/script.js", UmamiWebsiteID: testSiteID}})
	if got := do(t, s.handler(), "GET", "/", "", nil).Header().Get("Content-Security-Policy"); got != spaCSP {
		t.Fatalf("same-origin analytics changed the CSP: %q", got)
	}
	// Remote script: its origin joins script-src and connect-src only.
	s = newTestServer(t, AdminConfig{Analytics: &AnalyticsConfig{UmamiScript: "https://stats.example.com/u/script.js", UmamiWebsiteID: testSiteID}})
	rec := do(t, s.handler(), "GET", "/n/x/y.md", "", nil)
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://stats.example.com;",
		"connect-src 'self' https://stats.example.com;",
		"default-src 'self';",
		"frame-ancestors 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Count(csp, "stats.example.com") != 2 {
		t.Errorf("remote origin must appear only in script-src and connect-src: %q", csp)
	}
	if !strings.Contains(rec.Body.String(), `src="https://stats.example.com/u/script.js"`) {
		t.Error("remote script tag missing")
	}
	// Without analytics the shell keeps the base policy.
	s = newTestServer(t, AdminConfig{})
	if got := do(t, s.handler(), "GET", "/", "", nil).Header().Get("Content-Security-Policy"); got != spaCSP {
		t.Fatalf("CSP without analytics = %q", got)
	}
}

func TestAnalyticsNotOnSharePages(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}, Analytics: &AnalyticsConfig{UmamiScript: "https://stats.example.com/u/script.js", UmamiWebsiteID: testSiteID}})
	writeFile(t, s.vaultsDir+"/team/a.md", "# A\n")
	s.idx.buildAll(s.vaultsDir)
	e := s.shares.create("team", "a.md", false, 0, "", "alice")
	rec := do(t, s.handler(), "GET", "/share/"+e.Token, "", nil)
	if strings.Contains(rec.Body.String(), "data-website-id") || strings.Contains(rec.Header().Get("Content-Security-Policy"), "stats.example.com") {
		t.Fatal("share pages must not carry analytics")
	}
}
