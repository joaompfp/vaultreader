// SPDX-License-Identifier: AGPL-3.0-only

package server

import "net/http"

// version is the build stamp, set by Run from cmd/vaultreader's -X main.version.
var version = "dev"

// InstanceInfo is everything the SPA needs to adapt to this instance.
type InstanceInfo struct {
	Version      string          `json:"version"`
	SiteName     string          `json:"site_name"`
	User         string          `json:"user"`
	Features     map[string]bool `json:"features"`
	RWPaths      []string        `json:"rw_paths"`
	ShareBaseURL string          `json:"share_base_url"`
	BasePath     string          `json:"base_path"`
	DefaultNote  string          `json:"default_note"`
	DemoMode     bool            `json:"demo_mode"`
}

func (s *server) handleInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	s.cfgMu.RLock()
	rw := append([]string{}, s.cfg.RWPaths...)
	defaultNote := s.cfg.DefaultNote
	demo := s.cfg.DemoMode
	s.cfgMu.RUnlock()
	jsonResponse(w, InstanceInfo{
		Version:      version,
		SiteName:     s.siteName(),
		User:         userFrom(r),
		Features:     s.resolvedFeatures(),
		RWPaths:      rw,
		ShareBaseURL: s.shareBaseURL(r),
		BasePath:     s.basePath(),
		DefaultNote:  defaultNote,
		DemoMode:     demo,
	})
}

// handleHealth is the liveness probe; version lets ops detect drift
// between instances that should run the same image.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"status": "ok", "version": version})
}
