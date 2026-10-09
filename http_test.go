package main

import (
	"path/filepath"
	"testing"
)

// Read-only, cacheable GETs (file embeds, vault icons, static assets) must not
// drain the per-IP bucket: an image-heavy note would 429 its own images.
func TestRateLimitExemptsReadOnlyAssets(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	writeFile(t, filepath.Join(s.vaultsDir, "jll", "img.png"), "\x89PNG\r\n\x1a\n")
	writeFile(t, filepath.Join(s.vaultsDir, "jll", "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	h := s.handler()

	for i := 0; i < 300; i++ {
		if rec := do(t, h, "GET", "/api/file?vault=jll&path=img.png", "", nil); rec.Code != 200 {
			t.Fatalf("GET /api/file #%d = %d", i+1, rec.Code)
		}
		if rec := do(t, h, "GET", "/alpine.min.js", "", nil); rec.Code != 200 {
			t.Fatalf("GET /alpine.min.js #%d = %d", i+1, rec.Code)
		}
	}
	origin := map[string]string{"Origin": "http://example.com"}
	for i := 0; i < 240; i++ {
		if rec := do(t, h, "PUT", "/api/note?vault=jll&path=a.md", `{"content":"# A\n"}`, origin); rec.Code == 429 {
			t.Fatalf("PUT #%d limited too early", i+1)
		}
	}
	if rec := do(t, h, "PUT", "/api/note?vault=jll&path=a.md", `{"content":"# A\n"}`, origin); rec.Code != 429 {
		t.Fatalf("241st PUT /api/note = %d, want 429", rec.Code)
	}
}
