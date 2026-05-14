package main

import (
	"embed"
	"regexp"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed static
var staticFiles embed.FS

// ─── Data structures ──────────────────────────────────────────────────────────

type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"isDir"`
	Ext      string      `json:"ext,omitempty"` // lowercase, with leading dot, "" for .md / dirs
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
	outbound map[string][]string  // vaultKey → []normalized target names
	inbound  map[string][]string  // normalized basename → []vaultKeys that link to it
	byName   map[string][]NoteRef // normalized basename → all notes with that name (no overwrite)
	byPath   map[string]NoteRef   // lower-case "vault:path" → NoteRef (unique per note)
	// byAttachment maps a lower-case basename (with extension) → [(vault, relPath)]
	// for every non-markdown file under each vault. Populated at index-build
	// time so resolveEmbed doesn't fall back to a per-render filepath.Walk
	// of the whole vault when a note has many image embeds.
	byAttachment map[string][]NoteRef
	// searchByVault holds pre-lowercased title/body/tags + mtime for every
	// indexed note, keyed by vault name. The search hot path iterates this
	// slice instead of walking the filesystem and re-reading + re-lowercasing
	// every note on every keystroke into the wikilink popup.
	searchByVault map[string][]searchEntry
}

// searchEntry is the per-note payload backing the search hot path.
type searchEntry struct {
	rel        string // path relative to vault root, e.g. "folder/note.md"
	title      string // original-case title for result display
	titleLower string
	bodyLower  string
	tagsLower  []string
	mtime      int64
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
