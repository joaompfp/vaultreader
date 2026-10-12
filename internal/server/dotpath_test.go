// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasDotSegment(t *testing.T) {
	for p, want := range map[string]bool{
		"a.md": false, "notes/a.md": false, ".": false, "a/b.c/d.md": false,
		".git/config": true, ".obsidian": true, "a/.git/hooks/x": true,
		"a/..b/c.md": true, ".trash/x.md": true,
	} {
		if got := hasDotSegment(p); got != want {
			t.Errorf("hasDotSegment(%q) = %v, want %v", p, got, want)
		}
	}
}

// Hidden (dot) paths are never writable through the API, even when the
// whole vault is: .git/hooks, .obsidian/plugins and the like are code that
// a host tool or desktop app runs. .trash stays reachable only through the
// server's own trash code.
func TestDotPathWritesRefused(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, ".git", "config"), "[core]\n")
	writeFile(t, filepath.Join(v, "a.md"), "# A\n")
	writeFile(t, filepath.Join(v, "dir", "x.md"), "# X\n")
	h := s.handler()

	refused := []struct{ name, method, target, body string }{
		{"PUT .git/config", "PUT", "/api/note?vault=team&path=.git/config", "pwned"},
		{"PUT ./.git//config", "PUT", "/api/note?vault=team&path=./.git//config", "pwned"},
		{"create .obsidian plugin", "POST", "/api/note?vault=team&path=.obsidian/plugins/x/main.js", `{"raw":"x"}`},
		{"delete .git/config", "DELETE", "/api/note?vault=team&path=.git/config", ""},
		{"move into .git/hooks", "POST", "/api/move?vault=team&from=a.md&to=.git/hooks/post-commit", ""},
		{"move out of .git", "POST", "/api/move?vault=team&from=.git/config&to=config.md", ""},
		{"folder create .obsidian", "POST", "/api/folder?vault=team&path=.obsidian", ""},
		{"folder delete .git", "DELETE", "/api/folder?vault=team&path=.git", ""},
		{"folder rename into .git", "PATCH", "/api/folder?vault=team&from=dir&to=.git/dir", ""},
		{"folder rename .git out", "PATCH", "/api/folder?vault=team&from=.git&to=git", ""},
		{"attachment delete .git/config", "DELETE", "/api/attachments?vault=team&path=.git/config", ""},
		{"writable share of .git/config", "POST", "/api/shares/create", `{"vault":"team","path":".git/config","writable":true}`},
	}
	for _, c := range refused {
		rec := do(t, h, c.method, c.target, c.body, nil)
		if rec.Code != 403 {
			t.Errorf("%s: code = %d %s, want 403", c.name, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(rec.Body.String(), "hidden") {
			t.Errorf("%s: message %q does not say why", c.name, rec.Body.String())
		}
	}
	if b, _ := os.ReadFile(filepath.Join(v, ".git", "config")); string(b) != "[core]\n" {
		t.Fatalf(".git/config changed: %q", b)
	}
	for _, p := range []string{".obsidian", filepath.Join(".git", "hooks"), filepath.Join(".git", "dir"), "git", "config.md"} {
		if exists(filepath.Join(v, p)) {
			t.Fatalf("%s created despite refusal", p)
		}
	}
	if !exists(filepath.Join(v, "a.md")) || !exists(filepath.Join(v, "dir", "x.md")) {
		t.Fatal("source moved despite refusal")
	}

	// Upload: both the note path and the attachments path written.
	if rec := uploadImage(t, s, "team", ".git/n.md"); rec.Code != 403 {
		t.Fatalf("upload under .git = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if exists(filepath.Join(v, ".git", "attachments")) {
		t.Fatal("upload wrote under .git")
	}

	// Share PUT re-checks at write time (older share files hold any path).
	sh := s.shares.create("team", ".git/config", true, 0, "", "test")
	if rec := do(t, h, "PUT", "/share/"+sh.Token, `{"raw":"pwned"}`, nil); rec.Code != 403 {
		t.Fatalf("share PUT to .git/config = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(v, ".git", "config")); string(b) != "[core]\n" {
		t.Fatalf(".git/config changed via share: %q", b)
	}

	// Positive control: a normal save still works.
	if rec := do(t, h, "PUT", "/api/note?vault=team&path=a.md", "# A2\n", nil); rec.Code != 200 {
		t.Fatalf("normal save = %d %s", rec.Code, rec.Body.String())
	}
}

// A trash entry whose original path is hidden cannot be restored there.
func TestTrashRestoreRefusesDotDestination(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	name := makeTrashName(".git/hooks/post-commit", 1)
	writeFile(t, filepath.Join(v, ".trash", name), "#!/bin/sh\n")
	rec := do(t, s.handler(), "POST", "/api/trash/restore?vault=team&path=.trash/"+name, "", nil)
	if rec.Code != 403 {
		t.Fatalf("restore to .git = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if exists(filepath.Join(v, ".git")) {
		t.Fatal("restore wrote under .git")
	}
}

// The server's own trash code keeps working: soft delete, restore, empty.
func TestTrashStillWorksWithDotPathGuard(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "a.md"), "# A\n")
	writeFile(t, filepath.Join(v, "b.md"), "# B\n")
	h := s.handler()

	for _, p := range []string{"a.md", "b.md"} {
		if rec := do(t, h, "DELETE", "/api/note?vault=team&path="+p, "", nil); rec.Code != 200 {
			t.Fatalf("soft delete %s = %d %s", p, rec.Code, rec.Body.String())
		}
	}
	ents, _ := os.ReadDir(filepath.Join(v, ".trash"))
	if len(ents) != 2 {
		t.Fatalf("trash entries = %d, want 2", len(ents))
	}
	var aName string
	for _, e := range ents {
		if orig, _ := decodeTrashName(e.Name()); orig == "a.md" {
			aName = e.Name()
		}
	}
	if rec := do(t, h, "POST", "/api/trash/restore?vault=team&path=.trash/"+aName, "", nil); rec.Code != 204 {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body.String())
	}
	if !exists(filepath.Join(v, "a.md")) {
		t.Fatal("restore did not bring a.md back")
	}
	if rec := do(t, h, "DELETE", "/api/trash/empty?vault=team", "", nil); rec.Code != 204 {
		t.Fatalf("empty = %d %s", rec.Code, rec.Body.String())
	}
	if ents, _ := os.ReadDir(filepath.Join(v, ".trash")); len(ents) != 0 {
		t.Fatalf("trash not emptied: %d entries", len(ents))
	}
}
