package main

import "net/http"

// version is stamped at build time: -ldflags "-X main.version=<git sha>".
var version = "dev"

// InstanceInfo is everything the SPA needs to adapt to this instance.
type InstanceInfo struct {
	Version      string          `json:"version"`
	User         string          `json:"user"`
	Features     map[string]bool `json:"features"`
	RWPaths      []string        `json:"rw_paths"`
	ShareBaseURL string          `json:"share_base_url"`
	DefaultNote  string          `json:"default_note"`
}

func (s *server) handleInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	s.cfgMu.RLock()
	rw := append([]string{}, s.cfg.RWPaths...)
	defaultNote := s.cfg.DefaultNote
	s.cfgMu.RUnlock()
	jsonResponse(w, InstanceInfo{
		Version:      version,
		User:         userFrom(r),
		Features:     s.resolvedFeatures(),
		RWPaths:      rw,
		ShareBaseURL: s.shareBaseURL(),
		DefaultNote:  defaultNote,
	})
}

// handleHealth is the liveness probe; version lets ops detect drift
// between instances that should run the same image.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"status": "ok", "version": version})
}
