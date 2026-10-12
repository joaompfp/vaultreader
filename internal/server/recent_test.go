// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentNewestFirst(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	base := time.Now().Add(-time.Hour)
	for i, p := range []string{"team/old.md", "other/mid.md", "team/sub/new.md", "team/.trash/gone.md"} {
		full := filepath.Join(s.vaultsDir, p)
		writeFile(t, full, "# T\n")
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(full, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(s.vaultsDir, "team", "img.png"), "png")
	s.idx.buildAll(s.vaultsDir)
	h := s.handler()

	var got []RecentNote
	rec := do(t, h, "GET", "/api/recent", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	want := []string{"team/sub/new.md", "other/mid.md", "team/old.md"}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %v", got, want)
	}
	for i, w := range want {
		if got[i].Vault+"/"+got[i].Path != w {
			t.Errorf("[%d] = %s/%s, want %s", i, got[i].Vault, got[i].Path, w)
		}
	}

	// A save through the index moves the note to the top.
	s.idx.updateNote("team", "old.md", "# T\nedited\n")
	_ = json.Unmarshal(do(t, h, "GET", "/api/recent?limit=1", "", nil).Body.Bytes(), &got)
	if len(got) != 1 || got[0].Path != "old.md" {
		t.Errorf("after edit: %+v", got)
	}

	for _, q := range []string{"?limit=0", "?limit=x", "?limit=-3"} {
		if c := do(t, h, "GET", "/api/recent"+q, "", nil).Code; c != 400 {
			t.Errorf("%s: code %d, want 400", q, c)
		}
	}
	if c := do(t, h, "POST", "/api/recent", "", nil).Code; c != 405 {
		t.Errorf("POST: code %d, want 405", c)
	}
}
