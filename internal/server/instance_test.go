// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"testing"
)

func TestInstanceInfo(t *testing.T) {
	s := newTestServer(t, AdminConfig{
		RWPaths:      []string{"team"},
		ShareBaseURL: "https://team.example.com/share/",
		Features:     map[string]bool{"webdav": false},
	})
	rec := do(t, s.handler(), "GET", "/api/instance", "", map[string]string{"Remote-User": "alice"})
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.User != "alice" || got.Version != version || got.ShareBaseURL != "https://team.example.com/share/" {
		t.Fatalf("unexpected %+v", got)
	}
	if got.Features["webdav"] || !got.Features["shares"] || len(got.Features) != len(knownFeatures) {
		t.Fatalf("features %+v", got.Features)
	}
}

func TestHealthReportsVersion(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	rec := do(t, s.handler(), "GET", "/health", "", nil)
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["status"] != "ok" || got["version"] != version {
		t.Fatalf("health %v", got)
	}
}

func TestInstanceInfoDerivesShareBaseURL(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	rec := do(t, s.handler(), "GET", "http://notes.example.com/api/instance", "", nil)
	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ShareBaseURL != "http://notes.example.com/share/" {
		t.Fatalf("share_base_url = %q", got.ShareBaseURL)
	}
}

func TestInstanceReportsDemoMode(t *testing.T) {
	for _, demo := range []bool{false, true} {
		s := newTestServer(t, AdminConfig{DemoMode: demo})
		rec := do(t, s.handler(), "GET", "/api/instance", "", nil)
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["demo_mode"] != demo {
			t.Errorf("demo_mode = %v, want %v", got["demo_mode"], demo)
		}
	}
}
