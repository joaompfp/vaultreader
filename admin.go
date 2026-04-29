package main

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Admin config ─────────────────────────────────────────────────────────────

type AdminConfig struct {
	AdminToken string   `json:"admin_token,omitempty"` // secret token for admin endpoints; empty = admin disabled
	RWPaths    []string `json:"rw_paths"`              // vault-relative paths that allow writes, e.g. "pessoal/agents/hermes/skills"
}

type server struct {
	vaultsDir  string
	appdataDir string
	idx        *NoteIndex
	static     http.Handler
	cfgMu      sync.RWMutex
	cfg        AdminConfig
	shares     *ShareStore
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
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	_ = json.Unmarshal(data, &s.cfg)
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

// isWritable returns true if vault+path is under one of the configured RW paths.
// rw_paths are vault-rooted, e.g. "pessoal" (whole vault) or "pessoal/agents/hermes/skills" (subfolder).
// vault is e.g. "pessoal"; path is vault-relative e.g. "agents/hermes/skills/foo.md".
func (s *server) isWritable(vault, path string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	full := vault + "/" + path
	for _, rw := range s.cfg.RWPaths {
		rw = strings.TrimSuffix(rw, "/")
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
		// Limit body to 32KB
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		var incoming AdminConfig
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&incoming); err != nil {
			errResponse(w, 400, "invalid json")
			return
		}
		s.cfgMu.Lock()
		// Merge: keep existing token unless explicitly set
		if incoming.AdminToken != "" {
			s.cfg.AdminToken = incoming.AdminToken
		}
		// Validate RWPaths — reject .. and absolute paths
		for _, p := range incoming.RWPaths {
			if strings.Contains(p, "..") || filepath.IsAbs(p) {
				s.cfgMu.Unlock()
				errResponse(w, 400, "invalid rw_path")
				return
			}
		}
		s.cfg.RWPaths = incoming.RWPaths
		s.cfgMu.Unlock()
		if err := s.saveConfig(); err != nil {
			log.Printf("saveConfig error: %v", err)
			errResponse(w, 500, "failed to save config")
			return
		}
		jsonResponse(w, s.cfg)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

// handleHealth returns a lightweight health check.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
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
