// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// Read-only, cacheable GETs (file embeds, vault icons, static assets) must not
// drain the per-IP bucket: an image-heavy note would 429 its own images.
func TestRateLimitExemptsReadOnlyAssets(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	writeFile(t, filepath.Join(s.vaultsDir, "team", "img.png"), "\x89PNG\r\n\x1a\n")
	writeFile(t, filepath.Join(s.vaultsDir, "team", "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	h := s.handler()

	for i := 0; i < 300; i++ {
		if rec := do(t, h, "GET", "/api/file?vault=team&path=img.png", "", nil); rec.Code != 200 {
			t.Fatalf("GET /api/file #%d = %d", i+1, rec.Code)
		}
		if rec := do(t, h, "GET", "/alpine.min.js", "", nil); rec.Code != 200 {
			t.Fatalf("GET /alpine.min.js #%d = %d", i+1, rec.Code)
		}
	}
	origin := map[string]string{"Origin": "http://example.com"}
	for i := 0; i < 240; i++ {
		if rec := do(t, h, "PUT", "/api/note?vault=team&path=a.md", `{"content":"# A\n"}`, origin); rec.Code == 429 {
			t.Fatalf("PUT #%d limited too early", i+1)
		}
	}
	if rec := do(t, h, "PUT", "/api/note?vault=team&path=a.md", `{"content":"# A\n"}`, origin); rec.Code != 429 {
		t.Fatalf("241st PUT /api/note = %d, want 429", rec.Code)
	}
}

func TestIsTrustedProxyPeer(t *testing.T) {
	for host, want := range map[string]bool{
		"": true, "127.0.0.1": true, "::1": true, "[::1]": true,
		"10.1.2.3": true, "172.16.0.1": true, "172.31.255.1": true, "192.168.1.5": true,
		"fd00::5": true, "[fd12:3456::1]": true, "::ffff:10.0.0.1": true, "fe80::1%eth0": false,
		"172.15.0.1": false, "172.32.0.1": false, "8.8.8.8": false,
		"2001:db8::1": false, "[2001:db8::1]": false, "not-an-ip": false,
	} {
		if got := isTrustedProxyPeer(host); got != want {
			t.Errorf("isTrustedProxyPeer(%q) = %v, want %v", host, got, want)
		}
	}
}

// The limiter keys on the forwarded client IP only when the peer is a
// trusted proxy, whatever the address family or formatting of RemoteAddr.
func TestRateLimiterPeerParsing(t *testing.T) {
	for _, c := range []struct {
		remote  string
		trusted bool
	}{
		{"10.0.0.2:4000", true},
		{"[::1]:4000", true},
		{"[fd00::5]:4000", true},
		{"203.0.113.9:4000", false},
		{"[2001:db8::7]:4000", false},
	} {
		rl := newRateLimiter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), 1, time.Minute, nil)
		codes := []int{}
		for _, client := range []string{"198.51.100.1", "198.51.100.2"} {
			r := httptest.NewRequest("GET", "/api/x", nil)
			r.RemoteAddr = c.remote
			r.Header.Set("X-Real-IP", client)
			rec := httptest.NewRecorder()
			rl.ServeHTTP(rec, r)
			codes = append(codes, rec.Code)
		}
		// Trusted: separate buckets per forwarded client (both 200).
		// Untrusted: header ignored, one shared bucket (second is 429).
		want := 429
		if c.trusted {
			want = 200
		}
		if codes[0] != 200 || codes[1] != want {
			t.Errorf("%s: codes %v, want [200 %d]", c.remote, codes, want)
		}
	}
}
