package main

import (
	"io/fs"
	"net/http"
	"os"
	"sort"
)

// ─── Vaults ───────────────────────────────────────────────────────────────────

// preferred vault display order — vaults not listed here appear alphabetically after
var vaultOrder = []string{"agent-wiki", "pessoal", "work", "pcp", "sosracismo", "projects"}

func (s *server) handleVaults(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && !shouldSkip(e.Name()) {
			found[e.Name()] = true
		}
	}
	// ordered first, then any extras alphabetically
	var vaults []string
	for _, v := range vaultOrder {
		if found[v] {
			vaults = append(vaults, v)
			delete(found, v)
		}
	}
	var extra []string
	for v := range found {
		extra = append(extra, v)
	}
	sort.Strings(extra)
	vaults = append(vaults, extra...)
	jsonResponse(w, vaults)
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

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(staticFiles, "static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", spaCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	_, _ = w.Write(data)
}
