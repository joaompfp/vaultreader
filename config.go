// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
)

// knownFeatures lists every toggle an instance may switch off in
// config.json → "features". A missing key means enabled, so an empty
// config reproduces the original single-user behaviour.
var knownFeatures = []string{"shares", "share_writable", "webdav", "sync_status", "trash_empty"}

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
	if c.BasePath != "" && !validBasePath(c.BasePath) {
		return fmt.Errorf("base_path %q must look like \"/prefix\" (leading /, no trailing /, no . or .. segments, URL-safe characters)", c.BasePath)
	}
	return nil
}

// basePathRe: one or more "/segment" parts of unreserved URL characters,
// so the value can be spliced into HTML and URLs without escaping.
var basePathRe = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)+$`)

func validBasePath(p string) bool {
	if !basePathRe.MatchString(p) {
		return false
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// basePath is the public URL prefix the app is served under ("" = root).
func (s *server) basePath() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.BasePath
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

// shareBaseURL is the prefix the UI puts in front of a share token. An
// explicit share_base_url wins; otherwise it is derived from the request
// as <scheme>://<host><base_path>/share/.
func (s *server) shareBaseURL(r *http.Request) string {
	s.cfgMu.RLock()
	explicit := s.cfg.ShareBaseURL
	s.cfgMu.RUnlock()
	if explicit != "" {
		return explicit
	}
	return requestScheme(r) + "://" + r.Host + s.basePath() + "/share/"
}

// requestScheme is "https" or "http": X-Forwarded-Proto when the peer is
// a trusted proxy (first hop of a list, only those two values), else the
// connection's TLS state.
func requestScheme(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if isTrustedProxyPeer(peer) {
		proto := r.Header.Get("X-Forwarded-Proto")
		if i := strings.Index(proto, ","); i != -1 {
			proto = proto[:i]
		}
		switch strings.ToLower(strings.TrimSpace(proto)) {
		case "https":
			return "https"
		case "http":
			return "http"
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
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
