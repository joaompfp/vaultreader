package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

//go:embed static
var staticFiles embed.FS

var (
	vaultsDir   = flag.String("vaults", "/vaults", "path to vaults directory")
	port        = flag.String("port", "8080", "port to listen on")
	appdataDir  = flag.String("appdata", "/appdata", "path to appdata directory (vault icons, customisations)")
)

// ─── Data structures ──────────────────────────────────────────────────────────

type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"isDir"`
	MTime    int64       `json:"mtime"`
	Size     int64       `json:"size"`
	Children []*TreeNode `json:"children,omitempty"`
}

type NoteResponse struct {
	Raw         string         `json:"raw"`
	HTML        string         `json:"html"`
	Frontmatter map[string]any `json:"frontmatter"`
	Backlinks   []BacklinkRef  `json:"backlinks"`
	MTime       int64          `json:"mtime"`
}

type BacklinkRef struct {
	Vault   string `json:"vault"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

type SearchResult struct {
	Vault   string `json:"vault"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

type ResolveResult struct {
	Vault string `json:"vault"`
	Path  string `json:"path"`
}

type NoteRef struct {
	Vault string
	Path  string
	Title string
}

type NoteIndex struct {
	mu       sync.RWMutex
	outbound map[string][]string // vaultKey -> []target names
	inbound  map[string][]string // normalizedName -> []vaultKey paths that link to it
	allNotes map[string]NoteRef  // normalizedName -> NoteRef
}

// vaultKey is "vault:path" — unique across all vaults
func vaultKey(vault, path string) string {
	return vault + ":" + path
}

var (
	wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+?)(?:\|([^\]]+?))?\]\]`)
	headingRe  = regexp.MustCompile(`(?m)^#+\s+(.+)$`)
)

var md goldmark.Markdown

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
			extension.GFM,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
}

// ─── Index ────────────────────────────────────────────────────────────────────

func newIndex() *NoteIndex {
	return &NoteIndex{
		outbound: make(map[string][]string),
		inbound:  make(map[string][]string),
		allNotes: make(map[string]NoteRef),
	}
}

func normalizeName(name string) string {
	// strip .md extension, lowercase
	name = strings.TrimSuffix(name, ".md")
	return strings.ToLower(name)
}

func extractTitle(content, filename string) string {
	// look for first H1
	m := headingRe.FindStringSubmatch(content)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSuffix(filepath.Base(filename), ".md")
}

func (idx *NoteIndex) buildAll(vaultsPath string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.outbound = make(map[string][]string)
	idx.inbound = make(map[string][]string)
	idx.allNotes = make(map[string]NoteRef)

	entries, err := os.ReadDir(vaultsPath)
	if err != nil {
		log.Printf("index: cannot read vaults dir %s: %v", vaultsPath, err)
		return
	}

	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		vaultName := e.Name()
		vaultPath := filepath.Join(vaultsPath, vaultName)
		_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if shouldSkip(info.Name()) || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			rel, _ := filepath.Rel(vaultPath, path)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			content := string(data)
			_, body := parseFrontmatter(content)
			title := extractTitle(body, rel)
			nname := normalizeName(filepath.Base(rel))
			key := vaultKey(vaultName, rel)

			// Add compound key for vault-scoped lookup
			compoundKey := vaultName + ":" + nname
			idx.allNotes[compoundKey] = NoteRef{Vault: vaultName, Path: rel, Title: title}
			// Keep global key as fallback
			idx.allNotes[nname] = NoteRef{Vault: vaultName, Path: rel, Title: title}

			// extract wikilinks
			matches := wikilinkRe.FindAllStringSubmatch(body, -1)
			var targets []string
			for _, m := range matches {
				targets = append(targets, normalizeName(m[1]))
			}
			idx.outbound[key] = targets
			for _, t := range targets {
				idx.inbound[t] = append(idx.inbound[t], key)
			}
			_ = nname
			return nil
		})
	}
}

func (idx *NoteIndex) updateNote(vault, path, content string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	_, body := parseFrontmatter(content)
	title := extractTitle(body, path)
	nname := normalizeName(filepath.Base(path))
	key := vaultKey(vault, path)

	// update allNotes (both compound and global keys)
	compoundKey := vault + ":" + nname
	idx.allNotes[compoundKey] = NoteRef{Vault: vault, Path: path, Title: title}
	idx.allNotes[nname] = NoteRef{Vault: vault, Path: path, Title: title}

	// remove old outbound links from inbound index
	old := idx.outbound[key]
	for _, t := range old {
		links := idx.inbound[t]
		var filtered []string
		for _, l := range links {
			if l != key {
				filtered = append(filtered, l)
			}
		}
		idx.inbound[t] = filtered
	}

	// add new outbound links
	matches := wikilinkRe.FindAllStringSubmatch(body, -1)
	var targets []string
	for _, m := range matches {
		targets = append(targets, normalizeName(m[1]))
	}
	idx.outbound[key] = targets
	for _, t := range targets {
		idx.inbound[t] = append(idx.inbound[t], key)
	}
}

func (idx *NoteIndex) removeNote(vault, path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	nname := normalizeName(filepath.Base(path))
	key := vaultKey(vault, path)
	compoundKey := vault + ":" + nname

	delete(idx.allNotes, compoundKey)
	delete(idx.allNotes, nname)

	// remove outbound links from inbound index
	old := idx.outbound[key]
	for _, t := range old {
		links := idx.inbound[t]
		var filtered []string
		for _, l := range links {
			if l != key {
				filtered = append(filtered, l)
			}
		}
		idx.inbound[t] = filtered
	}
	delete(idx.outbound, key)
}

func (idx *NoteIndex) resolve(name, preferVault string) (string, string, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	nname := normalizeName(name)
	// Pass 1: prefer current vault
	if preferVault != "" {
		if ref, ok := idx.allNotes[preferVault+":"+nname]; ok {
			return ref.Vault, ref.Path, true
		}
	}
	// Pass 2: global fallback
	ref, ok := idx.allNotes[nname]
	if !ok {
		return "", "", false
	}
	return ref.Vault, ref.Path, true
}

func (idx *NoteIndex) getBacklinks(vault, path string) []BacklinkRef {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	nname := normalizeName(filepath.Base(path))
	keys := idx.inbound[nname]

	var refs []BacklinkRef
	seen := make(map[string]bool)
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		v, p := parts[0], parts[1]
		ref, ok := idx.allNotes[normalizeName(filepath.Base(p))]
		title := ref.Title
		if !ok {
			title = strings.TrimSuffix(filepath.Base(p), ".md")
		}
		refs = append(refs, BacklinkRef{Vault: v, Path: p, Title: title, Excerpt: ""})
	}
	return refs
}

// ─── Markdown rendering ───────────────────────────────────────────────────────

func renderMarkdown(raw string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(raw), &buf); err != nil {
		return "<pre>" + raw + "</pre>"
	}
	return buf.String()
}

func renderWikilinks(htmlStr string, currentVault string, idx *NoteIndex) string {
	return wikilinkRe.ReplaceAllStringFunc(htmlStr, func(match string) string {
		sub := wikilinkRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		name := sub[1]
		alias := sub[2]
		if alias == "" {
			alias = name
		}

		v, p, ok := idx.resolve(name, currentVault)
		if !ok {
			return fmt.Sprintf(`<a class="wikilink wikilink-missing" data-name="%s">%s</a>`, name, alias)
		}
		return fmt.Sprintf(`<a href="#" class="wikilink" data-vault="%s" data-path="%s">%s</a>`, v, p, alias)
	})
}

// ─── Frontmatter ─────────────────────────────────────────────────────────────

func parseFrontmatter(content string) (map[string]any, string) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return nil, content
	}
	// find closing ---
	rest := content[4:] // skip "---\n"
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, content
	}
	yamlStr := rest[:idx]
	body := rest[idx+4:] // skip "\n---"
	if strings.HasPrefix(body, "\n") {
		body = body[1:]
	} else if strings.HasPrefix(body, "\r\n") {
		body = body[2:]
	}

	fm := make(map[string]any)
	if err := yaml.Unmarshal([]byte(yamlStr), &fm); err != nil {
		return nil, content
	}
	return fm, body
}

// ─── File tree ────────────────────────────────────────────────────────────────

func shouldSkip(name string) bool {
	return strings.HasPrefix(name, ".") ||
		strings.Contains(name, ".sync-conflict-") ||
		strings.HasSuffix(name, ".tmp-vaultreader")
}

func buildTree(root, current string) ([]*TreeNode, error) {
	entries, err := os.ReadDir(current)
	if err != nil {
		return nil, err
	}

	var nodes []*TreeNode
	// dirs first, then files
	var dirs, files []os.DirEntry
	for _, e := range entries {
		if shouldSkip(e.Name()) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else if strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e)
		}
	}

	for _, e := range dirs {
		rel, _ := filepath.Rel(root, filepath.Join(current, e.Name()))
		children, _ := buildTree(root, filepath.Join(current, e.Name()))
		var mtime int64
		if info, err := e.Info(); err == nil {
			mtime = info.ModTime().Unix()
		}
		nodes = append(nodes, &TreeNode{
			Name:     e.Name(),
			Path:     rel,
			IsDir:    true,
			MTime:    mtime,
			Children: children,
		})
	}
	for _, e := range files {
		rel, _ := filepath.Rel(root, filepath.Join(current, e.Name()))
		var mtime, size int64
		if info, err := e.Info(); err == nil {
			mtime = info.ModTime().Unix()
			size = info.Size()
		}
		nodes = append(nodes, &TreeNode{
			Name:  e.Name(),
			Path:  rel,
			IsDir: false,
			MTime: mtime,
			Size:  size,
		})
	}
	return nodes, nil
}

// ─── Search ───────────────────────────────────────────────────────────────────

func searchVault(vaultPath, vaultName, query string) []SearchResult {
	query = strings.ToLower(query)
	var results []SearchResult

	_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || shouldSkip(info.Name()) {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		if len(results) >= 20 {
			return nil
		}

		rel, _ := filepath.Rel(vaultPath, path)
		nameMatch := strings.Contains(strings.ToLower(info.Name()), query)

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		contentLower := strings.ToLower(content)
		contentMatch := strings.Contains(contentLower, query)

		if !nameMatch && !contentMatch {
			return nil
		}

		_, body := parseFrontmatter(content)
		title := extractTitle(body, rel)

		// build excerpt around first match
		excerpt := ""
		if contentMatch {
			pos := strings.Index(contentLower, query)
			start := pos - 60
			if start < 0 {
				start = 0
			}
			end := pos + 60 + len(query)
			if end > len(content) {
				end = len(content)
			}
			excerpt = "..." + strings.ReplaceAll(content[start:end], "\n", " ") + "..."
		}

		results = append(results, SearchResult{
			Vault:   vaultName,
			Path:    rel,
			Title:   title,
			Excerpt: excerpt,
		})
		return nil
	})
	return results
}

// ─── Save ─────────────────────────────────────────────────────────────────────

func saveNote(vaultPath, notePath, content string) error {
	full := filepath.Join(vaultPath, notePath)
	// ensure directory exists
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	tmp := full + ".tmp-vaultreader"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

// ─── Gzip middleware ──────────────────────────────────────────────────────────

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (g *gzipResponseWriter) Write(b []byte) (int, error)  { return g.Writer.Write(b) }
func (g *gzipResponseWriter) Header() http.Header          { return g.ResponseWriter.Header() }
func (g *gzipResponseWriter) WriteHeader(code int)         { g.ResponseWriter.WriteHeader(code) }

// ─── HTTP handlers ────────────────────────────────────────────────────────────

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode: %v", err)
	}
}

func errResponse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── Admin config ─────────────────────────────────────────────────────────────

type AdminConfig struct {
	RWPaths []string `json:"rw_paths"` // vault-relative paths that allow writes, e.g. "pessoal/agents/hermes/skills"
}

type server struct {
	vaultsDir  string
	appdataDir string
	idx        *NoteIndex
	static     http.Handler
	cfgMu      sync.RWMutex
	cfg        AdminConfig
	shares     *ShareStore
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
	return os.WriteFile(s.configPath(), data, 0644)
}

// isWritable returns true if vault+path is under one of the configured RW paths.
// vault is e.g. "pessoal", path is vault-relative e.g. "agents/hermes/skills/foo.md"
func (s *server) isWritable(vault, path string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	full := vault + "/" + path
	for _, rw := range s.cfg.RWPaths {
		rw = strings.TrimSuffix(rw, "/")
		if rw == vault || // whole vault
			strings.HasPrefix(full, rw+"/") || // file under rw dir
			full == rw { // exact match
			return true
		}
	}
	return false
}

// ─── Admin handlers ───────────────────────────────────────────────────────────

func (s *server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		jsonResponse(w, cfg)
	case http.MethodPost:
		var incoming AdminConfig
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			errResponse(w, 400, "invalid json")
			return
		}
		s.cfgMu.Lock()
		s.cfg = incoming
		s.cfgMu.Unlock()
		if err := s.saveConfig(); err != nil {
			errResponse(w, 500, "failed to save config: "+err.Error())
			return
		}
		jsonResponse(w, s.cfg)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

func (s *server) handleAdminRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, 405, "method not allowed")
		return
	}
	jsonResponse(w, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0) // Docker restart policy brings it back up
	}()
}

// ─── Share system ─────────────────────────────────────────────────────────────

type ShareEntry struct {
	Token     string `json:"token"`
	Vault     string `json:"vault"`
	Path      string `json:"path"`
	Writable  bool   `json:"writable"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	CreatedAt int64  `json:"created_at"`
	Label     string `json:"label,omitempty"`
}

type ShareStore struct {
	mu      sync.RWMutex
	entries map[string]*ShareEntry
	path    string
}

func newShareStore(appdataDir string) *ShareStore {
	ss := &ShareStore{entries: make(map[string]*ShareEntry), path: filepath.Join(appdataDir, "shares.json")}
	ss.load()
	return ss
}

func (ss *ShareStore) load() {
	data, err := os.ReadFile(ss.path)
	if err != nil { return }
	var entries []*ShareEntry
	if err := json.Unmarshal(data, &entries); err != nil { return }
	ss.mu.Lock(); defer ss.mu.Unlock()
	for _, e := range entries { ss.entries[e.Token] = e }
}

func (ss *ShareStore) save() error {
	ss.mu.RLock()
	entries := make([]*ShareEntry, 0, len(ss.entries))
	for _, e := range ss.entries { entries = append(entries, e) }
	ss.mu.RUnlock()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil { return err }
	return os.WriteFile(ss.path, data, 0644)
}

func (ss *ShareStore) create(vault, path string, writable bool, ttl int64, label string) *ShareEntry {
	b := make([]byte, 12); _, _ = rand.Read(b)
	e := &ShareEntry{Token: fmt.Sprintf("%x", b), Vault: vault, Path: path,
		Writable: writable, CreatedAt: time.Now().Unix(), Label: label}
	if ttl > 0 { e.ExpiresAt = time.Now().Unix() + ttl }
	ss.mu.Lock(); ss.entries[e.Token] = e; ss.mu.Unlock()
	_ = ss.save(); return e
}

func (ss *ShareStore) get(token string) (*ShareEntry, bool) {
	ss.mu.RLock(); e, ok := ss.entries[token]; ss.mu.RUnlock()
	if !ok { return nil, false }
	if e.ExpiresAt > 0 && time.Now().Unix() > e.ExpiresAt { return nil, false }
	return e, true
}

func (ss *ShareStore) revoke(token string) {
	ss.mu.Lock(); delete(ss.entries, token); ss.mu.Unlock(); _ = ss.save()
}

func (ss *ShareStore) list() []*ShareEntry {
	ss.mu.RLock(); defer ss.mu.RUnlock()
	now := time.Now().Unix(); out := make([]*ShareEntry, 0)
	for _, e := range ss.entries { if e.ExpiresAt == 0 || now <= e.ExpiresAt { out = append(out, e) } }
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (s *server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { errResponse(w, 405, "method not allowed"); return }
	var req struct {
		Vault    string `json:"vault"`
		Path     string `json:"path"`
		Writable bool   `json:"writable"`
		TTL      int64  `json:"ttl"`
		Label    string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { errResponse(w, 400, "bad json"); return }
	if req.Vault == "" || req.Path == "" { errResponse(w, 400, "vault and path required"); return }
	jsonResponse(w, s.shares.create(req.Vault, req.Path, req.Writable, req.TTL, req.Label))
}

func (s *server) handleShareList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { errResponse(w, 405, "method not allowed"); return }
	jsonResponse(w, s.shares.list())
}

func (s *server) handleShareRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete { errResponse(w, 405, "method not allowed"); return }
	token := r.URL.Query().Get("token")
	if token == "" { errResponse(w, 400, "token required"); return }
	s.shares.revoke(token); jsonResponse(w, map[string]string{"status": "revoked"})
}

func (s *server) handleShareView(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/share/")
	if token == "" { http.NotFound(w, r); return }
	e, ok := s.shares.get(token)
	if !ok { http.Error(w, "Share link not found or expired.", http.StatusNotFound); return }

	if r.Method == http.MethodPut {
		if !e.Writable { errResponse(w, 403, "read-only share"); return }
		vp, ok := s.vaultPath(e.Vault)
		if !ok { errResponse(w, 400, "vault unavailable"); return }
		var body struct{ Raw string `json:"raw"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := saveNote(vp, e.Path, body.Raw); err != nil { errResponse(w, 500, err.Error()); return }
		s.idx.updateNote(e.Vault, e.Path, body.Raw)
		jsonResponse(w, map[string]string{"status": "saved"}); return
	}

	vp, ok := s.vaultPath(e.Vault)
	if !ok { http.Error(w, "Vault not available.", http.StatusNotFound); return }
	full, ok2 := s.safePath(vp, e.Path)
	if !ok2 { http.Error(w, "Invalid path.", http.StatusNotFound); return }
	data, err := os.ReadFile(full)
	if err != nil { http.Error(w, "Note not found.", http.StatusNotFound); return }

	raw := string(data)
	title := extractTitle(raw, e.Path)
	// Strip YAML frontmatter (--- ... ---) before rendering
	_, body := parseFrontmatter(raw)
	var buf bytes.Buffer; _ = md.Convert([]byte(body), &buf)

	expiresStr := "Never"
	if e.ExpiresAt > 0 { expiresStr = time.Unix(e.ExpiresAt, 0).Format("2 Jan 2006 15:04") }
	modeStr, modeCls := "Read-only", " ro"
	if e.Writable { modeStr, modeCls = "Editable", "" }

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title><style>
:root{--ac:#b91c1c;--t:#1a1a1a;--t2:#555;--t3:#888;--bd:#e0e0e0;--bg:#fff;--bg2:#f5f5f5}
@media(prefers-color-scheme:dark){:root{--t:#cdd6f4;--t2:#a6adc8;--t3:#6c7086;--bd:#45475a;--bg:#1e1e2e;--bg2:#181825}}
*{box-sizing:border-box;margin:0;padding:0}body{font-family:system-ui,sans-serif;font-size:16px;line-height:1.7;color:var(--t);background:var(--bg)}
.bar{display:flex;align-items:center;gap:10px;padding:9px 20px;border-bottom:1px solid var(--bd);font-size:12px;color:var(--t3)}
.bar strong{color:var(--ac);font-size:13px;font-weight:600}
.badge{padding:2px 8px;border-radius:10px;font-size:11px;font-weight:600;color:#fff;background:var(--ac)}.badge.ro{background:#666}
.content{max-width:740px;margin:0 auto;padding:36px 20px 80px}
h1,h2,h3{margin:1.3em 0 .4em;line-height:1.3}h1{font-size:2em}h2{font-size:1.5em}h3{font-size:1.2em}
p{margin:.7em 0}a{color:var(--ac)}code{background:var(--bg2);border-radius:4px;padding:2px 5px;font-size:.88em;font-family:monospace}
pre{background:var(--bg2);border-radius:8px;padding:14px;overflow-x:auto;margin:1em 0}pre code{background:none;padding:0}
blockquote{border-left:3px solid var(--ac);padding-left:14px;color:var(--t2);margin:1em 0}
ul,ol{padding-left:1.4em;margin:.5em 0}li{margin:.2em 0}
table{border-collapse:collapse;width:100%%;margin:1em 0}th,td{border:1px solid var(--bd);padding:7px 11px}th{background:var(--bg2)}
img{max-width:100%%}hr{border:none;border-top:1px solid var(--bd);margin:1.5em 0}
.foot{text-align:center;padding:20px;font-size:12px;color:var(--t3);border-top:1px solid var(--bd)}.foot a{color:var(--t3);text-decoration:none}
</style></head><body>
<div class="bar"><strong>%s</strong><span class="badge%s">%s</span><span>Expires: %s</span>
<span style="flex:1"></span><a href="https://notes.joao.date" style="color:var(--t3);font-size:11px">VaultReader</a></div>
<div class="content">%s</div>
<div class="foot">Shared via <a href="https://notes.joao.date">VaultReader</a></div>
</body></html>`, title, title, modeCls, modeStr, expiresStr, buf.String())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}


// handleVaultIcon serves a custom icon from appdata/icons/<vault>.(png|svg|jpg|webp)
// Falls back to a transparent 1x1 PNG if no custom icon exists.
func (s *server) handleVaultIcon(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	if vault == "" || strings.Contains(vault, "..") || strings.Contains(vault, "/") {
		http.NotFound(w, r)
		return
	}
	iconsDir := filepath.Join(s.appdataDir, "icons")
	for _, ext := range []string{".png", ".svg", ".jpg", ".webp"} {
		p := filepath.Join(iconsDir, vault+ext)
		if _, err := os.Stat(p); err == nil {
			switch ext {
			case ".svg":
				w.Header().Set("Content-Type", "image/svg+xml")
			case ".jpg":
				w.Header().Set("Content-Type", "image/jpeg")
			case ".webp":
				w.Header().Set("Content-Type", "image/webp")
			default:
				w.Header().Set("Content-Type", "image/png")
			}
			w.Header().Set("Cache-Control", "max-age=3600")
			http.ServeFile(w, r, p)
			return
		}
	}
	// No icon found — return 204 so frontend knows to use fallback
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) vaultPath(name string) (string, bool) {
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		return "", false
	}
	p := filepath.Join(s.vaultsDir, name)
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return p, true
}

func (s *server) safePath(vaultP, notePath string) (string, bool) {
	if strings.Contains(notePath, "..") {
		return "", false
	}
	full := filepath.Join(vaultP, notePath)
	// ensure it's still under vaultP
	rel, err := filepath.Rel(vaultP, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return full, true
}

// preferred vault display order — vaults not listed here appear alphabetically after
var vaultOrder = []string{"pessoal", "work", "pcp", "sosracismo", "projects"}

func (s *server) handleVaults(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
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

func (s *server) handleNote(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetNote(w, r)
	case http.MethodPut:
		s.handlePutNote(w, r)
	case http.MethodPost:
		s.handleCreateNote(w, r)
	case http.MethodDelete:
		s.handleDeleteNote(w, r)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

func (s *server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if path == "" {
		errResponse(w, 400, "path required")
		return
	}
	// ensure .md extension
	if !strings.HasSuffix(path, ".md") {
		path = path + ".md"
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	// don't overwrite existing notes
	if _, err := os.Stat(full); err == nil {
		errResponse(w, 409, "note already exists")
		return
	}

	var body struct {
		Raw string `json:"raw"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := saveNote(vp, path, body.Raw); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	s.idx.updateNote(vault, path, body.Raw)

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"status": "created", "path": path})
}

func (s *server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if _, err := os.Stat(full); os.IsNotExist(err) {
		errResponse(w, 404, "note not found")
		return
	}

	// soft-delete: move to .trash/ inside vault
	trashDir := filepath.Join(vp, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		errResponse(w, 500, "cannot create trash: "+err.Error())
		return
	}

	// preserve sub-path inside trash to avoid name collisions
	trashPath := filepath.Join(trashDir, strings.ReplaceAll(path, "/", "__"))
	// if file already in trash with same name, add timestamp
	if _, err := os.Stat(trashPath); err == nil {
		ext := filepath.Ext(trashPath)
		base := strings.TrimSuffix(trashPath, ext)
		trashPath = fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
	}

	if err := os.Rename(full, trashPath); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	s.idx.removeNote(vault, path)

	jsonResponse(w, map[string]string{"status": "deleted", "movedTo": ".trash/" + filepath.Base(trashPath)})
}

func (s *server) handleFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateFolder(w, r)
	case http.MethodDelete:
		s.handleDeleteFolder(w, r)
	case http.MethodPatch:
		s.handleRenameFolder(w, r)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

func (s *server) handleRenameFolder(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if from == "" || to == "" {
		errResponse(w, 400, "from and to required")
		return
	}

	fullFrom, ok := s.safePath(vp, from)
	if !ok {
		errResponse(w, 400, "invalid from path")
		return
	}
	fullTo, ok := s.safePath(vp, to)
	if !ok {
		errResponse(w, 400, "invalid to path")
		return
	}

	info, err := os.Stat(fullFrom)
	if os.IsNotExist(err) {
		errResponse(w, 404, "folder not found")
		return
	}
	if !info.IsDir() {
		errResponse(w, 400, "path is not a folder")
		return
	}
	if _, err := os.Stat(fullTo); err == nil {
		errResponse(w, 409, "destination already exists")
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullTo), 0755); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	if err := os.Rename(fullFrom, fullTo); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// update index: rekey all notes under renamed folder
	s.idx.mu.Lock()
	updates := map[string]string{} // oldPath -> newPath
	for key, ref := range s.idx.allNotes {
		if ref.Vault == vault && strings.HasPrefix(ref.Path, from+"/") {
			newPath := to + ref.Path[len(from):]
			updates[key] = newPath
		}
	}
	for oldKey, newPath := range updates {
		ref := s.idx.allNotes[oldKey]
		delete(s.idx.allNotes, oldKey)
		ref.Path = newPath
		newKey := vault + ":" + newPath
		s.idx.allNotes[newKey] = ref
	}
	s.idx.mu.Unlock()

	jsonResponse(w, map[string]string{"status": "renamed", "newPath": to})
}

func (s *server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if path == "" {
		errResponse(w, 400, "path required")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if _, err := os.Stat(full); err == nil {
		errResponse(w, 409, "folder already exists")
		return
	}
	if err := os.MkdirAll(full, 0755); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"status": "created", "path": path})
}

func (s *server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if path == "" {
		errResponse(w, 400, "path required")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	info, err := os.Stat(full)
	if os.IsNotExist(err) {
		errResponse(w, 404, "folder not found")
		return
	}
	if !info.IsDir() {
		errResponse(w, 400, "path is not a folder")
		return
	}

	// soft-delete: move to .trash/ inside vault
	trashDir := filepath.Join(vp, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		errResponse(w, 500, "cannot create trash: "+err.Error())
		return
	}
	trashPath := filepath.Join(trashDir, strings.ReplaceAll(path, "/", "__"))
	if _, err := os.Stat(trashPath); err == nil {
		trashPath = fmt.Sprintf("%s_%d", trashPath, time.Now().Unix())
	}
	if err := os.Rename(full, trashPath); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// remove all notes in this folder from the index
	s.idx.mu.Lock()
	prefix := vault + ":" // walk allNotes to find matching paths
	for key := range s.idx.allNotes {
		ref := s.idx.allNotes[key]
		if ref.Vault == vault && (strings.HasPrefix(ref.Path, path+"/") || ref.Path == path) {
			delete(s.idx.allNotes, key)
		}
		_ = prefix
	}
	s.idx.mu.Unlock()

	jsonResponse(w, map[string]string{"status": "deleted", "movedTo": ".trash/" + filepath.Base(trashPath)})
}

func (s *server) handleMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, 405, "method not allowed")
		return
	}
	vault := r.URL.Query().Get("vault")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if from == "" || to == "" {
		errResponse(w, 400, "from and to required")
		return
	}
	// Only enforce .md extension for files (not directories)
	fullFromCheck, okCheck := s.safePath(vp, from)
	if okCheck {
		if info, err := os.Stat(fullFromCheck); err == nil && !info.IsDir() {
			if !strings.HasSuffix(to, ".md") {
				to = to + ".md"
			}
		}
	}

	fullFrom, ok := s.safePath(vp, from)
	if !ok {
		errResponse(w, 400, "invalid from path")
		return
	}
	fullTo, ok := s.safePath(vp, to)
	if !ok {
		errResponse(w, 400, "invalid to path")
		return
	}
	if _, err := os.Stat(fullFrom); os.IsNotExist(err) {
		errResponse(w, 404, "source not found")
		return
	}
	if _, err := os.Stat(fullTo); err == nil {
		errResponse(w, 409, "destination already exists")
		return
	}

	// ensure target dir exists
	if err := os.MkdirAll(filepath.Dir(fullTo), 0755); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	if err := os.Rename(fullFrom, fullTo); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// update index: remove old, add new
	data, _ := os.ReadFile(fullTo)
	s.idx.removeNote(vault, from)
	s.idx.updateNote(vault, to, string(data))

	jsonResponse(w, map[string]string{"status": "moved", "newPath": to})
}

func (s *server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}

	data, err := os.ReadFile(full)
	if err != nil {
		errResponse(w, 404, "note not found")
		return
	}

	info, _ := os.Stat(full)
	var mtime int64
	if info != nil {
		mtime = info.ModTime().Unix()
	}

	raw := string(data)
	fm, body := parseFrontmatter(raw)
	if fm == nil {
		fm = map[string]any{}
	}

	rendered := renderMarkdown(body)
	rendered = renderWikilinks(rendered, vault, s.idx)

	backlinks := s.idx.getBacklinks(vault, path)
	if backlinks == nil {
		backlinks = []BacklinkRef{}
	}

	jsonResponse(w, NoteResponse{
		Raw:         raw,
		HTML:        rendered,
		Frontmatter: fm,
		Backlinks:   backlinks,
		MTime:       mtime,
	})
}

func (s *server) handlePutNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	_, ok = s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		errResponse(w, 400, "cannot read body")
		return
	}
	content := buf.String()

	if err := saveNote(vp, path, content); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// update index
	s.idx.updateNote(vault, path, content)

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	vault := r.URL.Query().Get("vault")

	if q == "" {
		jsonResponse(w, []SearchResult{})
		return
	}

	var results []SearchResult
	if vault != "" {
		vp, ok := s.vaultPath(vault)
		if ok {
			results = searchVault(vp, vault, q)
		}
	} else {
		entries, _ := os.ReadDir(s.vaultsDir)
		for _, e := range entries {
			if e.IsDir() && !shouldSkip(e.Name()) {
				vp := filepath.Join(s.vaultsDir, e.Name())
				results = append(results, searchVault(vp, e.Name(), q)...)
				if len(results) >= 20 {
					break
				}
			}
		}
	}

	if results == nil {
		results = []SearchResult{}
	}
	jsonResponse(w, results)
}

func (s *server) handleResolve(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	vault := r.URL.Query().Get("vault")

	v, p, ok := s.idx.resolve(name, vault)
	if !ok {
		errResponse(w, 404, "not found")
		return
	}
	jsonResponse(w, ResolveResult{Vault: v, Path: p})
}

// ─── Stats handler ────────────────────────────────────────────────────────────

type VaultStat struct {
	Name      string `json:"name"`
	NoteCount int    `json:"noteCount"`
}

type StatsResponse struct {
	TotalNotes int         `json:"totalNotes"`
	Vaults     []VaultStat `json:"vaults"`
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// count notes per vault from the filesystem (not index, to avoid double-counting compound keys)
	var stats []VaultStat
	total := 0
	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		count := 0
		vp := filepath.Join(s.vaultsDir, e.Name())
		_ = filepath.Walk(vp, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(p, ".md") && !shouldSkip(info.Name()) {
				count++
			}
			return nil
		})
		// sort vaults by vaultOrder then alphabetically handled at display layer
		stats = append(stats, VaultStat{Name: e.Name(), NoteCount: count})
		total += count
	}

	// apply preferred vault order
	sort.SliceStable(stats, func(i, j int) bool {
		pi, pj := len(vaultOrder), len(vaultOrder)
		for k, v := range vaultOrder {
			if stats[i].Name == v {
				pi = k
			}
			if stats[j].Name == v {
				pj = k
			}
		}
		if pi != pj {
			return pi < pj
		}
		return stats[i].Name < stats[j].Name
	})

	jsonResponse(w, StatsResponse{TotalNotes: total, Vaults: stats})
}

// ─── Sync status handler ───────────────────────────────────────────────────────

type SyncStatus struct {
	Available bool   `json:"available"`
	State     string `json:"state"` // "synced", "syncing", "error", "unknown"
	Message   string `json:"message,omitempty"`
}

var syncHTTPClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

func (s *server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("SYNCTHING_API_KEY")
	apiURL := os.Getenv("SYNCTHING_API_URL") // e.g. https://172.10.0.5:8384
	if apiKey == "" || apiURL == "" {
		jsonResponse(w, SyncStatus{Available: false, State: "unknown"})
		return
	}

	req, err := http.NewRequest("GET", apiURL+"/rest/db/completion", nil)
	if err != nil {
		jsonResponse(w, SyncStatus{Available: false, State: "error", Message: err.Error()})
		return
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := syncHTTPClient.Do(req)
	if err != nil {
		jsonResponse(w, SyncStatus{Available: false, State: "error", Message: "unreachable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		jsonResponse(w, SyncStatus{Available: true, State: "error", Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}

	var completion struct {
		Completion float64 `json:"completion"`
		NeedBytes  int64   `json:"needBytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		jsonResponse(w, SyncStatus{Available: true, State: "error", Message: "parse error"})
		return
	}

	if completion.NeedBytes == 0 {
		jsonResponse(w, SyncStatus{Available: true, State: "synced", Message: "Up to date"})
	} else {
		jsonResponse(w, SyncStatus{Available: true, State: "syncing",
			Message: fmt.Sprintf("%.0f%%", completion.Completion)})
	}
}

// ─── Static files ─────────────────────────────────────────────────────────────

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(staticFiles, "static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	log.Printf("vaultreader starting — vaults=%s port=%s", *vaultsDir, *port)

	idx := newIndex()

	// Check if vaults dir exists, warn but don't fail (might be mounted later)
	if _, err := os.Stat(*vaultsDir); err != nil {
		log.Printf("WARNING: vaults dir %s not found: %v", *vaultsDir, err)
		// Create it to avoid startup failure
		_ = os.MkdirAll(*vaultsDir, 0755)
	}

	// Build index
	t0 := time.Now()
	idx.buildAll(*vaultsDir)
	log.Printf("index built in %v", time.Since(t0))

	// Ensure appdata/icons exists
	_ = os.MkdirAll(filepath.Join(*appdataDir, "icons"), 0755)

	srv := &server{
		vaultsDir:  *vaultsDir,
		appdataDir: *appdataDir,
		idx:        idx,
		shares:     newShareStore(*appdataDir),
	}
	srv.loadConfig()

	// Static files sub-FS
	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("static sub: %v", err)
	}
	staticHandler := http.FileServer(http.FS(subFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			srv.handleIndex(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/vaults", srv.handleVaults)
	mux.HandleFunc("/api/tree", srv.handleTree)
	mux.HandleFunc("/api/note", srv.handleNote)
	mux.HandleFunc("/api/move", srv.handleMove)
	mux.HandleFunc("/api/folder", srv.handleFolder)
	mux.HandleFunc("/api/search", srv.handleSearch)
	mux.HandleFunc("/api/resolve", srv.handleResolve)
	mux.HandleFunc("/api/stats", srv.handleStats)
	mux.HandleFunc("/api/sync-status", srv.handleSyncStatus)
	mux.HandleFunc("/api/vault-icon", srv.handleVaultIcon)
	mux.HandleFunc("/api/admin/config", srv.handleAdminConfig)
	mux.HandleFunc("/api/shares", srv.handleShareList)
	mux.HandleFunc("/api/shares/create", srv.handleShareCreate)
	mux.HandleFunc("/api/shares/revoke", srv.handleShareRevoke)
	mux.HandleFunc("/share/", srv.handleShareView)
	mux.HandleFunc("/api/admin/restart", srv.handleAdminRestart)

	addr := ":" + *port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, gzipMiddleware(mux)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
