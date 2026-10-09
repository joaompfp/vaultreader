// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Folder rename honours RWPaths on both ends, like handleMove.
func TestRenameFolderRequiresWritable(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team/rw", "team/dst"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "ro", "a.md"), "# A\n")
	writeFile(t, filepath.Join(v, "rw", "b.md"), "# B\n")
	s.idx.buildAll(s.vaultsDir)
	h := s.handler()

	cases := []struct{ name, from, to string }{
		{"read-only source", "ro", "rw/ro2"},
		{"read-only destination", "rw", "ro/rw2"},
		{"read-only both", "ro", "ro3"},
	}
	for _, c := range cases {
		rec := do(t, h, "PATCH", "/api/folder?vault=team&from="+c.from+"&to="+c.to, "", nil)
		if rec.Code != 403 {
			t.Fatalf("%s: code = %d %s, want 403", c.name, rec.Code, rec.Body.String())
		}
		if !exists(filepath.Join(v, c.from)) || exists(filepath.Join(v, c.to)) {
			t.Fatalf("%s: filesystem changed despite 403", c.name)
		}
	}
	if _, _, ok := s.idx.resolve("a", "team", ""); !ok {
		t.Fatal("index changed despite 403")
	}

	// Positive control: writable on both ends works.
	if rec := do(t, h, "PATCH", "/api/folder?vault=team&from=rw&to=dst/rw", "", nil); rec.Code != 200 {
		t.Fatalf("writable rename = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if !exists(filepath.Join(v, "dst", "rw", "b.md")) {
		t.Fatal("writable rename did not move the folder")
	}
}

func TestRenameFolderRefusesExistingDestination(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "a", "x.md"), "# X\n")
	writeFile(t, filepath.Join(v, "b", "y.md"), "# Y\n")
	rec := do(t, s.handler(), "PATCH", "/api/folder?vault=team&from=a&to=b", "", nil)
	if rec.Code != 409 {
		t.Fatalf("code = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if !exists(filepath.Join(v, "a", "x.md")) || !exists(filepath.Join(v, "b", "y.md")) || exists(filepath.Join(v, "b", "x.md")) {
		t.Fatal("filesystem changed despite 409")
	}
}

func TestRenameFolderRejectsTraversal(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "a", "x.md"), "# X\n")
	for _, q := range []string{"from=a&to=../escaped", "from=../team&to=z", "from=a&to=/abs"} {
		if rec := do(t, s.handler(), "PATCH", "/api/folder?vault=team&"+q, "", nil); rec.Code != 400 {
			t.Fatalf("%s: code = %d, want 400", q, rec.Code)
		}
	}
	if exists(filepath.Join(s.vaultsDir, "escaped")) || !exists(filepath.Join(v, "a", "x.md")) {
		t.Fatal("traversal rename touched the filesystem")
	}
}
