// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"sort"
)

// ─── Vaults ───────────────────────────────────────────────────────────────────

func (s *server) handleVaults(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && !shouldSkip(e.Name()) {
			names = append(names, e.Name())
		}
	}
	vaults := s.orderVaults(names)
	jsonResponse(w, vaults)
}

// orderVaults returns names with config vault_order entries first (in
// that order; absent ones skipped), then the rest alphabetically.
func (s *server) orderVaults(names []string) []string {
	s.cfgMu.RLock()
	order := append([]string{}, s.cfg.VaultOrder...)
	s.cfgMu.RUnlock()
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	out := []string{}
	for _, v := range order {
		if found[v] {
			out = append(out, v)
			delete(found, v)
		}
	}
	var extra []string
	for v := range found {
		extra = append(extra, v)
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func (s *server) handleTree(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	nodes, err := buildTree(vp, vp)
	if err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	jsonResponse(w, nodes)
}

// spaCSP is the Content-Security-Policy for the SPA shell (/ and /n/*).
// Second layer behind DOMPurify. The bundled Alpine build needs
// 'unsafe-eval' and the shell uses inline scripts (no build step, so no
// nonces); the protection that matters is connect-src / form-action /
// object-src / base-uri / frame-ancestors. img-src allows https: because
// notes embed external images. /share/* sends its own nonce-based CSP.
const spaCSP = "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; " +
	"media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; frame-src 'self'; " +
	"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'"

// indexHandler serves the SPA shell. base_path and analytics are fixed at
// startup, so the rewritten shell and its CSP are computed once here.
func (s *server) indexHandler() http.HandlerFunc {
	data, err := fs.ReadFile(staticFiles, "static/index.html")
	s.cfgMu.RLock()
	analytics := s.cfg.Analytics
	s.cfgMu.RUnlock()
	if err == nil {
		data = withAnalytics(withBasePath(data, s.basePath()), analytics)
	}
	csp := shellCSP(analytics)
	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.Error(w, "index.html not found", 500)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		_, _ = w.Write(data)
	}
}

// codemirrorTag is the one asset tag outside <head>: the editor bundle.
const codemirrorTag = `<script src="/codemirror.bundle.js">`

// withBasePath tells the shell its public prefix: a <meta name="vr-base">
// (right after <meta charset>) the frontend reads once, and the
// root-absolute src="/…" / href="/…" attributes of the bundled assets
// rewritten to "<base>/…". Only the <head> section and the codemirror
// bundle tag are rewritten; the inline application script is left alone.
// base is validated to URL-safe characters (validBasePath), so no escaping
// is needed. With no base the asset URLs stay as they are.
func withBasePath(page []byte, base string) []byte {
	const charset = `<meta charset="UTF-8">`
	meta := `<meta name="vr-base" content="` + base + `">`
	page = bytes.Replace(page, []byte(charset), []byte(charset+"\n  "+meta), 1)
	if base == "" {
		return page
	}
	head, rest, found := bytes.Cut(page, []byte("</head>"))
	if !found {
		return page
	}
	rewrite := func(b []byte) []byte {
		b = bytes.ReplaceAll(b, []byte(` src="/`), []byte(` src="`+base+`/`))
		return bytes.ReplaceAll(b, []byte(` href="/`), []byte(` href="`+base+`/`))
	}
	rest = bytes.Replace(rest, []byte(codemirrorTag),
		[]byte(`<script src="`+base+`/codemirror.bundle.js">`), 1)
	return append(append(rewrite(head), "</head>"...), rest...)
}
