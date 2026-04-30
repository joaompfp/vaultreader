package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

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

// revokeAll clears every entry. Returns how many were removed.
func (ss *ShareStore) revokeAll() int {
	ss.mu.Lock(); n := len(ss.entries); ss.entries = map[string]*ShareEntry{}; ss.mu.Unlock()
	_ = ss.save()
	return n
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

// handleShareRevokeAll deletes every active share in one call. Avoids the
// rate-limit cliff the loop version hit (and the wrong-method 405 the
// bulk frontend was sending pre-2026-04-29).
func (s *server) handleShareRevokeAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete { errResponse(w, 405, "method not allowed"); return }
	count := s.shares.revokeAll()
	jsonResponse(w, map[string]int{"revoked": count})
}

func (s *server) handleShareView(w http.ResponseWriter, r *http.Request) {
	// Strip leading /share/ then split off any sub-path (e.g. /share/TOKEN/file).
	rest := strings.TrimPrefix(r.URL.Path, "/share/")
	if rest == "" { http.NotFound(w, r); return }
	parts := strings.SplitN(rest, "/", 2)
	token := parts[0]
	if token == "" { http.NotFound(w, r); return }
	e, ok := s.shares.get(token)
	if !ok { http.Error(w, "Share link not found or expired.", http.StatusNotFound); return }

	// Sub-path routing under /share/<token>/...:
	//   /share/<token>/file?path=…   → in-vault image embed
	//   /share/<token>/asset?name=…  → bundled JS/CSS for share-page
	//                                  rendering (mermaid, katex)
	// Both stay under the /share/* prefix so reverse-proxy bypass rules
	// cover them. The /api/* namespace remains auth-gated.
	if len(parts) == 2 {
		sub := parts[1]
		if sub == "file" || strings.HasPrefix(sub, "file") {
			s.handleShareFile(w, r, e)
			return
		}
		if sub == "asset" || strings.HasPrefix(sub, "asset") {
			s.handleShareAsset(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}

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

	// Non-markdown files (images, PDFs, anything-but-.md): serve directly
	// via the same byte stream that `/share/<token>/file` would emit. The
	// markdown rendering path below would run goldmark over binary bytes
	// and emit garbage. Browser then renders the file natively (image,
	// PDF viewer, download prompt) just like `/api/file`.
	pathExt := strings.ToLower(filepath.Ext(e.Path))
	if pathExt != ".md" && pathExt != "" {
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		}
		if ct := mime.TypeByExtension(pathExt); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		http.ServeFile(w, r, full)
		return
	}

	data, err := os.ReadFile(full)
	if err != nil { http.Error(w, "Note not found.", http.StatusNotFound); return }

	raw := string(data)
	title := extractTitle(raw, e.Path)
	// Strip YAML frontmatter, expand image embeds, render markdown, and rewrite
	// the embed image URLs from /api/file?... to /share/<token>/file?path=...
	// so they're served under the share's auth context (covered by the
	// reverse-proxy /share/* bypass) instead of the gated /api namespace.
	_, body := parseFrontmatter(raw)
	body = expandEmbeds(body, e.Vault, e.Path, s.idx, s.vaultsDir)
	var buf bytes.Buffer
	_ = md.Convert([]byte(protectWikilinkPipes(body)), &buf)
	// Post-process: callouts → styled divs, wikilinks → plain spans (no
	// off-share nav), then rewrite image src for the share-file route.
	renderedHTML := restoreWikilinkPipes(buf.String())
	renderedHTML = renderCallouts(renderedHTML)
	renderedHTML = renderWikilinksPlain(renderedHTML)
	renderedHTML = rewriteShareImageURLs(renderedHTML, e.Path)

	expiresStr := "Never"
	if e.ExpiresAt > 0 { expiresStr = time.Unix(e.ExpiresAt, 0).Format("2 Jan 2006 15:04") }
	modeStr, modeCls := "Read-only", " ro"
	if e.Writable { modeStr, modeCls = "Editable", "" }

	// Conditionally include mermaid/katex assets only when the rendered
	// HTML actually needs them. Both are big — mermaid ~3MB, katex+fonts
	// ~1.5MB — so don't ship them with every share page.
	wantsMermaid := strings.Contains(renderedHTML, "language-mermaid")
	wantsMath := strings.Contains(renderedHTML, "$$") ||
		strings.Contains(renderedHTML, "\\(") ||
		strings.Contains(renderedHTML, "\\[")

	// All asset/file URLs below are emitted as path-relative ("asset?…",
	// "file?…") so they resolve against the page's <base href>. That keeps
	// the share page working under both `/share/<token>` and the proxy
	// alias `/notas/<token>` without baking the prefix into the HTML.
	headExtra := ""
	bodyExtra := ""
	if wantsMath {
		headExtra += `<link rel="stylesheet" href="asset?name=katex.min.css">`
		bodyExtra += `<script src="asset?name=katex.min.js"></script>` +
			`<script src="asset?name=katex-auto-render.min.js"></script>` +
			`<script>document.addEventListener('DOMContentLoaded',function(){renderMathInElement(document.body,{delimiters:[{left:'$$',right:'$$',display:true},{left:'\\(',right:'\\)',display:false},{left:'\\[',right:'\\]',display:true}],throwOnError:false});});</script>`
	}
	if wantsMermaid {
		bodyExtra += `<script src="asset?name=mermaid.min.js"></script>` +
			`<script>document.addEventListener('DOMContentLoaded',async function(){if(typeof mermaid==='undefined')return;await mermaid.initialize({startOnLoad:false});const els=Array.from(document.querySelectorAll('pre code.language-mermaid'));for(const el of els){const code=el.textContent||'';const pre=el.closest('pre');if(!pre)continue;const div=document.createElement('div');div.className='mermaid';pre.replaceWith(div);try{const{svg}=await mermaid.render('shm-'+Date.now()+Math.random().toString(36).slice(2),code);div.innerHTML=svg;}catch(e){div.innerHTML='<pre style=\"color:#b91c1c\">'+e.message+'</pre>';}}});</script>`
	}

	// <base href="<token>/"> makes relative URLs in the page resolve to
	// `…/<token>/file?…` and `…/<token>/asset?…` regardless of whether
	// the page was reached via `/share/<token>` or `/notas/<token>`.
	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<base href="%s/">
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
.mermaid svg{display:block;margin:1em auto;max-width:100%%}
.wikilink-plain{color:var(--ac);background:rgba(185,28,28,.06);padding:1px 4px;border-radius:3px;font-size:.95em}
.callout{margin:1em 0;padding:9px 14px 11px;border:1px solid var(--bd);border-radius:8px;background:rgba(185,28,28,.03)}
.callout-title{font-weight:600;font-size:.92em;color:var(--ac);margin-bottom:.35em;letter-spacing:.01em}
.callout-icon{display:none}
.callout-body>:first-child{margin-top:0}.callout-body>:last-child{margin-bottom:0}
.foot{text-align:center;padding:20px;font-size:12px;color:var(--t3);border-top:1px solid var(--bd)}.foot a{color:var(--t3);text-decoration:none}
</style>%s</head><body>
<div class="bar"><strong>%s</strong><span class="badge%s">%s</span><span>Expires: %s</span>
<span style="flex:1"></span><a href="https://notes.joao.date" style="color:var(--t3);font-size:11px">VaultReader</a></div>
<div class="content">%s</div>
<div class="foot">Shared via <a href="https://notes.joao.date">VaultReader</a></div>
%s</body></html>`, token, title, headExtra, title, modeCls, modeStr, expiresStr, renderedHTML, bodyExtra)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}


// handleShareAsset serves a bundled static asset (mermaid/katex JS or CSS,
// fonts) under the share auth context. Strict allowlist prevents tokens
// from being abused to fetch the SPA index.html or other private bits.
var shareAssetAllowlist = map[string]string{
	"mermaid.min.js":          "static/mermaid.min.js",
	"katex.min.js":            "static/katex.min.js",
	"katex.min.css":           "static/katex.min.css",
	"katex-auto-render.min.js": "static/katex-auto-render.min.js",
}

func (s *server) handleShareAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		errResponse(w, 405, "method not allowed")
		return
	}
	name := r.URL.Query().Get("name")
	// Need the token to rewrite KaTeX CSS font URLs back into asset URLs.
	rest := strings.TrimPrefix(r.URL.Path, "/share/")
	token := strings.SplitN(rest, "/", 2)[0]

	embedPath, ok := shareAssetAllowlist[name]
	if !ok {
		// Allow KaTeX font files at the path "fonts/<file>"
		if strings.HasPrefix(name, "fonts/") &&
			(strings.HasSuffix(name, ".woff2") || strings.HasSuffix(name, ".woff") ||
				strings.HasSuffix(name, ".ttf")) {
			embedPath = "static/" + name
		} else {
			http.NotFound(w, r)
			return
		}
	}
	data, err := staticFiles.ReadFile(embedPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// KaTeX CSS contains relative `url(fonts/X)` references; rewrite to
	// absolute /share/TOKEN/asset?name=fonts/X URLs so the browser fetches
	// them under the same share-auth bypass.
	if name == "katex.min.css" {
		data = []byte(strings.ReplaceAll(string(data),
			"url(fonts/",
			"url(/share/"+token+"/asset?name=fonts/"))
	}
	switch filepath.Ext(name) {
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
	case ".woff":
		w.Header().Set("Content-Type", "font/woff")
	case ".ttf":
		w.Header().Set("Content-Type", "font/ttf")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

// handleShareFile serves a file embedded inside a shared note, using the
// share's own auth context (i.e. anyone with the token). Scoped strictly
// to the share's vault; honors safePath to block traversal.
func (s *server) handleShareFile(w http.ResponseWriter, r *http.Request, e *ShareEntry) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		errResponse(w, 405, "method not allowed")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(e.Vault)
	if !ok { http.NotFound(w, r); return }
	full, ok := s.safePath(vp, path)
	if !ok { http.NotFound(w, r); return }
	info, err := os.Stat(full)
	if err != nil || info.IsDir() { http.NotFound(w, r); return }
	// Defensive: only image / pdf / common-attachment types. A leaked share
	// token shouldn't yield arbitrary file reads from the vault — even
	// though safePath already keeps us inside the vault.
	ext := strings.ToLower(filepath.Ext(full))
	allowed := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".webp": true, ".svg": true, ".bmp": true, ".avif": true,
		".pdf": true, ".mp3": true, ".wav": true, ".m4a": true,
		".mp4": true, ".webm": true, ".mov": true,
	}
	if !allowed[ext] {
		http.Error(w, "file type not served via share", http.StatusForbidden)
		return
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, full)
}

// rewriteShareImageURLs replaces inline `<img src="/api/file?...">` URLs
// emitted by goldmark with relative `file?path=...` URLs that resolve
// against the page's <base href> (set to "<token>/"). Two image-source
// shapes are rewritten:
//
//  1. `/api/file?vault=X&path=Y` — emitted by expandEmbeds for `![[…]]`
//     wikilink embeds. Goldmark HTML-escapes the `&`, so the attribute
//     looks like `src="/api/file?vault=X&amp;path=Y"`.
//
//  2. Plain markdown image paths `![alt](path/to/img.jpg)` — goldmark
//     emits these as `src="path/to/img.jpg"`, which the share page
//     would otherwise resolve against `/notas/` (the proxy prefix) and
//     404. Rewrite to `file?path=<vault-relative-path>` so the asset
//     comes out of the share's vault.
//
// Using path-relative URLs (no leading slash, no /share/<token>/ prefix)
// is what makes the share page work under both `/share/<token>` and the
// public `/notas/<token>` Traefik alias — the <base href> tag picks up
// whichever prefix the user came in on.
func rewriteShareImageURLs(html, notePath string) string {
	noteDir := filepath.Dir(notePath)

	// Pass 1: rewrite `/api/file?vault=X&path=Y` (with optional `&amp;`).
	reAPI := regexp.MustCompile(`src="(/api/file\?[^"]+)"`)
	html = reAPI.ReplaceAllStringFunc(html, func(match string) string {
		quoted := strings.TrimPrefix(match, `src="`)
		quoted = strings.TrimSuffix(quoted, `"`)
		unescaped := strings.ReplaceAll(quoted, "&amp;", "&")
		u, err := url.Parse(unescaped)
		if err != nil {
			return match
		}
		p := u.Query().Get("path")
		if p == "" {
			return match
		}
		return fmt.Sprintf(`src="file?path=%s"`, urlEscape(p))
	})

	// Pass 2: rewrite plain markdown image refs whose src is neither
	// absolute (http://, https://, /…) nor data: nor already pointing at
	// our share endpoint. These are vault-relative or note-relative
	// filesystem paths from `![alt](path)` syntax.
	reImg := regexp.MustCompile(`src="([^"]+)"`)
	html = reImg.ReplaceAllStringFunc(html, func(match string) string {
		quoted := strings.TrimPrefix(match, `src="`)
		quoted = strings.TrimSuffix(quoted, `"`)
		// Skip already-rewritten, absolute, scheme-bearing, data:, or
		// fragment-only refs.
		if strings.HasPrefix(quoted, "file?") ||
			strings.HasPrefix(quoted, "/") ||
			strings.HasPrefix(quoted, "#") ||
			strings.Contains(quoted, "://") ||
			strings.HasPrefix(quoted, "data:") ||
			strings.HasPrefix(quoted, "mailto:") {
			return match
		}
		decoded, err := url.QueryUnescape(strings.ReplaceAll(quoted, "&amp;", "&"))
		if err != nil {
			decoded = quoted
		}
		// Resolve the markdown-image path against the note's directory
		// (matches Obsidian's "shortest path when possible" — explicit
		// relative paths win). filepath.Join + Clean handles `../` walks.
		joined := filepath.Clean(filepath.Join(noteDir, decoded))
		if strings.HasPrefix(joined, "..") {
			return match
		}
		return fmt.Sprintf(`src="file?path=%s"`, urlEscape(joined))
	})

	return html
}
