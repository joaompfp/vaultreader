package main

import (
	"bytes"
	"context"
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/webdav"
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
	Size        int64          `json:"size"`
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
	embedRe    = regexp.MustCompile(`!\[\[([^\]|]+?)(?:\|([^\]]+?))?\]\]`)
	headingRe  = regexp.MustCompile(`(?m)^#+\s+(.+)$`)
	imageExtRe = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|svg|webp|bmp|avif)$`)
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

// expandEmbeds rewrites Obsidian embed syntax `![[target]]` into standard
// markdown so goldmark renders it natively. Image targets become
// `![alt](/api/file?vault=X&path=Y)` (which goldmark turns into <img>).
// Non-image targets degrade to a plain wikilink, leaving the existing
// wikilinkRe pass to handle them.
//
// Targets may be:
//   - relative to the current note's directory (e.g. `../../_source/foo.png`)
//   - absolute within a vault (no leading `/` in Obsidian — bare names
//     are matched against the note index by basename)
func expandEmbeds(raw string, currentVault, currentNotePath string, idx *NoteIndex, vaultsDir string) string {
	noteDir := filepath.Dir(currentNotePath)
	return embedRe.ReplaceAllStringFunc(raw, func(match string) string {
		sub := embedRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		target := sub[1]
		alias := sub[2]
		if alias == "" {
			alias = filepath.Base(target)
		}

		// Strip any #heading or |alias suffix already handled by the regex group.
		// (alias above is sub[2]; #heading is part of sub[1] here — we ignore it
		//  for embeds since we only resolve to a file).
		cleanTarget := target
		if hash := strings.Index(cleanTarget, "#"); hash >= 0 {
			cleanTarget = cleanTarget[:hash]
		}

		isImage := imageExtRe.MatchString(cleanTarget)

		// Resolve target → (vault, path).
		v, p, ok := resolveEmbed(cleanTarget, currentVault, noteDir, idx, vaultsDir)
		if !ok {
			// Leave it as a wikilink for renderWikilinks to mark as missing.
			if isImage {
				return fmt.Sprintf(`<span class="embed-missing" title="%s">[image missing: %s]</span>`,
					htmlEscape(cleanTarget), htmlEscape(filepath.Base(cleanTarget)))
			}
			return "[[" + sub[1] + "]]" // strip the !, let wikilink pass handle it
		}

		if isImage {
			url := fmt.Sprintf("/api/file?vault=%s&path=%s",
				urlEscape(v), urlEscape(p))
			return fmt.Sprintf("![%s](%s)", alias, url)
		}
		// Non-image embed (md transclusion, pdf, audio): render as link for now.
		return fmt.Sprintf("[%s](#vault=%s&path=%s)", alias, urlEscape(v), urlEscape(p))
	})
}

// resolveEmbed locates the embed target on disk. Order:
//  1. If target contains a path separator, treat as relative to the note's
//     directory (Obsidian's "shortest path when possible" — explicit paths win).
//  2. Otherwise, look up by basename in the note index (current vault first).
//  3. As a last resort, try the target verbatim against the current vault root.
func resolveEmbed(target, currentVault, noteDir string, idx *NoteIndex, vaultsDir string) (string, string, bool) {
	if strings.ContainsAny(target, "/\\") {
		joined := filepath.Clean(filepath.Join(noteDir, target))
		// Reject paths that escape the vault root.
		if strings.HasPrefix(joined, "..") {
			return "", "", false
		}
		full := filepath.Join(vaultsDir, currentVault, joined)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			return currentVault, joined, true
		}
		return "", "", false
	}
	// Bare name → ask the index.
	if v, p, ok := idx.resolve(target, currentVault); ok {
		return v, p, true
	}
	// Index only tracks .md notes; for image basenames it'll miss. Try walking
	// the current vault for a matching file (cheap once, results not cached).
	root := filepath.Join(vaultsDir, currentVault)
	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(p) == target {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if found != "" {
		rel, err := filepath.Rel(root, found)
		if err == nil {
			return currentVault, rel, true
		}
	}
	return "", "", false
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func urlEscape(s string) string { return url.QueryEscape(s) }

func renderWikilinks(htmlStr string, currentVault, currentNotePath string, idx *NoteIndex, vaultsDir string) string {
	noteDir := filepath.Dir(currentNotePath)
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

		// Strip #heading and ^block-id suffixes from the lookup target
		// (we keep them in the alias for display purposes via the original).
		lookup := name
		if hash := strings.IndexAny(lookup, "#^"); hash >= 0 {
			lookup = lookup[:hash]
		}

		v, p, ok := resolveWikilinkTarget(lookup, currentVault, noteDir, idx, vaultsDir)
		if !ok {
			return fmt.Sprintf(`<a href="#" class="wikilink wikilink-missing" data-name="%s">%s</a>`,
				htmlEscape(name), htmlEscape(alias))
		}
		// Real href so right-click "open in new tab", bookmarking, and
		// browser back/forward all work natively. The data-* attributes
		// stay so the SPA click handler can update state without re-parsing.
		return fmt.Sprintf(`<a href="%s" class="wikilink" data-vault="%s" data-path="%s">%s</a>`,
			noteHref(v, p), htmlEscape(v), htmlEscape(p), htmlEscape(alias))
	})
}

// noteHref builds a clean URL for a note: /n/<vault>/<encoded path segments>.
// The path keeps its `.md` extension to stay unambiguous (matches filebrowser
// pattern). Each segment is URL-encoded individually so spaces, parens, etc.
// survive without breaking the route.
func noteHref(vault, path string) string {
	var segs []string
	segs = append(segs, url.PathEscape(vault))
	for _, p := range strings.Split(path, "/") {
		if p == "" {
			continue
		}
		segs = append(segs, url.PathEscape(p))
	}
	return "/n/" + strings.Join(segs, "/")
}

// resolveWikilinkTarget mirrors resolveEmbed's resolution order but is
// tailored to `.md` notes (auto-appending the extension when absent):
//  1. Path-shaped target → relative to note dir, then to vault root.
//  2. Bare name → existing index lookup by basename.
func resolveWikilinkTarget(target, currentVault, noteDir string, idx *NoteIndex, vaultsDir string) (string, string, bool) {
	withMd := func(p string) string {
		if strings.EqualFold(filepath.Ext(p), ".md") {
			return p
		}
		return p + ".md"
	}

	if strings.ContainsAny(target, "/\\") {
		// Try relative to current note's directory.
		candidate := filepath.Clean(filepath.Join(noteDir, withMd(target)))
		if !strings.HasPrefix(candidate, "..") {
			full := filepath.Join(vaultsDir, currentVault, candidate)
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				return currentVault, candidate, true
			}
		}
		// Try relative to vault root.
		candidate2 := filepath.Clean(withMd(target))
		if !strings.HasPrefix(candidate2, "..") && !strings.HasPrefix(candidate2, "/") {
			full := filepath.Join(vaultsDir, currentVault, candidate2)
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				return currentVault, candidate2, true
			}
		}
		// Path-shaped but didn't resolve directly — fall through to
		// basename lookup (handles `[[folder/foo]]` where `foo` is unique
		// in the index but lives somewhere else than `folder/foo`).
		base := filepath.Base(target)
		if v, p, ok := idx.resolve(base, currentVault); ok {
			return v, p, true
		}
		return "", "", false
	}
	// Bare name → ask the index.
	return idx.resolve(target, currentVault)
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
	type scored struct {
		r     SearchResult
		score float64
	}
	var hits []scored

	now := time.Now().Unix()

	_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || shouldSkip(info.Name()) {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		rel, _ := filepath.Rel(vaultPath, path)
		baseLower := strings.ToLower(info.Name())
		nameMatch := strings.Contains(baseLower, query)

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		contentLower := strings.ToLower(content)
		contentMatch := strings.Contains(contentLower, query)

		_, body := parseFrontmatter(content)
		title := extractTitle(body, rel)
		titleMatch := strings.Contains(strings.ToLower(title), query)

		if !nameMatch && !contentMatch && !titleMatch {
			return nil
		}

		// Scoring:
		//   exact title (whole-string)         → +20
		//   substring in title (first H1)      → +10
		//   substring in filename basename     →  +5
		//   substring in body                  →  +1 (× count up to 5)
		//   recency boost                      →  +0..3 (modified within 30d)
		score := 0.0
		titleLower := strings.ToLower(title)
		if titleLower == query {
			score += 20
		} else if titleMatch {
			score += 10
		}
		if nameMatch {
			score += 5
		}
		if contentMatch {
			// Count up to 5 occurrences in body to reward repeated mentions.
			n := strings.Count(contentLower, query)
			if n > 5 {
				n = 5
			}
			score += float64(n)
		}
		// Recency: 0 days old → +3, 30+ days → 0.
		ageDays := float64(now-info.ModTime().Unix()) / 86400
		if ageDays < 0 {
			ageDays = 0
		}
		if ageDays < 30 {
			score += 3.0 * (1.0 - ageDays/30.0)
		}

		// Build excerpt around first content match (or skip if title-only).
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

		hits = append(hits, scored{
			r: SearchResult{
				Vault:   vaultName,
				Path:    rel,
				Title:   title,
				Excerpt: excerpt,
			},
			score: score,
		})
		return nil
	})

	// Sort: score desc; ties broken by mtime via score (recency already baked in)
	// then by path asc for stability.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].r.Path < hits[j].r.Path
	})

	// Cap at 20 top hits per vault.
	if len(hits) > 20 {
		hits = hits[:20]
	}
	out := make([]SearchResult, len(hits))
	for i, h := range hits {
		out[i] = h.r
	}
	return out
}

// ─── Save ─────────────────────────────────────────────────────────────────────

func saveNote(vaultPath, notePath, content string) error {
	full := filepath.Join(vaultPath, notePath)
	// ensure directory exists
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	// Normalize: strip trailing whitespace per line, ensure exactly one
	// trailing newline. Reduces noise in git diffs when the same file is
	// edited from different tools (Obsidian, vim, VaultReader).
	content = normalizeMarkdown(content)
	tmp := full + ".tmp-vaultreader"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

// normalizeMarkdown applies whitespace normalization that is uncontroversial
// across markdown editors (Obsidian, vim, VS Code with default settings):
//   - strip trailing spaces/tabs from every line
//   - end the file with exactly one newline (no trailing blanks; no missing)
// Does NOT touch line endings beyond what os.WriteFile does, and does NOT
// collapse internal blank lines (markdown's blank-line semantics matter).
func normalizeMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		// rstrip space + tab; preserve other whitespace (none should appear
		// at end of a markdown line anyway).
		lines[i] = strings.TrimRight(ln, " \t")
	}
	out := strings.Join(lines, "\n")
	// Trim ALL trailing newlines, then add exactly one back.
	out = strings.TrimRight(out, "\n")
	if out != "" {
		out += "\n"
	}
	return out
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

	// Sub-path routing: /share/<token>/file?path=… serves an in-vault
	// resource (image embed) using the share's own auth context. Stays
	// under the /share/* prefix so reverse-proxy bypass rules cover it.
	if len(parts) == 2 {
		sub := parts[1]
		if sub == "file" || strings.HasPrefix(sub, "file") {
			s.handleShareFile(w, r, e)
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
	var buf bytes.Buffer; _ = md.Convert([]byte(body), &buf)
	rendered := rewriteShareImageURLs(buf.String(), token)
	buf.Reset()
	buf.WriteString(rendered)

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
// emitted by goldmark with `/share/<token>/file?path=...` so embeds load
// under the share's auth context instead of the gated /api namespace.
//
// Goldmark HTML-escapes the `&` between query args to `&amp;`, so the
// attribute looks like: src="/api/file?vault=X&amp;path=Y". The regex
// matches that variant.
func rewriteShareImageURLs(html, token string) string {
	re := regexp.MustCompile(`src="(/api/file\?[^"]+)"`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		quoted := strings.TrimPrefix(match, `src="`)
		quoted = strings.TrimSuffix(quoted, `"`)
		// Decode HTML entities (just `&amp;` → `&` is enough here).
		unescaped := strings.ReplaceAll(quoted, "&amp;", "&")
		u, err := url.Parse(unescaped)
		if err != nil {
			return match
		}
		p := u.Query().Get("path")
		if p == "" {
			return match
		}
		return fmt.Sprintf(`src="/share/%s/file?path=%s"`, token, urlEscape(p))
	})
}

// handleFile serves an arbitrary file from inside a vault. Used by image
// embeds (`![[...]]` rewritten to `/api/file?vault=X&path=Y`) but also
// available for downloading any tracked attachment.
func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		http.Error(w, "missing vault or path", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.NotFound(w, r)
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	if ct := mime.TypeByExtension(ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, full)
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
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", false
	}
	cleanName := filepath.Clean(name)
	if strings.Contains(cleanName, "..") || strings.HasPrefix(cleanName, ".") {
		return "", false
	}
	p := filepath.Join(s.vaultsDir, cleanName)
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return p, true
}

func (s *server) safePath(vaultP, notePath string) (string, bool) {
	// Reject empty paths and absolute paths (leading separator or drive letter).
	if notePath == "" {
		return "", false
	}
	if strings.HasPrefix(notePath, "/") || strings.HasPrefix(notePath, "\\") {
		return "", false
	}
	// Canonicalise before any resolution to eliminate .., ., duplicate slashes.
	full := filepath.Clean(filepath.Join(vaultP, notePath))
	vaultBase := filepath.Clean(vaultP)
	// On Windows filepath.Clean("C:\foo") returns "C:\foo" while filepath.Clean("foo") stays relative.
	// Ensure the resolved path is still inside the vault directory.
	rel, err := filepath.Rel(vaultBase, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	if full != vaultBase && !strings.HasPrefix(full, vaultBase+string(filepath.Separator)) {
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
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
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
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
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
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
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
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
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
	if !s.isWritable(vault, from) {
		errResponse(w, 403, "source path is not writable")
		return
	}
	fullTo, ok := s.safePath(vp, to)
	if !ok {
		errResponse(w, 400, "invalid to path")
		return
	}
	if !s.isWritable(vault, to) {
		errResponse(w, 403, "destination path is not writable")
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
	var mtime, size int64
	if info != nil {
		mtime = info.ModTime().Unix()
		size = info.Size()
	}

	raw := string(data)
	fm, body := parseFrontmatter(raw)
	if fm == nil {
		fm = map[string]any{}
	}

	body = expandEmbeds(body, vault, path, s.idx, s.vaultsDir)
	rendered := renderMarkdown(body)
	rendered = renderWikilinks(rendered, vault, path, s.idx, s.vaultsDir)

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
		Size:        size,
	})
}

func (s *server) handlePutNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	ifMTime := r.URL.Query().Get("ifMTime") // optional: client's last-known mtime

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
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
		return
	}

	// Conflict detection: if client supplied ifMTime, compare against on-disk.
	// 409 with the current file's content + mtime so the client can resolve.
	if ifMTime != "" {
		want, err := strconv.ParseInt(ifMTime, 10, 64)
		if err == nil && want > 0 {
			if info, errStat := os.Stat(full); errStat == nil {
				cur := info.ModTime().Unix()
				// Allow a 1-second slop for filesystems with second-precision mtime.
				if cur > want+1 {
					data, _ := os.ReadFile(full)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]any{
						"error":      "file changed on disk",
						"diskMtime":  cur,
						"diskRaw":    string(data),
					})
					return
				}
			}
		}
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

	// Return the new mtime so client can stay in sync without an extra GET.
	var newMTime int64
	if info, err := os.Stat(full); err == nil {
		newMTime = info.ModTime().Unix()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]int64{"mtime": newMTime})
}

// handleUpload accepts a multipart image upload tied to a note and writes
// the file under <note-dir>/attachments/<note-base>-<unix>.<ext>. Body cap
// 10MB. Same isWritable + safePath guards as handlePutNote.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, 405, "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB cap
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		errResponse(w, 400, "cannot parse form: "+err.Error())
		return
	}
	vault := r.FormValue("vault")
	notePath := r.FormValue("notePath")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if _, ok := s.safePath(vp, notePath); !ok {
		errResponse(w, 400, "invalid notePath")
		return
	}
	if !s.isWritable(vault, notePath) {
		errResponse(w, 403, "path is not writable")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		errResponse(w, 400, "missing file")
		return
	}
	defer file.Close()

	ct := hdr.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		errResponse(w, 400, "file must be an image")
		return
	}
	ext := ""
	switch ct {
	case "image/png":
		ext = "png"
	case "image/jpeg", "image/jpg":
		ext = "jpg"
	case "image/gif":
		ext = "gif"
	case "image/webp":
		ext = "webp"
	case "image/svg+xml":
		ext = "svg"
	default:
		errResponse(w, 400, "unsupported image type: "+ct)
		return
	}

	// Compute target dir: <note-dir>/attachments
	noteDir := filepath.Dir(notePath)
	if noteDir == "." {
		noteDir = ""
	}
	relAttachDir := filepath.Join(noteDir, "attachments")
	fullAttachDir, ok := s.safePath(vp, relAttachDir)
	if !ok {
		errResponse(w, 400, "invalid attachment dir")
		return
	}
	if err := os.MkdirAll(fullAttachDir, 0755); err != nil {
		errResponse(w, 500, "cannot create attachments dir: "+err.Error())
		return
	}

	noteBase := strings.TrimSuffix(filepath.Base(notePath), ".md")
	// Sanitize: strip anything that isn't [a-zA-Z0-9_-]
	noteBase = sanitizeFilename(noteBase)
	if noteBase == "" {
		noteBase = "image"
	}
	filename := fmt.Sprintf("%s-%d.%s", noteBase, time.Now().Unix(), ext)
	relFilePath := filepath.Join(relAttachDir, filename)
	fullFilePath, ok := s.safePath(vp, relFilePath)
	if !ok {
		errResponse(w, 400, "invalid file path")
		return
	}

	out, err := os.Create(fullFilePath)
	if err != nil {
		errResponse(w, 500, "cannot create file: "+err.Error())
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		errResponse(w, 500, "cannot write file: "+err.Error())
		return
	}

	// Return path *relative to the note's directory* so the client can embed
	// it as ![[attachments/foo.png]] regardless of where the note lives.
	jsonResponse(w, map[string]string{
		"path": filepath.Join("attachments", filename),
	})
}

// sanitizeFilename keeps [a-zA-Z0-9_-], replaces anything else with '-'.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	return b.String()
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

// handleBacklinks returns just the backlinks for a note, cheaply (no
// disk read of the note itself). Used by rename/move flows to warn
// about wikilinks that would break before the user confirms.
func (s *server) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		errResponse(w, 400, "vault and path required")
		return
	}
	backlinks := s.idx.getBacklinks(vault, path)
	if backlinks == nil {
		backlinks = []BacklinkRef{}
	}
	jsonResponse(w, map[string]any{"backlinks": backlinks})
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


// handleTrashList returns items in .trash/ for a vault.
func (s *server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vault := r.URL.Query().Get("vault")
	if vault == "" {
		http.Error(w, "missing vault", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	trashDir := filepath.Join(vp, ".trash")
	items := []map[string]string{}
	entries, err := os.ReadDir(trashDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				items = append(items, map[string]string{"name": e.Name() + "/", "path": ".trash/" + e.Name(), "isDir": "true"})
			} else {
				// Strip timestamp suffix `_<number>` for display name
				name := e.Name()
				if idx := strings.LastIndex(name, "_"); idx > 0 {
					if _, err := strconv.ParseInt(name[idx+1:], 10, 64); err == nil {
					name = name[:idx]
					}
				}
				// Replace __ back to /
				display := strings.ReplaceAll(name, "__", "/")
				items = append(items, map[string]string{"name": display, "path": ".trash/" + e.Name(), "isDir": "false"})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]interface{}{"items": items})
}

// handleTrashRestore moves a .trash/ item back to its original location.
func (s *server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		http.Error(w, "missing params", http.StatusBadRequest)
		return
	}
	// path is relative to vault root, e.g. ".trash/filename.md"
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	trashFull := filepath.Join(vp, strings.ReplaceAll(path, "/", string(os.PathSeparator)))
	// Compute original path: strip ".trash/" prefix, strip timestamp suffix, replace __ with /
	base := filepath.Base(trashFull)
	if idx := strings.LastIndex(base, "_"); idx > 0 {
		if _, err := strconv.ParseInt(base[idx+1:], 10, 64); err == nil {
			base = base[:idx]
		}
	}
	originalBase := strings.ReplaceAll(base, "__", "/")
	// Put it back at vault root (simplest restore — could preserve original dir from "__" separator)
	dest := filepath.Join(vp, originalBase)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		http.Error(w, "mkdir failed", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(trashFull, dest); err != nil {
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}
	// Reindex if it was a note
	if strings.HasSuffix(dest, ".md") {
		rel, _ := filepath.Rel(vp, dest)
		data, _ := os.ReadFile(dest)
		s.idx.updateNote(vault, rel, string(data))
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTrashEmpty permanently deletes .trash/ items.
func (s *server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vault := r.URL.Query().Get("vault")
	if vault == "" {
		http.Error(w, "missing vault", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	trashDir := filepath.Join(vp, ".trash")
	path := r.URL.Query().Get("path")
	if path != "" {
		// Delete single item
		full := filepath.Join(vp, strings.ReplaceAll(path, "/", string(os.PathSeparator)))
		// safety check
		if !strings.HasPrefix(full, trashDir) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		os.RemoveAll(full)
	} else {
		// Empty entire trash
		os.RemoveAll(trashDir)
		os.MkdirAll(trashDir, 0755)
	}
	w.WriteHeader(http.StatusNoContent)
}

// AttachmentItem is one image file in a vault, with metadata + reference count.
type AttachmentItem struct {
	Vault    string             `json:"vault"`
	Path     string             `json:"path"`
	Name     string             `json:"name"`
	Size     int64              `json:"size"`
	MTime    int64              `json:"mtime"`
	Ext      string             `json:"ext"`
	RefCount int                `json:"refCount"`
	Refs     []AttachmentRef    `json:"refs,omitempty"` // referencing notes (cap 8)
}

type AttachmentRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".svg": true,
}

// handleAttachments lists all image files in a vault with reference counts.
// GET /api/attachments?vault=X
// DELETE /api/attachments?vault=X&path=Y → moves to .trash/
func (s *server) handleAttachments(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}

	if r.Method == http.MethodDelete {
		path := r.URL.Query().Get("path")
		full, ok := s.safePath(vp, path)
		if !ok {
			errResponse(w, 400, "invalid path")
			return
		}
		if !s.isWritable(vault, path) {
			errResponse(w, 403, "path is not writable")
			return
		}
		// Move to .trash/ following the existing trash convention:
		// flatten path with `__` and append `_<unix>`.
		trashDir := filepath.Join(vp, ".trash")
		if err := os.MkdirAll(trashDir, 0755); err != nil {
			errResponse(w, 500, "mkdir failed")
			return
		}
		flat := strings.ReplaceAll(path, "/", "__")
		trashName := fmt.Sprintf("%s_%d", flat, time.Now().Unix())
		if err := os.Rename(full, filepath.Join(trashDir, trashName)); err != nil {
			errResponse(w, 500, "rename failed: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}

	// Walk the vault, collect images, and tally references.
	items := []AttachmentItem{}
	imagePaths := []string{}
	imageInfo := map[string]os.FileInfo{}
	mdContents := map[string]string{}

	_ = filepath.Walk(vp, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip .trash, .obsidian, hidden dirs
		rel, _ := filepath.Rel(vp, p)
		first := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
		if strings.HasPrefix(first, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if imageExtensions[ext] {
			imagePaths = append(imagePaths, rel)
			imageInfo[rel] = info
		} else if ext == ".md" {
			data, err := os.ReadFile(p)
			if err == nil {
				mdContents[rel] = string(data)
			}
		}
		return nil
	})

	// Count references. Obsidian's ![[...]] syntax can use:
	//   - basename ("foo.png" or "foo")
	//   - any suffix of the vault-relative path ("subdir/foo.png", "full/path/foo.png")
	// We try all suffixes plus also ![alt](path) for standard markdown images.
	refByImage := map[string]int{}
	// Per-image: count + a list of referencing note paths (capped). We
	// also need note titles for display — extract first-H1 (or fall back
	// to the basename) from each matched note's content.
	refsByImage := map[string][]AttachmentRef{}
	const refsCap = 8

	for imgRel := range imageInfo {
		segs := strings.Split(imgRel, string(os.PathSeparator))
		base := segs[len(segs)-1]
		baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
		patterns := []string{
			"![[" + base + "]]",
			"![[" + baseNoExt + "]]",
		}
		// Add every suffix of the path: "foo.png", "subdir/foo.png", ...
		for i := len(segs) - 1; i >= 0; i-- {
			suffix := strings.Join(segs[i:], "/")
			patterns = append(patterns,
				"![["+suffix+"]]",
				"![["+strings.TrimSuffix(suffix, filepath.Ext(suffix))+"]]",
				"]("+suffix+")",  // standard markdown ![alt](path)
			)
		}
		count := 0
		var refs []AttachmentRef
		for notePath, content := range mdContents {
			for _, pat := range patterns {
				if strings.Contains(content, pat) {
					count++
					if len(refs) < refsCap {
						_, body := parseFrontmatter(content)
						title := extractTitle(body, notePath)
						refs = append(refs, AttachmentRef{Path: notePath, Title: title})
					}
					break
				}
			}
		}
		refByImage[imgRel] = count
		if len(refs) > 0 {
			refsByImage[imgRel] = refs
		}
	}

	for _, rel := range imagePaths {
		info := imageInfo[rel]
		items = append(items, AttachmentItem{
			Vault:    vault,
			Path:     rel,
			Name:     filepath.Base(rel),
			Size:     info.Size(),
			MTime:    info.ModTime().Unix(),
			Ext:      strings.ToLower(filepath.Ext(rel)),
			RefCount: refByImage[rel],
			Refs:     refsByImage[rel],
		})
	}

	// Sort: orphans first (refCount==0), then by mtime desc.
	sort.SliceStable(items, func(i, j int) bool {
		if (items[i].RefCount == 0) != (items[j].RefCount == 0) {
			return items[i].RefCount == 0
		}
		return items[i].MTime > items[j].MTime
	})

	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  len(items),
		"orphan": len(items) - countWithRefs(items),
	})
}

func countWithRefs(items []AttachmentItem) int {
	n := 0
	for _, it := range items {
		if it.RefCount > 0 {
			n++
		}
	}
	return n
}

// handleTags returns frontmatter tags aggregated across all notes.
// GET /api/tags[?vault=X] → [{ tag: "...", count: N, vaults: [...] }, ...]
func (s *server) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vaultFilter := r.URL.Query().Get("vault")

	type TagEntry struct {
		Tag    string   `json:"tag"`
		Count  int      `json:"count"`
		Vaults []string `json:"vaults"`
	}

	tagCount := map[string]int{}
	tagVaults := map[string]map[string]bool{}

	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		vaultName := e.Name()
		if vaultFilter != "" && vaultName != vaultFilter {
			continue
		}
		vp := filepath.Join(s.vaultsDir, vaultName)
		_ = filepath.Walk(vp, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(vp, p)
			first := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
			if strings.HasPrefix(first, ".") {
				return nil
			}
			if !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			fm, _ := parseFrontmatter(string(data))
			if fm == nil {
				return nil
			}
			// tags: [a, b]  OR  tags: a (single string)  OR  tag: x
			collect := func(v any) {
				switch t := v.(type) {
				case string:
					if t != "" {
						tagCount[t]++
						if tagVaults[t] == nil {
							tagVaults[t] = map[string]bool{}
						}
						tagVaults[t][vaultName] = true
					}
				case []any:
					for _, item := range t {
						if s, ok := item.(string); ok && s != "" {
							tagCount[s]++
							if tagVaults[s] == nil {
								tagVaults[s] = map[string]bool{}
							}
							tagVaults[s][vaultName] = true
						}
					}
				}
			}
			if v, ok := fm["tags"]; ok {
				collect(v)
			}
			if v, ok := fm["tag"]; ok {
				collect(v)
			}
			return nil
		})
	}

	tags := make([]TagEntry, 0, len(tagCount))
	for tag, count := range tagCount {
		vaultsList := make([]string, 0, len(tagVaults[tag]))
		for v := range tagVaults[tag] {
			vaultsList = append(vaultsList, v)
		}
		sort.Strings(vaultsList)
		tags = append(tags, TagEntry{Tag: tag, Count: count, Vaults: vaultsList})
	}
	sort.SliceStable(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Tag < tags[j].Tag
	})

	jsonResponse(w, map[string]any{
		"tags":  tags,
		"total": len(tags),
	})
}

// handleGraph returns the wikilink graph for one or all vaults.
// GET /api/graph[?vault=X] → { nodes: [...], edges: [...] }
// handleGraph supports three scopes:
//   - none / ?vault=X         → whole vault (or all vaults if no vault).
//   - ?vault=X&folder=path    → only notes whose path is under <folder>.
//   - ?center=vault:path&depth=N → ego graph: <center> + N hops via outbound + inbound.
//
// `center` takes precedence over folder/vault filters.
func (s *server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vaultFilter := r.URL.Query().Get("vault") // empty = all vaults
	folder := strings.Trim(r.URL.Query().Get("folder"), "/")
	center := r.URL.Query().Get("center") // "vault:path"
	depthStr := r.URL.Query().Get("depth")
	depth := 1
	if d, err := strconv.Atoi(depthStr); err == nil && d >= 0 && d <= 5 {
		depth = d
	}

	type GraphNode struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Vault string `json:"vault"`
		Path  string `json:"path"`
		Refs  int    `json:"refs"`
		IsCenter bool `json:"isCenter,omitempty"`
	}
	type GraphEdge struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
	}

	s.idx.mu.RLock()
	defer s.idx.mu.RUnlock()

	// Step 1: build a vaultKey → NoteRef lookup of every note in the index,
	// deduped (allNotes contains both the bare name and `vault:name` keys).
	allByKey := map[string]NoteRef{}
	for _, ref := range s.idx.allNotes {
		key := vaultKey(ref.Vault, ref.Path)
		if _, ok := allByKey[key]; ok {
			continue
		}
		allByKey[key] = ref
	}

	// Step 2: pick the candidate set based on scope.
	noteByKey := map[string]NoteRef{}

	if center != "" {
		// Neighborhood graph: BFS from center using outbound + inbound.
		if _, ok := allByKey[center]; !ok {
			errResponse(w, 404, "center note not found")
			return
		}
		noteByKey[center] = allByKey[center]
		frontier := []string{center}
		for hop := 0; hop < depth; hop++ {
			next := []string{}
			for _, k := range frontier {
				// outbound: edges from k → resolve target normalized names → keys
				for _, t := range s.idx.outbound[k] {
					if ref, ok := s.idx.allNotes[t]; ok {
						tKey := vaultKey(ref.Vault, ref.Path)
						if _, seen := noteByKey[tKey]; !seen {
							noteByKey[tKey] = ref
							next = append(next, tKey)
						}
					}
				}
				// inbound: which notes link to k? inbound is keyed by normalized name.
				// k is "vault:path"; we need to find the normalized names that
				// resolve to k, then look them up in inbound.
				ref := allByKey[k]
				baseName := normalizeName(filepath.Base(ref.Path))
				compoundName := ref.Vault + ":" + baseName
				for _, candidate := range []string{baseName, compoundName} {
					for _, srcKey := range s.idx.inbound[candidate] {
						if srcRef, ok := allByKey[srcKey]; ok {
							sKey := vaultKey(srcRef.Vault, srcRef.Path)
							if _, seen := noteByKey[sKey]; !seen {
								noteByKey[sKey] = srcRef
								next = append(next, sKey)
							}
						}
					}
				}
			}
			frontier = next
		}
	} else {
		// Whole-vault or folder-scoped graph.
		for k, ref := range allByKey {
			if vaultFilter != "" && ref.Vault != vaultFilter {
				continue
			}
			if folder != "" {
				// Folder scope only meaningful with a vault filter — but if
				// folder is given without vault, apply against any vault.
				prefix := folder + "/"
				if ref.Path != folder && !strings.HasPrefix(ref.Path, prefix) {
					continue
				}
			}
			noteByKey[k] = ref
		}
	}

	nodes := make([]GraphNode, 0, len(noteByKey))
	for key, ref := range noteByKey {
		nodes = append(nodes, GraphNode{
			ID:       key,
			Label:    ref.Title,
			Vault:    ref.Vault,
			Path:     ref.Path,
			IsCenter: key == center,
		})
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	// Step 3: collect edges *between* nodes in the candidate set only.
	edges := []GraphEdge{}
	refCount := map[string]int{}
	for srcKey, targets := range s.idx.outbound {
		if _, ok := noteByKey[srcKey]; !ok {
			continue
		}
		for _, t := range targets {
			ref, ok := s.idx.allNotes[t]
			if !ok {
				continue
			}
			tKey := vaultKey(ref.Vault, ref.Path)
			if _, ok := noteByKey[tKey]; !ok {
				continue
			}
			edges = append(edges, GraphEdge{
				ID:     srcKey + "->" + tKey,
				Source: srcKey,
				Target: tKey,
			})
			refCount[tKey]++
		}
	}

	for i := range nodes {
		nodes[i].Refs = refCount[nodes[i].ID]
	}

	jsonResponse(w, map[string]any{
		"nodes":  nodes,
		"edges":  edges,
		"vault":  vaultFilter,
		"folder": folder,
		"center": center,
		"depth":  depth,
	})
}

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

// rateLimiter per-IP sliding window.
type rateLimiter struct {
	handler http.Handler
	mu sync.Mutex
	visitors map[string][]time.Time
	limit int
	window time.Duration
}

func newRateLimiter(h http.Handler, limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{handler: h, visitors: make(map[string][]time.Time), limit: limit, window: window}
}

func (rl *rateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 { ip = ip[:idx] }
	// Behind Traefik (only ingress path), honor forwarded client IP so the
	// per-IP bucket is real-per-user, not a single shared bridge-IP bucket.
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip = xri
	} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.Index(xff, ","); comma != -1 {
			ip = strings.TrimSpace(xff[:comma])
		} else {
			ip = strings.TrimSpace(xff)
		}
	}
	now := time.Now()
	rl.mu.Lock()
	cutoff := now.Add(-rl.window)
	var valid []time.Time
	for _, t := range rl.visitors[ip] {
		if t.After(cutoff) { valid = append(valid, t) }
	}
	if len(valid) >= rl.limit {
		rl.mu.Unlock()
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	rl.visitors[ip] = append(valid, now)
	rl.mu.Unlock()
	rl.handler.ServeHTTP(w, r)
}

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
	mux.HandleFunc("/api/upload", srv.handleUpload)
	mux.HandleFunc("/api/move", srv.handleMove)
	mux.HandleFunc("/api/folder", srv.handleFolder)
	mux.HandleFunc("/api/search", srv.handleSearch)
	mux.HandleFunc("/api/resolve", srv.handleResolve)
	mux.HandleFunc("/api/backlinks", srv.handleBacklinks)
	mux.HandleFunc("/api/stats", srv.handleStats)
	mux.HandleFunc("/api/sync-status", srv.handleSyncStatus)
	mux.HandleFunc("/api/vault-icon", srv.handleVaultIcon)
	mux.HandleFunc("/api/file", srv.handleFile)
	// Clean note URLs: /n/<vault>/<path-with-extension>. The shell is the
	// SPA — the frontend reads location.pathname on load and fetches the
	// note via /api/note. Real URLs make right-click→new-tab, bookmarks,
	// browser back/forward, and link sharing work natively.
	mux.HandleFunc("/n/", srv.handleIndex)
	mux.HandleFunc("/api/admin/config", srv.handleAdminConfig)
	mux.HandleFunc("/api/shares", srv.handleShareList)
	mux.HandleFunc("/api/shares/create", srv.handleShareCreate)
	mux.HandleFunc("/api/shares/revoke", srv.handleShareRevoke)
	mux.HandleFunc("/api/shares/revoke-all", srv.handleShareRevokeAll)
	mux.HandleFunc("/share/", srv.handleShareView)
	mux.HandleFunc("/api/admin/restart", srv.handleAdminRestart)
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/api/trash", srv.handleTrashList)
	mux.HandleFunc("/api/trash/restore", srv.handleTrashRestore)
	mux.HandleFunc("/api/trash/empty", srv.handleTrashEmpty)
	mux.HandleFunc("/api/attachments", srv.handleAttachments)
	mux.HandleFunc("/api/graph", srv.handleGraph)
	mux.HandleFunc("/api/tags", srv.handleTags)
	mux.Handle("/webdav/", srv.newWebDAVHandler())

	addr := ":" + *port
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      gzipMiddleware(newRateLimiter(mux, 240, time.Minute)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	srv.shutdown = make(chan struct{})

	go func() {
		log.Printf("listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		log.Println("shutting down (signal)")
	case <-srv.shutdown:
		log.Println("shutting down (admin)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}
