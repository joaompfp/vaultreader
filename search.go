package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
