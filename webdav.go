// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"log"
	"net/http"
	"path"
	"strings"

	"golang.org/x/net/webdav"
)

// ─── WebDAV ────────────────────────────────────────────────────────────────────

// newWebDAVHandler returns a read-only WebDAV handler over the vaults dir.
// Mounted at /webdav/. Only GET, HEAD, OPTIONS, PROPFIND are allowed; all
// mutating verbs (PUT, DELETE, MKCOL, COPY, MOVE, LOCK) get 405.
// PROPFIND hrefs are built from the handler's Prefix, so it gets the
// public base_path too (requests arrive here with base_path stripped).
func (s *server) newWebDAVHandler() http.Handler {
	fsys := webdav.Dir(s.vaultsDir)
	locks := webdav.NewMemLS()
	logger := func(r *http.Request, err error) {
		if err != nil {
			log.Printf("webdav %s %s: %v", r.Method, r.URL.Path, err)
		}
	}
	readOnlyMethods := map[string]bool{
		"GET": true, "HEAD": true, "OPTIONS": true, "PROPFIND": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vault bytes on the app origin: same Content-Type decision as
		// /api/file (never sniffed), and every response sandboxed since
		// WebDAV is never embedded by the SPA.
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasSuffix(r.URL.Path, "/") {
			setRawFileHeaders(w, strings.ToLower(path.Ext(r.URL.Path)))
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		if !readOnlyMethods[r.Method] {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS, PROPFIND")
			http.Error(w, "method not allowed (read-only WebDAV)", http.StatusMethodNotAllowed)
			return
		}
		base := s.basePath()
		dav := &webdav.Handler{Prefix: base + "/webdav", FileSystem: fsys, LockSystem: locks, Logger: logger}
		if base != "" {
			r = r.Clone(r.Context())
			r.URL.Path = base + r.URL.Path
			r.URL.RawPath = ""
		}
		dav.ServeHTTP(w, r)
	})
}
