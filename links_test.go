// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func backlinkPaths(s *server, vault, path string) []string {
	var out []string
	for _, b := range s.idx.getBacklinks(vault, path) {
		out = append(out, b.Path)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every wikilink form Obsidian (and our [[ autocomplete) writes must count
// as a backlink of the note it points to.
func TestBacklinksAllLinkForms(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "Folder", "Note.md"), "# Note\n")
	writeFile(t, filepath.Join(v, "bare.md"), "[[Note]]\n")
	writeFile(t, filepath.Join(v, "path.md"), "[[Folder/Note]]\n")
	writeFile(t, filepath.Join(v, "alias.md"), "[[Folder/Note|the note]]\n")
	writeFile(t, filepath.Join(v, "heading.md"), "[[Folder/Note#Intro]]\n")
	writeFile(t, filepath.Join(v, "block.md"), "[[Note^abc123]]\n")
	writeFile(t, filepath.Join(v, "ext.md"), "[[folder/note.MD]]\n")
	writeFile(t, filepath.Join(v, "escaped.md"), "| [[Folder/Note\\|x]] |\n")
	writeFile(t, filepath.Join(v, "Folder", "sib.md"), "[[./Note]]\n")
	writeFile(t, filepath.Join(v, "other.md"), "[[Elsewhere/Note]] [[Missing]]\n")
	s.idx.buildAll(s.vaultsDir)

	got := backlinkPaths(s, "team", "Folder/Note.md")
	want := []string{"Folder/sib.md", "alias.md", "bare.md", "block.md", "escaped.md", "ext.md", "heading.md", "other.md", "path.md"}
	if !equalStrings(got, want) {
		t.Fatalf("backlinks = %v\nwant       %v", got, want)
	}
}

// A path-style link picks the note at that path, not a same-named note
// elsewhere; a bare ambiguous name keeps proximity resolution.
func TestBacklinksDisambiguation(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "a", "Note.md"), "# A note\n")
	writeFile(t, filepath.Join(v, "b", "Note.md"), "# B note\n")
	writeFile(t, filepath.Join(v, "root.md"), "[[b/Note]]\n")
	writeFile(t, filepath.Join(v, "a", "near.md"), "[[Note]]\n")
	s.idx.buildAll(s.vaultsDir)

	if got := backlinkPaths(s, "team", "b/Note.md"); !equalStrings(got, []string{"root.md"}) {
		t.Fatalf("b/Note backlinks = %v, want [root.md]", got)
	}
	if got := backlinkPaths(s, "team", "a/Note.md"); !equalStrings(got, []string{"a/near.md"}) {
		t.Fatalf("a/Note backlinks = %v, want [a/near.md]", got)
	}
}

// updateNote (app save) and the rescanner keep path-style links consistent.
func TestBacklinksPathStyleAfterUpdateAndRescan(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "Folder", "Note.md"), "# Note\n")
	writeFile(t, filepath.Join(v, "src.md"), "nothing\n")
	s.idx.buildAll(s.vaultsDir)
	rs := newRescanner(s.vaultsDir, s.idx)

	s.idx.updateNote("team", "src.md", "[[Folder/Note|n]]\n")
	if got := backlinkPaths(s, "team", "Folder/Note.md"); !equalStrings(got, []string{"src.md"}) {
		t.Fatalf("after updateNote = %v", got)
	}
	s.idx.updateNote("team", "src.md", "no links\n")
	if got := backlinkPaths(s, "team", "Folder/Note.md"); len(got) != 0 {
		t.Fatalf("stale backlink after unlink = %v", got)
	}

	writeFile(t, filepath.Join(v, "ext.md"), "[[Folder/Note#H]]\n")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(v, "ext.md"), future, future)
	rs.pass()
	if got := backlinkPaths(s, "team", "Folder/Note.md"); !equalStrings(got, []string{"ext.md"}) {
		t.Fatalf("after rescan = %v", got)
	}
	_ = os.Remove(filepath.Join(v, "ext.md"))
	rs.pass()
	if got := backlinkPaths(s, "team", "Folder/Note.md"); len(got) != 0 {
		t.Fatalf("after rescan delete = %v", got)
	}
}

type graphResp struct {
	Nodes []struct {
		ID   string `json:"id"`
		Refs int    `json:"refs"`
	} `json:"nodes"`
	Edges []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"edges"`
}

func getGraph(t *testing.T, s *server, query string) graphResp {
	t.Helper()
	rec := do(t, s.handler(), "GET", "/api/graph?"+query, "", nil)
	if rec.Code != 200 {
		t.Fatalf("graph %s = %d %s", query, rec.Code, rec.Body.String())
	}
	var g graphResp
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func hasEdge(g graphResp, src, dst string) bool {
	for _, e := range g.Edges {
		if e.Source == src && e.Target == dst {
			return true
		}
	}
	return false
}

func TestGraphPathStyleEdges(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "Folder", "Note.md"), "# Note\n")
	writeFile(t, filepath.Join(v, "other", "Note.md"), "# Other\n")
	writeFile(t, filepath.Join(v, "path.md"), "[[Folder/Note|x]]\n")
	writeFile(t, filepath.Join(v, "heading.md"), "[[Folder/Note#H]]\n")
	s.idx.buildAll(s.vaultsDir)

	g := getGraph(t, s, "vault=team")
	for _, src := range []string{"team:path.md", "team:heading.md"} {
		if !hasEdge(g, src, "team:Folder/Note.md") {
			t.Fatalf("missing edge %s -> team:Folder/Note.md; edges=%v", src, g.Edges)
		}
		if hasEdge(g, src, "team:other/Note.md") {
			t.Fatalf("wrong edge %s -> team:other/Note.md", src)
		}
	}

	// Ego graph around the target reaches the path-style linkers via inbound.
	eg := getGraph(t, s, "center=team:Folder/Note.md&depth=1")
	ids := map[string]bool{}
	for _, n := range eg.Nodes {
		ids[n.ID] = true
	}
	if !ids["team:path.md"] || !ids["team:heading.md"] || ids["team:other/Note.md"] {
		t.Fatalf("ego graph nodes = %v", ids)
	}
}

// Folder rename re-indexes the moved notes' outbound links at once, so they
// stay backlink sources under their new paths without waiting for a rescan.
func TestBacklinksFolderRenameImmediate(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "Target.md"), "# Target\n")
	writeFile(t, filepath.Join(v, "old", "src.md"), "# Src\n[[Target]]\n")
	s.idx.buildAll(s.vaultsDir)

	if rec := do(t, s.handler(), "PATCH", "/api/folder?vault=team&from=old&to=new", "", nil); rec.Code != 200 {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}
	if got := backlinkPaths(s, "team", "Target.md"); !equalStrings(got, []string{"new/src.md"}) {
		t.Fatalf("backlinks after folder rename = %v, want [new/src.md]", got)
	}
	g := getGraph(t, s, "vault=team")
	if !hasEdge(g, "team:new/src.md", "team:Target.md") || hasEdge(g, "team:old/src.md", "team:Target.md") {
		t.Fatalf("graph edges after folder rename = %v", g.Edges)
	}
}

func TestBacklinksFolderDeleteImmediate(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "Target.md"), "# Target\n")
	writeFile(t, filepath.Join(v, "gone", "src.md"), "[[Target]]\n")
	s.idx.buildAll(s.vaultsDir)
	if got := backlinkPaths(s, "team", "Target.md"); len(got) != 1 {
		t.Fatalf("positive control: backlinks = %v", got)
	}

	if rec := do(t, s.handler(), "DELETE", "/api/folder?vault=team&path=gone", "", nil); rec.Code != 200 {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if got := backlinkPaths(s, "team", "Target.md"); len(got) != 0 {
		t.Fatalf("stale backlink after folder delete = %v", got)
	}
	if _, ok := s.idx.outbound["team:gone/src.md"]; ok {
		t.Fatal("deleted note still has outbound links in the index")
	}
}

// Moving the target note leaves [[OldFolder/Note]] pointing at it through
// the renderer's basename fallback.
func TestBacklinksSurviveTargetMove(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	v := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(v, "OldFolder", "Note.md"), "# Note\n")
	writeFile(t, filepath.Join(v, "src.md"), "[[OldFolder/Note]]\n")
	s.idx.buildAll(s.vaultsDir)

	if rec := do(t, s.handler(), "POST", "/api/move?vault=team&from=OldFolder/Note.md&to=NewFolder/Note.md", "", nil); rec.Code != 200 {
		t.Fatalf("move = %d %s", rec.Code, rec.Body.String())
	}
	if got := backlinkPaths(s, "team", "NewFolder/Note.md"); !equalStrings(got, []string{"src.md"}) {
		t.Fatalf("backlinks after target move = %v, want [src.md]", got)
	}
}
