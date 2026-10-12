// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateSiteName(t *testing.T) {
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"", true},
		{"Team notes", true},
		{`<script>"&'`, true}, // escaped where rendered
		{strings.Repeat("é", maxSiteName), true},
		{strings.Repeat("a", maxSiteName+1), false},
		{"a\nb", false},
		{"a\x00b", false},
	} {
		if err := validateSiteName(c.name); (err == nil) != c.ok {
			t.Errorf("site_name %q: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestShellTitleFollowsSiteName(t *testing.T) {
	s := newTestServer(t, AdminConfig{AdminToken: "tok", RWPaths: []string{"team"}})
	h := s.handler()
	if body := do(t, h, "GET", "/", "", nil).Body.String(); !strings.Contains(body, "<title>VaultReader</title>") {
		t.Fatalf("default title missing")
	}
	auth := map[string]string{"X-Admin-Token": "tok", "Content-Type": "application/json"}
	if rec := do(t, h, "POST", "/api/admin/config", `{"site_name":"  Notes <&> Co  "}`, auth); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	body := do(t, h, "GET", "/n/team/a.md", "", nil).Body.String()
	if !strings.Contains(body, "<title>Notes &lt;&amp;&gt; Co</title>") {
		t.Errorf("title not rendered/escaped: %q", body[:300])
	}
	var inst InstanceInfo
	_ = json.Unmarshal(do(t, h, "GET", "/api/instance", "", nil).Body.Bytes(), &inst)
	if inst.SiteName != "Notes <&> Co" {
		t.Errorf("instance site_name = %q", inst.SiteName)
	}
	// Saving only the name must not touch rw_paths (partial update).
	if got := s.cfg.RWPaths; len(got) != 1 || got[0] != "team" {
		t.Errorf("rw_paths = %v, want [team]", got)
	}
	// Persisted, and reloads strictly.
	s.cfg = AdminConfig{}
	s.loadConfig()
	if s.cfg.SiteName != "Notes <&> Co" || len(s.cfg.RWPaths) != 1 {
		t.Errorf("reloaded config = %+v", s.cfg)
	}
}

func TestAdminConfigRejects(t *testing.T) {
	s := newTestServer(t, AdminConfig{AdminToken: "tok"})
	h := s.handler()
	auth := map[string]string{"X-Admin-Token": "tok", "Content-Type": "application/json"}
	for _, c := range []struct {
		body    string
		headers map[string]string
		want    int
	}{
		{`{"site_name":"x"}`, map[string]string{"Content-Type": "application/json"}, 403},
		{`{"site_name":"x"}`, map[string]string{"X-Admin-Token": "nope"}, 403},
		{`{"site_name":"a\nb"}`, auth, 400},
		{`{"site_name":"` + strings.Repeat("a", maxSiteName+1) + `"}`, auth, 400},
		{`{"features":{"webdav":false}}`, auth, 400}, // file-only setting
		{`{"rw_paths":["../x"]}`, auth, 400},
	} {
		if rec := do(t, h, "POST", "/api/admin/config", c.body, c.headers); rec.Code != c.want {
			t.Errorf("%s: code %d, want %d", c.body, rec.Code, c.want)
		}
	}
	if s.cfg.SiteName != "" {
		t.Errorf("rejected request changed site_name to %q", s.cfg.SiteName)
	}
}
