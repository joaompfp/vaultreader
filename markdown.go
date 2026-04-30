package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ─── Markdown rendering ───────────────────────────────────────────────────────

// wikilinkAliasPipeSentinel is swapped in for `|` inside `[[…|…]]` before
// goldmark parses the markdown — otherwise the alias pipe inside a table
// cell is treated as a column boundary, splitting the wikilink across
// two cells. We swap it back after rendering so renderWikilinks /
// renderWikilinksPlain see the canonical `[[name|alias]]` form.
const wikilinkAliasPipeSentinel = "\x01WLPIPE\x01"

func protectWikilinkPipes(raw string) string {
	return wikilinkRe.ReplaceAllStringFunc(raw, func(m string) string {
		// Only the *first* `|` inside the link is the alias separator;
		// any further `|` would already be illegal per the regex (which
		// doesn't allow `|` inside the alias group), so a single
		// replacement is enough.
		return strings.Replace(m, "|", wikilinkAliasPipeSentinel, 1)
	})
}

func restoreWikilinkPipes(s string) string {
	return strings.ReplaceAll(s, wikilinkAliasPipeSentinel, "|")
}

func renderMarkdown(raw string) string {
	var buf bytes.Buffer
	if err := md.Convert([]byte(protectWikilinkPipes(raw)), &buf); err != nil {
		return "<pre>" + raw + "</pre>"
	}
	return restoreWikilinkPipes(buf.String())
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
		// Strip a trailing backslash that the regex picks up when the
		// source uses Obsidian's `\|` escape (`[[name\|alias]]`). The
		// escape is unnecessary here because protectWikilinkPipes already
		// shields the alias pipe from goldmark's table parser, but we
		// still want to render correctly when users carry escaped forms
		// over from Obsidian.
		name := strings.TrimRight(sub[1], `\`)
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
		name := strings.TrimRight(sub[1], `\`)
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
