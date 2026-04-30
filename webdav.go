package main

import (
	"log"
	"net/http"

	"golang.org/x/net/webdav"
)

// ─── WebDAV ────────────────────────────────────────────────────────────────────

// newWebDAVHandler returns a read-only WebDAV handler over the vaults dir.
// Mounted at /webdav/. Only GET, HEAD, OPTIONS, PROPFIND are allowed; all
// mutating verbs (PUT, DELETE, MKCOL, COPY, MOVE, LOCK) get 405.
func (s *server) newWebDAVHandler() http.Handler {
	dav := &webdav.Handler{
		Prefix:     "/webdav",
		FileSystem: webdav.Dir(s.vaultsDir),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("webdav %s %s: %v", r.Method, r.URL.Path, err)
			}
		},
	}
	readOnlyMethods := map[string]bool{
		"GET": true, "HEAD": true, "OPTIONS": true, "PROPFIND": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !readOnlyMethods[r.Method] {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS, PROPFIND")
			http.Error(w, "method not allowed (read-only WebDAV)", http.StatusMethodNotAllowed)
			return
		}
		dav.ServeHTTP(w, r)
	})
}
