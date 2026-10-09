// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesServeExistingEndpoints(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	writeFile(t, filepath.Join(s.vaultsDir, "team", "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	h := s.handler()

	if rec := do(t, h, "GET", "/health", "", nil); rec.Code != 200 {
		t.Fatalf("/health = %d", rec.Code)
	}
	rec := do(t, h, "GET", "/api/vaults", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "team") {
		t.Fatalf("/api/vaults = %d %q", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/note?vault=team&path=a.md", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"raw":"# A\n"`) {
		t.Fatalf("/api/note = %d %q", rec.Code, rec.Body.String())
	}
}
