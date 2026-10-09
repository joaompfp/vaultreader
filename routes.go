// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"
)

// routes is the single place where URL paths meet handlers.
func (s *server) routes() *http.ServeMux {
	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("static sub: %v", err)
	}
	staticHandler := http.FileServer(http.FS(subFS))

	index := s.indexHandler()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			index(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/vaults", s.handleVaults)
	mux.HandleFunc("/api/tree", s.handleTree)
	mux.HandleFunc("/api/note", s.handleNote)
	mux.HandleFunc("/api/note/export", s.feature("export_docx", s.handleExportNote))
	mux.HandleFunc("/api/templates/docx", s.feature("export_docx", s.handleDocxTemplates))
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/move", s.handleMove)
	mux.HandleFunc("/api/folder", s.handleFolder)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/resolve", s.handleResolve)
	mux.HandleFunc("/api/backlinks", s.handleBacklinks)
	mux.HandleFunc("/api/templates", s.handleTemplates)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/sync-status", s.feature("sync_status", s.handleSyncStatus))
	mux.HandleFunc("/api/vault-icon", s.handleVaultIcon)
	mux.HandleFunc("/api/file", s.handleFile)
	// Clean note URLs: /n/<vault>/<path-with-extension>. The shell is the
	// SPA — the frontend reads location.pathname on load and fetches the
	// note via /api/note. Real URLs make right-click→new-tab, bookmarks,
	// browser back/forward, and link sharing work natively.
	mux.HandleFunc("/n/", index)
	mux.HandleFunc("/api/admin/config", s.handleAdminConfig)
	mux.HandleFunc("/api/writable-paths", s.handleWritablePaths)
	mux.HandleFunc("/api/instance", s.handleInstance)
	mux.HandleFunc("/api/shares", s.feature("shares", s.handleShareList))
	mux.HandleFunc("/api/shares/create", s.feature("shares", s.handleShareCreate))
	mux.HandleFunc("/api/shares/revoke", s.feature("shares", s.handleShareRevoke))
	mux.HandleFunc("/api/shares/revoke-all", s.feature("shares", s.handleShareRevokeAll))
	mux.HandleFunc("/share/", s.feature("shares", s.handleShareView))
	mux.HandleFunc("/api/admin/restart", s.handleAdminRestart)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/trash", s.handleTrashList)
	mux.HandleFunc("/api/trash/restore", s.handleTrashRestore)
	mux.HandleFunc("/api/trash/empty", s.feature("trash_empty", s.handleTrashEmpty))
	mux.HandleFunc("/api/attachments", s.handleAttachments)
	mux.HandleFunc("/api/graph", s.handleGraph)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/webdav/", s.feature("webdav", s.newWebDAVHandler().ServeHTTP))
	return mux
}

// handler wraps the mux in the middleware chain used in production.
func (s *server) handler() http.Handler {
	return s.stripBasePath(gzipMiddleware(originGuard(s.requireUser(newRateLimiter(s.routes(), 240, time.Minute, rateExempt)))))
}

// stripBasePath makes everything behind it see root-relative paths, so
// routes, requireUser exemptions, originGuard and the rate limiter never
// deal with base_path. A request may arrive with the prefix (served
// directly) or without it (a proxy already stripped it); both work.
// base_path is the *public* prefix, so root-relative redirects get it
// prepended either way. "<base>" alone redirects to "<base>/".
func (s *server) stripBasePath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := s.basePath()
		if base == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == base {
			target := base + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		if strings.HasPrefix(r.URL.Path, base+"/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, base)
			r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, base)
			r = r2
		}
		next.ServeHTTP(&basePathWriter{ResponseWriter: w, base: base}, r)
	})
}

// basePathWriter prepends base to root-relative Location headers
// (ServeMux path-cleaning and trailing-slash redirects).
type basePathWriter struct {
	http.ResponseWriter
	base        string
	wroteHeader bool
}

func (w *basePathWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		h := w.Header()
		if loc := h.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
			h.Set("Location", w.base+loc)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *basePathWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *basePathWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// rateExempt: read-only, cacheable GETs that a single page view fans out
// into (note image embeds, vault icons, embedded static assets) skip the
// per-IP limiter, so an image-heavy note can't 429 itself. Everything
// else (/api/* incl. mutations, /share/*, /webdav/, the shell) is limited.
func rateExempt(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p == "/api/file" || p == "/api/vault-icon" {
		return true
	}
	if p == "/" || p == "/index.html" || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/share/") ||
		strings.HasPrefix(p, "/webdav/") || strings.HasPrefix(p, "/n/") {
		return false
	}
	st, err := fs.Stat(staticFiles, "static"+p)
	return err == nil && !st.IsDir()
}
