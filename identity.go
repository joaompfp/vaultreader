// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"strings"
)

// remoteUserHeader is set by Authelia via Traefik forwardAuth
// (authResponseHeaders), which overwrites any client-supplied value.
const remoteUserHeader = "Remote-User"

// userFrom returns who made the request, for attribution only — never
// for authorization. Missing or blank → "unknown".
func userFrom(r *http.Request) string {
	if u := strings.TrimSpace(r.Header.Get(remoteUserHeader)); u != "" {
		return u
	}
	return "unknown"
}

// requireUser makes the instance fail closed: with require_user on, a
// request that did not come through Authelia (no Remote-User) gets 401.
// Share links authenticate by token and /health is for probes.
func (s *server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.cfgMu.RLock()
		required := s.cfg.RequireUser
		s.cfgMu.RUnlock()
		if !required || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/share/") {
			next.ServeHTTP(w, r)
			return
		}
		if userFrom(r) == "unknown" {
			errResponse(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
