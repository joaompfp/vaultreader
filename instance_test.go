package main

import (
	"encoding/json"
	"testing"
)

func TestInstanceInfo(t *testing.T) {
	s := newTestServer(t, AdminConfig{
		RWPaths:      []string{"jll"},
		ShareBaseURL: "https://team.joao.date/share/",
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
	if got.User != "alice" || got.Version != version || got.ShareBaseURL != "https://team.joao.date/share/" {
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
