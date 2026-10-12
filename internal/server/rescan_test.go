// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAllSkipsDotDirs(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "keep.md"), "# Keep\n")
	writeFile(t, filepath.Join(v, ".trash", makeTrashName("gone.md", 1)), "# Gone\n")
	writeFile(t, filepath.Join(v, ".obsidian", "x.md"), "# X\n")
	s.idx.buildAll(s.vaultsDir)

	rec := do(t, s.handler(), "GET", "/api/search?q=VRTRASH&vault=team", "", nil)
	if ctl := do(t, s.handler(), "GET", "/api/search?q=Keep&vault=team", "", nil); !strings.Contains(ctl.Body.String(), "keep.md") {
		t.Fatalf("positive control: search does not return keep.md: %s", ctl.Body.String())
	}
	if strings.Contains(rec.Body.String(), ".trash") {
		t.Fatalf("trashed note is searchable: %s", rec.Body.String())
	}
	if _, _, ok := s.idx.resolve("x", "team", ""); ok {
		t.Fatal(".obsidian note was indexed")
	}
	if _, _, ok := s.idx.resolve("keep", "team", ""); !ok {
		t.Fatal("regular note missing from index")
	}
}

func TestRescanPicksUpExternalChanges(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	rs := newRescanner(s.vaultsDir, s.idx)

	if c, r := rs.pass(); c != 0 || r != 0 {
		t.Fatalf("idle pass changed=%d removed=%d", c, r)
	}

	// Created outside the app.
	writeFile(t, filepath.Join(v, "b.md"), "# B\n[[a]]\n")
	// Modified outside the app (force a different mtime).
	writeFile(t, filepath.Join(v, "a.md"), "# A changed\n")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(v, "a.md"), future, future)

	if c, _ := rs.pass(); c != 2 {
		t.Fatalf("changed = %d, want 2", c)
	}
	if bl := s.idx.getBacklinks("team", "a.md"); len(bl) != 1 {
		t.Fatalf("backlinks to a.md = %d, want 1", len(bl))
	}

	// Deleted outside the app.
	_ = os.Remove(filepath.Join(v, "b.md"))
	if _, r := rs.pass(); r != 1 {
		t.Fatalf("removed = %d, want 1", r)
	}
	if _, _, ok := s.idx.resolve("b", "team", ""); ok {
		t.Fatal("deleted note still resolvable")
	}
}

func TestNoteMeta(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	writeFile(t, filepath.Join(s.vaultsDir, "team", "a.md"), "# A\n")
	h := s.handler()
	rec := do(t, h, "GET", "/api/note?vault=team&path=a.md&meta=1", "", nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"mtime"`) || strings.Contains(body, `"html"`) {
		t.Fatalf("meta = %d %s", rec.Code, body)
	}
	if rec := do(t, h, "GET", "/api/note?vault=team&path=nope.md&meta=1", "", nil); rec.Code != 404 {
		t.Fatalf("missing meta = %d, want 404", rec.Code)
	}
}

func TestSymlinkedFileIndexedAndRescanned(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "real", "target.md"), "# Target\n")
	if err := os.Symlink(filepath.Join(v, "real", "target.md"), filepath.Join(v, "link.md")); err != nil {
		t.Skip("symlinks unavailable")
	}
	s.idx.buildAll(s.vaultsDir)
	if _, _, ok := s.idx.resolve("link", "team", ""); !ok {
		t.Fatal("symlinked note missing after buildAll")
	}

	// Rescanner picks up a symlink created later.
	rs := newRescanner(s.vaultsDir, s.idx)
	if err := os.Symlink(filepath.Join(v, "real", "target.md"), filepath.Join(v, "link3.md")); err != nil {
		t.Fatal(err)
	}
	if c, _ := rs.pass(); c != 1 {
		t.Fatalf("changed = %d, want 1", c)
	}
	if _, _, ok := s.idx.resolve("link3", "team", ""); !ok {
		t.Fatal("rescanner did not index symlinked note")
	}
}

func TestRescanReadFailureKeepsNote(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod does not block reads as root")
	}
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	p := filepath.Join(v, "a.md")
	writeFile(t, p, "# A\n")
	s.idx.buildAll(s.vaultsDir)
	rs := newRescanner(s.vaultsDir, s.idx)

	writeFile(t, p, "# A changed a lot\n")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p, 0o644)
	if c, r := rs.pass(); r != 0 || c != 0 {
		t.Fatalf("changed=%d removed=%d, want 0/0", c, r)
	}
	if _, _, ok := s.idx.resolve("a", "team", ""); !ok {
		t.Fatal("unreadable note was evicted")
	}
	_ = os.Chmod(p, 0o644)
	if c, _ := rs.pass(); c != 1 {
		t.Fatalf("retry changed = %d, want 1", c)
	}
}
