// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Admin config ─────────────────────────────────────────────────────────────

type AdminConfig struct {
	AdminToken string   `json:"admin_token,omitempty"` // secret token for admin endpoints; empty = admin disabled
	RWPaths    []string `json:"rw_paths"`              // vault-relative paths that allow writes, e.g. "notes/agents/skills"

	SiteName     string           `json:"site_name,omitempty"`      // browser tab title; empty = "VaultReader"
	RequireUser  bool             `json:"require_user,omitempty"`   // 401 without Remote-User (team instance)
	ShareBaseURL string           `json:"share_base_url,omitempty"` // public prefix for share links; empty = derived from the request
	BasePath     string           `json:"base_path,omitempty"`      // public URL prefix, e.g. "/vaultreader"; empty = served at /
	DefaultNote  string           `json:"default_note,omitempty"`   // "vault/path.md" opened when nothing is restored
	DemoMode     bool             `json:"demo_mode,omitempty"`      // public demo: the SPA shows a "changes are reset" banner
	VaultOrder   []string         `json:"vault_order,omitempty"`    // vaults listed first, in this order; the rest follow alphabetically
	Features     map[string]bool  `json:"features,omitempty"`       // missing key = enabled; see knownFeatures
	Analytics    *AnalyticsConfig `json:"analytics,omitempty"`      // optional Umami tracking tag in the SPA shell
}

type server struct {
	vaultsDir  string
	appdataDir string
	idx        *NoteIndex
	static     http.Handler
	cfgMu      sync.RWMutex
	cfg        AdminConfig
	shares     *ShareStore
	auditLog   *auditLog
	shutdown   chan struct{}
}

func (s *server) configPath() string {
	return filepath.Join(s.appdataDir, "config.json")
}

func (s *server) loadConfig() {
	data, err := os.ReadFile(s.configPath())
	if err != nil {
		return // no config yet → empty (all writes blocked by default)
	}
	cfg, err := parseConfig(data)
	if err != nil {
		log.Fatalf("config: %s: %v", s.configPath(), err)
	}
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

// parseConfig decodes config.json strictly: an unknown key (a typo such
// as "require_usr") is an error, so a misspelt security setting fails
// startup instead of silently reverting to the permissive default.
func parseConfig(data []byte) (AdminConfig, error) {
	var cfg AdminConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return AdminConfig{}, err
	}
	if dec.More() {
		return AdminConfig{}, fmt.Errorf("trailing data after the config object")
	}
	if err := validateConfig(cfg); err != nil {
		return AdminConfig{}, err
	}
	return cfg, nil
}

func (s *server) saveConfig() error {
	s.cfgMu.RLock()
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	s.cfgMu.RUnlock()
	if err != nil {
		return err
	}
	tmp := s.configPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.configPath())
}

// isWritable returns true if vault+path is under one of the configured RW paths
// and has no hidden (dot) segment. Every mutating handler goes through it.
// rw_paths are vault-rooted, e.g. "notes" (whole vault) or "notes/agents/skills" (subfolder).
// vault is e.g. "notes"; path is vault-relative e.g. "agents/skills/foo.md".
func (s *server) isWritable(vault, path string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	// Judge the cleaned path: callers pass the raw query value, and a raw
	// "rw/../ro" would otherwise pass the prefix test for "rw". Anything
	// escaping the vault is never writable.
	clean := pathpkg.Clean(filepath.ToSlash(path))
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return false
	}
	// Hidden paths (.git, .obsidian, .trash, …) are never writable via the
	// API, whatever rw_paths say.
	if hasDotSegment(clean) {
		return false
	}
	full := vault
	if clean != "." {
		full += "/" + clean
	}
	for _, rw := range s.cfg.RWPaths {
		rw = strings.TrimSuffix(pathpkg.Clean(filepath.ToSlash(rw)), "/")
		if rw == vault || // whole vault writable
			full == rw || // exact match
			strings.HasPrefix(full, rw+"/") { // under rw dir
			return true
		}
	}
	return false
}

// ─── Admin handlers ───────────────────────────────────────────────────────────

func (s *server) requireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	s.cfgMu.RLock()
	token := s.cfg.AdminToken
	s.cfgMu.RUnlock()
	if token == "" {
		errResponse(w, 403, "admin not configured")
		return false
	}
	headerToken := r.Header.Get("X-Admin-Token")
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(token)) != 1 {
		log.Printf("admin: invalid token from %s", r.RemoteAddr)
		errResponse(w, 403, "unauthorized")
		return false
	}
	return true
}

// handleWritablePaths exposes only the rw_paths list (no admin token).
// The list isn't sensitive — knowing which vaults/folders are writable
// only confirms what the writes themselves would already reveal. Lets
// the SPA disable write-related UI (paste-append, edit toggle, etc.)
// without needing the admin token.
func (s *server) handleWritablePaths(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	s.cfgMu.RLock()
	paths := make([]string, len(s.cfg.RWPaths))
	copy(paths, s.cfg.RWPaths)
	s.cfgMu.RUnlock()
	jsonResponse(w, map[string][]string{"rw_paths": paths})
}

// adminPatch is the body of POST /api/admin/config: the settings editable
// at runtime. A nil field is left unchanged; everything else in
// config.json (features, base_path, …) is file-only.
type adminPatch struct {
	AdminToken *string   `json:"admin_token"`
	RWPaths    *[]string `json:"rw_paths"`
	SiteName   *string   `json:"site_name"`
}

func (s *server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminToken(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		jsonResponse(w, cfg)
	case http.MethodPost:
		// Partial update: only the keys present in the body change, so the
		// SPA can save one setting without resending (or wiping) the rest.
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var patch adminPatch
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&patch); err != nil {
			errResponse(w, 400, "invalid json")
			return
		}
		if patch.RWPaths != nil {
			for _, p := range *patch.RWPaths {
				if strings.Contains(p, "..") || filepath.IsAbs(p) {
					errResponse(w, 400, "invalid rw_path")
					return
				}
			}
		}
		if patch.SiteName != nil {
			*patch.SiteName = strings.TrimSpace(*patch.SiteName)
			if err := validateSiteName(*patch.SiteName); err != nil {
				errResponse(w, 400, err.Error())
				return
			}
		}
		s.cfgMu.Lock()
		if patch.AdminToken != nil && *patch.AdminToken != "" {
			s.cfg.AdminToken = *patch.AdminToken
		}
		if patch.RWPaths != nil {
			s.cfg.RWPaths = *patch.RWPaths
		}
		if patch.SiteName != nil {
			s.cfg.SiteName = *patch.SiteName
		}
		cfg := s.cfg
		s.cfgMu.Unlock()
		if err := s.saveConfig(); err != nil {
			log.Printf("saveConfig error: %v", err)
			errResponse(w, 500, "failed to save config")
			return
		}
		jsonResponse(w, cfg)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

func (s *server) handleAdminRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, 405, "method not allowed")
		return
	}
	if !s.requireAdminToken(w, r) {
		return
	}
	jsonResponse(w, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(s.shutdown)
	}()
}
