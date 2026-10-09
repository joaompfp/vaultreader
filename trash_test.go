package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrashRestoreRejectsTraversal(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	secret := filepath.Join(s.appdataDir, "shares.json")
	writeFile(t, secret, "[]")
	h := s.handler()

	for _, p := range []string{
		"../../appdata/shares.json",
		".trash/../../../appdata/shares.json",
		"notes/a.md",      // not under .trash
		".trash/sub/x.md", // nested — only direct children are restorable
		"/etc/passwd",
	} {
		rec := do(t, h, "POST", "/api/trash/restore?vault=jll&path="+p, "", nil)
		if rec.Code != 400 {
			t.Errorf("path %q: code %d, want 400", p, rec.Code)
		}
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("appdata file was moved: %v", err)
	}
}

func TestTrashRestoreRoundTrip(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	writeFile(t, filepath.Join(s.vaultsDir, "jll", "x", "n.md"), "# N\n")
	h := s.handler()

	rec := do(t, h, "DELETE", "/api/note?vault=jll&path=x/n.md", "", nil)
	if rec.Code != 200 {
		t.Fatalf("delete = %d", rec.Code)
	}
	entries, _ := os.ReadDir(filepath.Join(s.vaultsDir, "jll", ".trash"))
	if len(entries) != 1 {
		t.Fatalf("trash entries = %d", len(entries))
	}
	rec = do(t, h, "POST", "/api/trash/restore?vault=jll&path=.trash/"+entries[0].Name(), "", nil)
	if rec.Code != 204 {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.vaultsDir, "jll", "x", "n.md")); err != nil {
		t.Fatalf("not restored: %v", err)
	}
}

func TestTrashRestoreRequiresWritable(t *testing.T) {
	s := newTestServer(t, AdminConfig{}) // nothing writable
	writeFile(t, filepath.Join(s.vaultsDir, "jll", ".trash", makeTrashName("n.md", 1)), "# N\n")
	rec := do(t, s.handler(), "POST", "/api/trash/restore?vault=jll&path=.trash/"+makeTrashName("n.md", 1), "", nil)
	if rec.Code != 403 {
		t.Fatalf("code %d, want 403", rec.Code)
	}
}

// Sweep finding: handleDocxTemplates walked ancestors of an unvalidated path.
func TestDocxTemplatesRejectsTraversal(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	// <root>/outside/.vr/leak.docx sits outside the vault (vaultsDir/jll).
	root := filepath.Dir(s.vaultsDir)
	writeFile(t, filepath.Join(root, "outside", ".vr", "leak.docx"), "x")
	rec := do(t, s.handler(), "GET", "/api/templates/docx?vault=jll&path=../../outside/x.md", "", nil)
	if strings.Contains(rec.Body.String(), "leak.docx") {
		t.Fatalf("template outside vault listed: %s", rec.Body.String())
	}
}

// Restoring over a note recreated at the original path must not clobber it.
func TestTrashRestoreRefusesOverwrite(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	a := filepath.Join(s.vaultsDir, "jll", "x", "a.md")
	writeFile(t, a, "# old\n")
	h := s.handler()
	if rec := do(t, h, "DELETE", "/api/note?vault=jll&path=x/a.md", "", nil); rec.Code != 200 {
		t.Fatalf("delete = %d", rec.Code)
	}
	writeFile(t, a, "# new\n")
	entries, _ := os.ReadDir(filepath.Join(s.vaultsDir, "jll", ".trash"))
	if len(entries) != 1 {
		t.Fatalf("trash entries = %d", len(entries))
	}
	rec := do(t, h, "POST", "/api/trash/restore?vault=jll&path=.trash/"+entries[0].Name(), "", nil)
	if rec.Code != 409 {
		t.Fatalf("restore = %d, want 409", rec.Code)
	}
	if got, _ := os.ReadFile(a); string(got) != "# new\n" {
		t.Fatalf("recreated note clobbered: %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.vaultsDir, "jll", ".trash", entries[0].Name())); err != nil {
		t.Fatalf("trash entry lost: %v", err)
	}
}
