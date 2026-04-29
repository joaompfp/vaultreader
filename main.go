package main

import (
	"bytes"
	"context"
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/base64"
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
	Kind    string `json:"kind,omitempty"` // "" or "note" for notes; "image" for image attachments
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

// renderWikilinksPlain rewrites `[[name|alias]]` to a plain styled span,
// dropping the navigation. Used in shared notes — the share is one specific
// note, so wikilinks shouldn't escape into the vault. The visible text is
// the alias (when present) or the bare name (without #heading / ^block).
func renderWikilinksPlain(htmlStr string) string {
	return wikilinkRe.ReplaceAllStringFunc(htmlStr, func(match string) string {
		sub := wikilinkRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		name := sub[1]
		alias := sub[2]
		if alias == "" {
			alias = name
			if hash := strings.IndexAny(alias, "#^"); hash >= 0 {
				alias = alias[:hash]
			}
		}
		return fmt.Sprintf(`<span class="wikilink-plain">%s</span>`, htmlEscape(alias))
	})
}

// renderCallouts post-processes goldmark-rendered HTML to convert Obsidian
// callouts into styled <div class="callout callout-<type>"> blocks. The
// markdown:
//
//	> [!info] Document Metadata
//	> Body content here
//
// becomes (after goldmark):
//
//	<blockquote><p>[!info] Document Metadata</p><p>Body content here</p></blockquote>
//
// We match `<blockquote>\s*<p>[!type] optional title</p>` and rewrite to
// `<div class="callout callout-info"><div class="callout-title">…</div>…</div>`.
// Anything inside the blockquote past the first <p> is preserved verbatim.
//
// Supported types use the Obsidian set; unknown types still render via the
// generic `.callout` styles. The marker `[!type]-` (Obsidian fold-start
// syntax) is treated identically — fold state is not preserved.
var calloutRe = regexp.MustCompile(
	`(?s)<blockquote>\s*<p>\[!([a-zA-Z0-9_-]+)\]-?\s*([^<\n]*)</p>(.*?)</blockquote>`)

func renderCallouts(htmlStr string) string {
	return calloutRe.ReplaceAllStringFunc(htmlStr, func(match string) string {
		sub := calloutRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		typ := strings.ToLower(sub[1])
		title := strings.TrimSpace(sub[2])
		if title == "" {
			// Default title: capitalized type name (Obsidian's fallback).
			if len(typ) > 0 {
				title = strings.ToUpper(typ[:1]) + typ[1:]
			}
		}
		body := sub[3]
		return fmt.Sprintf(
			`<div class="callout callout-%s" data-callout="%s"><div class="callout-title"><span class="callout-icon"></span>%s</div><div class="callout-body">%s</div></div>`,
			htmlEscape(typ), htmlEscape(typ), htmlEscape(title), body)
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

// searchQuery is a parsed search input.
//   plain:    free-text terms (substring matched against name/title/body)
//   tags:     each must appear in frontmatter tags/tag (lowercased)
//   paths:    each must appear in the vault-relative path (lowercased)
//   titles:   each must appear in the title (lowercased)
//   modAfter: file mtime must be >= this unix timestamp (or 0)
//   modBefore: file mtime must be <= this unix timestamp (or 0)
type searchQuery struct {
	plain     string
	tags      []string
	paths     []string
	titles    []string
	modAfter  int64
	modBefore int64
}

// parseSearchQuery accepts strings like "tag:foo path:bar baz" and pulls
// out structured filters. Unknown prefixes are treated as plain text.
// Date format for modified: an ISO date (2026-01-01) or a relative spec
// (`<7d`, `>30d`).
func parseSearchQuery(q string) searchQuery {
	out := searchQuery{}
	parts := strings.Fields(q)
	var plainParts []string
	now := time.Now()
	for _, p := range parts {
		lower := strings.ToLower(p)
		switch {
		case strings.HasPrefix(lower, "tag:") && len(lower) > 4:
			out.tags = append(out.tags, strings.TrimPrefix(lower, "tag:"))
		case strings.HasPrefix(lower, "tags:") && len(lower) > 5:
			out.tags = append(out.tags, strings.TrimPrefix(lower, "tags:"))
		case strings.HasPrefix(lower, "path:") && len(lower) > 5:
			out.paths = append(out.paths, strings.TrimPrefix(lower, "path:"))
		case strings.HasPrefix(lower, "title:") && len(lower) > 6:
			out.titles = append(out.titles, strings.TrimPrefix(lower, "title:"))
		case strings.HasPrefix(lower, "modified:") && len(lower) > 9:
			spec := strings.TrimPrefix(lower, "modified:")
			parseModSpec(spec, now, &out)
		default:
			plainParts = append(plainParts, p)
		}
	}
	out.plain = strings.ToLower(strings.Join(plainParts, " "))
	return out
}

// extractTagsLower returns the note's tag list (lowercased) by reading
// `tags` and `tag` from frontmatter. Both string-list and single-string
// forms are supported, matching the frontend's chip rendering rules.
func extractTagsLower(fm map[string]any) []string {
	out := []string{}
	collect := func(v any) {
		switch t := v.(type) {
		case string:
			if t != "" {
				out = append(out, strings.ToLower(t))
			}
		case []any:
			for _, item := range t {
				if s, ok := item.(string); ok && s != "" {
					out = append(out, strings.ToLower(s))
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
	return out
}

func parseModSpec(spec string, now time.Time, q *searchQuery) {
	// `<7d`, `>30d`, `<2026-01-01`, `>2026-01-01`. Default operator `>` if missing.
	op := byte('>')
	if len(spec) > 0 && (spec[0] == '<' || spec[0] == '>' || spec[0] == '=') {
		op = spec[0]
		spec = spec[1:]
	}
	if spec == "" {
		return
	}
	var t time.Time
	// Relative form: 7d, 30d, 2w, 1m, 1y
	if len(spec) >= 2 {
		unit := spec[len(spec)-1]
		nstr := spec[:len(spec)-1]
		if n, err := strconv.Atoi(nstr); err == nil && n >= 0 {
			d := time.Duration(0)
			switch unit {
			case 'd':
				d = time.Duration(n) * 24 * time.Hour
			case 'w':
				d = time.Duration(n) * 7 * 24 * time.Hour
			case 'm':
				d = time.Duration(n) * 30 * 24 * time.Hour
			case 'y':
				d = time.Duration(n) * 365 * 24 * time.Hour
			}
			if d > 0 {
				t = now.Add(-d)
			}
		}
	}
	// Absolute date form: YYYY-MM-DD
	if t.IsZero() {
		if pt, err := time.Parse("2006-01-02", spec); err == nil {
			t = pt
		}
	}
	if t.IsZero() {
		return
	}
	switch op {
	case '<':
		// "modified:<7d" → modified BEFORE 7 days ago, i.e. older than 7 days.
		// "modified:<2026-01-01" → before that date.
		q.modBefore = t.Unix()
	case '>':
		// "modified:>7d" → modified AFTER 7 days ago, i.e. within last 7 days.
		q.modAfter = t.Unix()
	case '=':
		// Exact day — narrow the window to 24h around it.
		q.modAfter = t.Unix()
		q.modBefore = t.Add(24 * time.Hour).Unix()
	}
}

func searchVault(vaultPath, vaultName, query string) []SearchResult {
	q := parseSearchQuery(query)
	plain := q.plain
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
		relLower := strings.ToLower(rel)
		baseLower := strings.ToLower(info.Name())

		// Operator filters (mtime + path) — cheap; skip the read if any fails.
		mtime := info.ModTime().Unix()
		if q.modAfter > 0 && mtime < q.modAfter {
			return nil
		}
		if q.modBefore > 0 && mtime > q.modBefore {
			return nil
		}
		for _, p := range q.paths {
			if !strings.Contains(relLower, p) {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		contentLower := strings.ToLower(content)

		fm, body := parseFrontmatter(content)
		title := extractTitle(body, rel)
		titleLower := strings.ToLower(title)

		// Operator filters (title + tags) — need parsed frontmatter.
		for _, t := range q.titles {
			if !strings.Contains(titleLower, t) {
				return nil
			}
		}
		if len(q.tags) > 0 {
			noteTags := extractTagsLower(fm)
			for _, want := range q.tags {
				found := false
				for _, have := range noteTags {
					// Match if exact, hierarchical descendant (work/active),
					// or substring (so tag:london matches "london-2026").
					if have == want ||
						strings.HasPrefix(have, want+"/") ||
						strings.Contains(have, want) {
						found = true
						break
					}
				}
				if !found {
					return nil
				}
			}
		}

		// Plain-text matching: when there's a `plain` portion of the query,
		// require it to appear in name OR title OR body. When the query is
		// pure operators (e.g. "tag:work modified:>7d"), every file passing
		// the operator filters is a hit.
		nameMatch, contentMatch, titleMatch := false, false, false
		if plain != "" {
			nameMatch = strings.Contains(baseLower, plain)
			contentMatch = strings.Contains(contentLower, plain)
			titleMatch = strings.Contains(titleLower, plain)
			if !nameMatch && !contentMatch && !titleMatch {
				return nil
			}
		}

		// Scoring (same shape as before; `plain == ""` skips the +N body bonuses).
		score := 0.0
		if plain != "" {
			if titleLower == plain {
				score += 20
			} else if titleMatch {
				score += 10
			}
			if nameMatch {
				score += 5
			}
			if contentMatch {
				n := strings.Count(contentLower, plain)
				if n > 5 {
					n = 5
				}
				score += float64(n)
			}
		} else {
			// Operator-only query: small base score so all results sort.
			score += 5
		}
		// Recency: 0 days old → +3, 30+ days → 0.
		ageDays := float64(now-mtime) / 86400
		if ageDays < 0 {
			ageDays = 0
		}
		if ageDays < 30 {
			score += 3.0 * (1.0 - ageDays/30.0)
		}

		// Excerpt around the first plain-text match (skip if operator-only).
		excerpt := ""
		if contentMatch && plain != "" {
			pos := strings.Index(contentLower, plain)
			start := pos - 60
			if start < 0 {
				start = 0
			}
			end := pos + 60 + len(plain)
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

	// Image attachments — second pass. Only includes image hits when the
	// query has a plain-text portion (operator-only queries like
	// "tag:foo modified:>7d" don't apply to images, since images carry
	// no frontmatter).
	if plain != "" && len(q.tags) == 0 {
	_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip dotfile-prefixed top-level dirs (.trash, .obsidian, etc).
		rel, _ := filepath.Rel(vaultPath, path)
		relLower := strings.ToLower(rel)
		first := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
		if strings.HasPrefix(first, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !imageExtensions[ext] {
			return nil
		}
		// Honor path:/modified: operator filters on images too.
		mtime := info.ModTime().Unix()
		if q.modAfter > 0 && mtime < q.modAfter {
			return nil
		}
		if q.modBefore > 0 && mtime > q.modBefore {
			return nil
		}
		for _, p := range q.paths {
			if !strings.Contains(relLower, p) {
				return nil
			}
		}
		base := strings.ToLower(info.Name())
		if !strings.Contains(base, plain) {
			return nil
		}
		score := 3.0
		if strings.HasPrefix(base, plain) {
			score += 1
		}
		ageDays := float64(now-mtime) / 86400
		if ageDays < 0 {
			ageDays = 0
		}
		if ageDays < 30 {
			score += 1.5 * (1.0 - ageDays/30.0)
		}
		hits = append(hits, scored{
			r: SearchResult{
				Vault:   vaultName,
				Path:    rel,
				Title:   info.Name(),
				Excerpt: "",
				Kind:    "image",
			},
			score: score,
		})
		return nil
	})
	} // end "if plain != '' && len(q.tags) == 0" image-pass guard

	// Sort: score desc; ties broken by path asc for stability.
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
	_ = md.Convert([]byte(body), &buf)
	// Post-process: callouts → styled divs, wikilinks → plain spans (no
	// off-share nav), then rewrite image src for the share-file route.
	renderedHTML := renderCallouts(buf.String())
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

	// Trash filename: encode the original path in base64-url so restore is
	// unambiguous. Format: VRTRASH_<b64>_<unix>.<ext>. The legacy `__`-flat
	// scheme conflicted with files whose names contained double underscores.
	trashPath := filepath.Join(trashDir, makeTrashName(path, time.Now().Unix()))
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
	trashPath := filepath.Join(trashDir, makeTrashName(path, time.Now().Unix()))
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
	rendered = renderCallouts(rendered)
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

// handleTemplates lists `.md` files under <vault>/templates/. Each entry
// returns its path, display name (basename without .md), and body. The
// frontend reads the body, runs variable expansion ({{date}}, etc.), and
// POSTs a new note with the result.
func (s *server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vault := r.URL.Query().Get("vault")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	tplDir := filepath.Join(vp, "templates")
	type tplEntry struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Body string `json:"body"`
	}
	out := []tplEntry{}
	_ = filepath.Walk(tplDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vp, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		out = append(out, tplEntry{
			Path: rel,
			Name: strings.TrimSuffix(info.Name(), ".md"),
			Body: string(data),
		})
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	jsonResponse(w, map[string]any{"templates": out})
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


// Trash naming: VRTRASH_<base64url(path)>_<unix><ext>. Encoding the full
// vault-relative path eliminates the ambiguity of the legacy `__`-flatten
// scheme (where files containing `__` round-tripped wrong) and supports
// arbitrary characters in original paths. The .ext is preserved at the
// end so OS file pickers / `file` command still recognize the type.
const trashSentinel = "VRTRASH_"

func makeTrashName(originalPath string, unix int64) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(originalPath))
	ext := filepath.Ext(originalPath)
	return fmt.Sprintf("%s%s_%d%s", trashSentinel, enc, unix, ext)
}

// decodeTrashName: trash basename → (originalPath, true) or ("", false).
// Falls back to the legacy `__` scheme when the sentinel isn't present so
// already-trashed files keep working.
func decodeTrashName(base string) (string, bool) {
	if !strings.HasPrefix(base, trashSentinel) {
		return "", false
	}
	rest := base[len(trashSentinel):]
	// Expected: <b64>_<unix><ext>. Last `_` separates b64 from unix+ext.
	cut := strings.LastIndex(rest, "_")
	if cut <= 0 {
		return "", false
	}
	enc := rest[:cut]
	data, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// legacyDecodeTrashName: best-effort decode for entries created before the
// sentinel scheme. Mirrors the old logic; ambiguous for files with `__` in
// real names but the old scheme can't do better.
func legacyDecodeTrashName(base string) string {
	// Strip optional `_<unix>` collision suffix.
	if idx := strings.LastIndex(base, "_"); idx > 0 {
		if _, err := strconv.ParseInt(base[idx+1:], 10, 64); err == nil {
			base = base[:idx]
		} else {
			// Maybe `_<unix>.<ext>`
			ext := filepath.Ext(base)
			noext := strings.TrimSuffix(base, ext)
			if i2 := strings.LastIndex(noext, "_"); i2 > 0 {
				if _, err := strconv.ParseInt(noext[i2+1:], 10, 64); err == nil {
					base = noext[:i2] + ext
				}
			}
		}
	}
	return strings.ReplaceAll(base, "__", "/")
}

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
				// Display name = the original vault-relative path. Decode via
				// the sentinel scheme; fall back to legacy for older entries.
				display, ok := decodeTrashName(e.Name())
				if !ok {
					display = legacyDecodeTrashName(e.Name())
				}
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
	// Decode the trash filename back to its original vault-relative path.
	// New scheme uses VRTRASH_<b64> and is exact; legacy `__`-flatten scheme
	// kicks in for entries created before the sentinel was introduced.
	base := filepath.Base(trashFull)
	originalBase, ok := decodeTrashName(base)
	if !ok {
		originalBase = legacyDecodeTrashName(base)
	}
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
		trashName := makeTrashName(path, time.Now().Unix())
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
	mux.HandleFunc("/api/templates", srv.handleTemplates)
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
