package main

import (
	"fmt"
	"net/http"
	"strings"
)

// knownFeatures lists every toggle an instance may switch off in
// config.json → "features". A missing key means enabled, so an empty
// config reproduces the original single-user behaviour.
var knownFeatures = []string{"shares", "share_writable", "webdav", "sync_status", "trash_empty"}

const defaultShareBaseURL = "https://joao.date/notas/"

func validateConfig(c AdminConfig) error {
	for k := range c.Features {
		known := false
		for _, f := range knownFeatures {
			if k == f {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown feature %q (known: %s)", k, strings.Join(knownFeatures, ", "))
		}
	}
	if c.DefaultNote != "" && !strings.Contains(c.DefaultNote, "/") {
		return fmt.Errorf("default_note %q must be \"vault/path.md\"", c.DefaultNote)
	}
	if c.ShareBaseURL != "" && !strings.HasSuffix(c.ShareBaseURL, "/") {
		return fmt.Errorf("share_base_url %q must end with /", c.ShareBaseURL)
	}
	return nil
}

func (s *server) enabled(name string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	v, ok := s.cfg.Features[name]
	return !ok || v
}

func (s *server) resolvedFeatures() map[string]bool {
	out := make(map[string]bool, len(knownFeatures))
	for _, f := range knownFeatures {
		out[f] = s.enabled(f)
	}
	return out
}

func (s *server) shareBaseURL() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg.ShareBaseURL == "" {
		return defaultShareBaseURL
	}
	return s.cfg.ShareBaseURL
}

// feature gates a route: a disabled feature answers 404, as if the
// endpoint did not exist.
func (s *server) feature(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled(name) {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}
