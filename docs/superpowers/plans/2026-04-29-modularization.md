# VaultReader Modularization Implementation Plan

> ⚠️ **Status (2026-04-30):** Stage 1 SHIPPED in commit `cb5b7c6` on `main`. **Stage 2 ABANDONED** — the ES-modules + progressive-override architecture in this plan races Alpine 3.x's auto-bootstrap and is not viable. See the "Stage 2 retrospective" section in [docs/superpowers/specs/2026-04-29-modularization-design.md](../specs/2026-04-29-modularization-design.md) before considering a retry. The Stage 2 sections of this plan (Tasks 2.0 onward) are kept for historical reference only.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `main.go` (3,588 lines) and the inline `<script>` in `static/index.html` (~3,550 lines of `vaultApp()`) into focused modules, with byte-identical runtime behavior, no JS build step, and `package main` preserved.

**Architecture:** Stage 1 splits `main.go` into ~19 sibling `.go` files in `package main` (Go's package scope makes this a pure organisational change — same compiler input, same binary). Stage 2 extracts the inline `vaultApp()` factory into ~24 native ES modules under `static/js/`, assembled via the mixin `Object.assign({}, ...mixins)` pattern, with no bundler.

**Tech Stack:** Go 1.21 (`package main`, `go build`); native ES Modules (`<script type="module">`, `import`/`export`); Alpine.js v3 (existing); embed.FS (existing); Docker scratch image (existing).

**Spec:** [docs/superpowers/specs/2026-04-29-modularization-design.md](../specs/2026-04-29-modularization-design.md)

**Verification command (used throughout):**

```bash
# Build + deploy + smoke-test the bridge IP (matches CLAUDE.md verification pattern)
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/vaults"  # must return JSON array
curl -s "http://$IP:8080/api/tree?vault=pessoal" | head -c 200  # must return JSON
```

---

## Pre-flight checks

- [ ] **Step 1: Confirm clean working tree**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
git status
```

Expected: `nothing to commit, working tree clean` (or only the design doc/plan in `docs/superpowers/`).

- [ ] **Step 2: Capture pre-refactor baseline**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
wc -l main.go static/index.html static/style.css > /tmp/vr-baseline-sizes.txt
go build -o /tmp/vr-baseline-binary . 2>&1 | tail -5
sha256sum /tmp/vr-baseline-binary > /tmp/vr-baseline-sha.txt
cat /tmp/vr-baseline-sizes.txt
cat /tmp/vr-baseline-sha.txt
```

Expected: `wc -l` shows `main.go` ~3588 lines; `go build` succeeds; sha256 captured.

If `go` isn't installed locally, skip the binary hash — Stage 1 verification will rely on `docker build` instead.

- [ ] **Step 3: Snapshot current behaviour for comparison**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
mkdir -p /tmp/vr-baseline
curl -s "http://$IP:8080/api/vaults"            > /tmp/vr-baseline/vaults.json
curl -s "http://$IP:8080/api/writable-paths"    > /tmp/vr-baseline/writable.json
curl -s "http://$IP:8080/api/stats"             > /tmp/vr-baseline/stats.json
curl -sI "http://$IP:8080/health" | head -1     > /tmp/vr-baseline/health.txt
```

Expected: each file has non-empty JSON / "HTTP/1.1 200" content.

---

# Stage 1 — Backend split

**Goal:** Split `main.go` (3,588 lines) into ~19 focused `.go` files in `package main`. Zero behaviour change. The compiler concatenates all package files, so the resulting binary is byte-identical (modulo build cache nondeterminism).

**Approach:** Each task moves one logical group of code to its target file. After every task, the project must `go build` cleanly and pass smoke tests. Commit after every task.

**Pattern for every Stage 1 task:**
1. Create the new file with `package main` declaration + required imports
2. Move the targeted code from `main.go` to the new file (cut from one, paste into the other — no logic change)
3. Run `go build` (or `docker build` if no local Go) to verify
4. Smoke-test the affected endpoint(s)
5. Commit

**Imports rule:** Each new `.go` file gets its own `import (...)` block. Only include packages used by the code in *that* file. After Stage 1 is done, `go build` will tell you if any import is missing or unused. Don't try to be clever — if you cut code and the imports go unused, `goimports` will fix it. Run `goimports -w *.go` after each task to keep the imports clean (or `gofmt -s -w *.go` if `goimports` isn't available).

---

### Task 1.0: Create the Stage 1 worktree

**Files:**
- Create: working branch `refactor/backend-split`

- [ ] **Step 1: Create branch**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
git checkout -b refactor/backend-split
git status
```

Expected: switched to a new branch with no changes.

---

### Task 1.1: Extract `http.go` — gzip middleware + JSON helpers + rate limiter

**Files:**
- Create: `http.go`
- Modify: `main.go` (remove lines 1130-1168 and 3426-3471, except for `// ─── Main ──` header)

**Note:** Despite the section header "Main" at L3426, the rate limiter logic doesn't really belong with `func main()`. We move it to `http.go` because it's HTTP-layer infrastructure.

- [ ] **Step 1: Create `http.go` with gzip + json helpers + rate limiter**

```go
package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ─── Gzip middleware ──────────────────────────────────────────────────────────

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, Writer: gz}, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (g *gzipResponseWriter) Write(b []byte) (int, error)  { return g.Writer.Write(b) }
func (g *gzipResponseWriter) Header() http.Header          { return g.ResponseWriter.Header() }
func (g *gzipResponseWriter) WriteHeader(code int)         { g.ResponseWriter.WriteHeader(code) }

// ─── HTTP response helpers ────────────────────────────────────────────────────

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func errResponse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ─── Rate limiter ─────────────────────────────────────────────────────────────

type rateLimiter struct {
	next   http.Handler
	limit  int
	window time.Duration
	mu     sync.Mutex
	hits   map[string][]time.Time
}

func newRateLimiter(h http.Handler, limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{next: h, limit: limit, window: window, hits: make(map[string][]time.Time)}
}

func (rl *rateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = r.Header.Get("X-Forwarded-For")
		if comma := strings.Index(ip, ","); comma >= 0 {
			ip = ip[:comma]
		}
		ip = strings.TrimSpace(ip)
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	rl.mu.Lock()
	now := time.Now()
	cutoff := now.Add(-rl.window)
	hits := rl.hits[ip]
	var fresh []time.Time
	for _, t := range hits {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= rl.limit {
		rl.hits[ip] = fresh
		rl.mu.Unlock()
		errResponse(w, 429, "rate limit exceeded")
		return
	}
	rl.hits[ip] = append(fresh, now)
	rl.mu.Unlock()
	rl.next.ServeHTTP(w, r)
}
```

- [ ] **Step 2: Remove the moved code from `main.go`**

Delete from `main.go`:
- Lines `1130-1153` (gzip middleware + gzipResponseWriter)
- Lines `1155-1168` (jsonResponse + errResponse)
- Lines `3426-3471` excluding `func main()` itself (the rateLimiter type, newRateLimiter, ServeHTTP method)

After deletion, `main.go` should still contain `func main()` and everything else outside those ranges.

- [ ] **Step 3: Build to verify**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
go build -o /tmp/vr-stage1-after-task1 . 2>&1
```

Expected: succeeds with no errors.

If `go` isn't available locally:

```bash
cd /home/joao/docker
bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -5
```

Expected: container builds and starts successfully.

- [ ] **Step 4: Smoke test**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/vaults"
diff <(curl -s "http://$IP:8080/api/vaults") /tmp/vr-baseline/vaults.json
```

Expected: empty diff. Same response as baseline.

- [ ] **Step 5: Commit**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
git add http.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract http.go (gzip + json helpers + rate limiter)

Pure code move from main.go. Same package main, same compiled output.
Smoke-tested: /api/vaults response matches pre-refactor baseline.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.2: Extract `markdown.go` — renderer, embed expansion, wikilinks, callouts, frontmatter

**Files:**
- Create: `markdown.go`
- Modify: `main.go` (remove lines 332-655 — Markdown rendering + Frontmatter sections)

This is the largest single move (~325 lines). It includes:
- `wikilinkAliasPipeSentinel` const
- `protectWikilinkPipes`, `restoreWikilinkPipes`
- `renderMarkdown`
- `expandEmbeds`, `resolveEmbed`
- `htmlEscape`, `urlEscape`
- `renderWikilinks`, `renderWikilinksPlain`
- `calloutRe` var, `renderCallouts`
- `noteHref`, `resolveWikilinkTarget`
- `parseFrontmatter`

It does NOT include the goldmark `init()` (lines 117-132) or the regex vars `wikilinkRe`/`embedRe`/`headingRe`/`imageExtRe` (lines 107-112). Those move with the data structures in Task 1.4.

- [ ] **Step 1: Create `markdown.go`**

Move lines 332-655 from `main.go` verbatim (the entire "Markdown rendering" section through end of `parseFrontmatter`). Add `package main` + imports at top:

```go
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
```

Body: paste from current `main.go:332-655` (the section dividers `// ─── Markdown rendering ──` through the end of `parseFrontmatter` at L655).

- [ ] **Step 2: Remove from `main.go`**

Delete lines 332-655 from `main.go` (the entire "Markdown rendering" + "Frontmatter" sections).

- [ ] **Step 3: Tidy imports**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
gofmt -s -w *.go
# If goimports is installed:
which goimports && goimports -w *.go
```

If `goimports` isn't available, manually verify `main.go`'s import block doesn't have `gopkg.in/yaml.v3` (it now lives in markdown.go) — but it's used by other handlers too, so keep it if those still reference it.

- [ ] **Step 4: Build**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
go build -o /tmp/vr-stage1-after-task2 . 2>&1
```

Or via docker:

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -5
```

Expected: successful build.

- [ ] **Step 5: Smoke test markdown rendering specifically**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# Pick any note that exists; testing /api/note exercises renderMarkdown + renderWikilinks + renderCallouts.
curl -s "http://$IP:8080/api/note?vault=pessoal&path=Welcome.md" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print('html-bytes:', len(d.get('html', '')))
print('has-wikilinks:', 'class=\"wikilink' in d.get('html', ''))
print('frontmatter-keys:', list(d.get('frontmatter', {}).keys()))
"
```

Expected: non-zero html-bytes, wikilink rendering still works, frontmatter parsed.

- [ ] **Step 6: Commit**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
git add markdown.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract markdown.go (rendering, embeds, wikilinks, callouts, frontmatter)

Pure code move from main.go. Includes renderMarkdown, expandEmbeds,
renderWikilinks, renderWikilinksPlain, renderCallouts,
protectWikilinkPipes, parseFrontmatter, and supporting helpers.
Smoke-tested: /api/note response renders correctly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.3: Extract `index.go` — NoteIndex struct + methods

**Files:**
- Create: `index.go`
- Modify: `main.go` (remove lines 134-330 — Index section)

Includes:
- `newIndex` factory
- `normalizeName`, `extractTitle` helpers
- `(idx *NoteIndex) buildAll`, `updateNote`, `removeNote`, `resolve`, `getBacklinks`

Does NOT include the `NoteIndex` and `NoteRef` struct definitions themselves (those stay in `data.go` — see Task 1.4).

- [ ] **Step 1: Create `index.go`**

```go
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)
```

Body: lines 134-330 from current `main.go` (the entire "Index" section, starting from `// ─── Index ──` header through the end of `getBacklinks`).

- [ ] **Step 2: Remove from `main.go`**

Delete lines 134-330 from `main.go`.

- [ ] **Step 3: Build**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader && go build . 2>&1
```

Or docker build.

Expected: succeeds.

- [ ] **Step 4: Smoke test wikilink resolution + backlinks**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# resolve a known wikilink target
curl -s "http://$IP:8080/api/resolve?name=Welcome&vault=pessoal" | head
# fetch backlinks for a note
curl -s "http://$IP:8080/api/backlinks?vault=pessoal&path=Welcome.md" | head
```

Expected: resolve returns `{"vault":"pessoal","path":"Welcome.md"}`-shaped JSON; backlinks returns array.

- [ ] **Step 5: Commit**

```bash
git add index.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract index.go (NoteIndex methods + helpers)

Pure code move. Includes buildAll, updateNote, removeNote, resolve,
getBacklinks, normalizeName, extractTitle, newIndex.
Smoke-tested: /api/resolve and /api/backlinks return expected shapes.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.4: Extract `data.go` — shared types + regex vars + goldmark init

**Files:**
- Create: `data.go`
- Modify: `main.go` (remove lines 41-132 — types + regex vars + goldmark init)

Includes:
- `staticFiles embed.FS` (this stays here because it's the package-level data declaration; we keep `//go:embed` directive with it)
- All shared structs: `TreeNode`, `NoteResponse`, `BacklinkRef`, `SearchResult`, `ResolveResult`, `NoteRef`, `NoteIndex`
- `vaultKey` helper
- Regex vars: `wikilinkRe`, `embedRe`, `headingRe`, `imageExtRe`
- `md goldmark.Markdown` var + `init()` that configures goldmark

**Decision rationale:** these are types and configuration shared across many files (markdown.go, search.go, files.go, notes.go, etc.). Keeping them in one `data.go` avoids per-file duplication of import boilerplate AND keeps the `//go:embed` declaration somewhere visible to readers looking for "where do static files come from?"

- [ ] **Step 1: Create `data.go`**

```go
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
	Ext      string      `json:"ext,omitempty"`
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
	Score   int    `json:"score"`
	Kind    string `json:"kind,omitempty"`
}

type ResolveResult struct {
	Vault string `json:"vault"`
	Path  string `json:"path"`
}

type NoteRef struct {
	Vault    string
	Path     string
	Title    string
	Tags     []string
	Outgoing []string
}

type NoteIndex struct {
	mu       sync.RWMutex
	byKey    map[string]*NoteRef
	byName   map[string][]*NoteRef
	incoming map[string][]string
}

// vaultKey is "vault:path" — unique across all vaults
func vaultKey(vault, path string) string {
	return vault + ":" + path
}

// ─── Shared regexes ───────────────────────────────────────────────────────────

var (
	wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+?)(?:\|([^\]]+?))?\]\]`)
	embedRe    = regexp.MustCompile(`!\[\[([^\]|]+?)(?:\|([^\]]+?))?\]\]`)
	headingRe  = regexp.MustCompile(`(?m)^#+\s+(.+)$`)
	imageExtRe = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|svg|webp|bmp|avif)$`)
)

// ─── Goldmark renderer ────────────────────────────────────────────────────────

var md goldmark.Markdown

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Strikethrough,
			extension.Table,
			extension.TaskList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
}
```

(Verify exact import paths and goldmark setup against the current `main.go:117-132` — the snippet above is the canonical shape but if you find differences in the actual `init()` body, use the actual code.)

- [ ] **Step 2: Remove from `main.go`**

Delete lines 41-132 from `main.go` — the `//go:embed` block, the Data structures section, the regex vars, and the goldmark init.

- [ ] **Step 3: Build**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader && go build . 2>&1
```

Expected: succeeds.

- [ ] **Step 4: Smoke test**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# Static files served from embed.FS
curl -sI "http://$IP:8080/index.html" | head -3
curl -sI "http://$IP:8080/style.css" | head -3
# Tree (uses TreeNode)
curl -s "http://$IP:8080/api/tree?vault=pessoal" | head -c 200
```

Expected: all return 200 OK with appropriate content-types; tree JSON matches TreeNode shape.

- [ ] **Step 5: Commit**

```bash
git add data.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract data.go (shared types, regexes, goldmark init, embed.FS)

Pure code move. The //go:embed directive moves with staticFiles var.
All shared structs (TreeNode, NoteIndex, NoteRef, etc.), shared
regexes (wikilinkRe, embedRe, headingRe, imageExtRe), and goldmark
init() consolidated into data.go.
Smoke-tested: static file serving + /api/tree work.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.5: Extract `search.go` — search query parsing + searchVault

**Files:**
- Create: `search.go`
- Modify: `main.go` (remove search section)

Includes (lines 729-1087 of current `main.go`):
- `searchQuery` struct
- `parseSearchQuery`
- `extractTagsLower`
- `parseModSpec`
- `searchVault`

Does NOT include `handleSearch` — that's an HTTP handler, stays in `main.go` until Task 1.10 (notes.go) where it gets grouped with note-related handlers.

- [ ] **Step 1: Create `search.go`**

```go
package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)
```

Body: lines 729-1087 from current `main.go` (the "Search" section through end of `searchVault`).

- [ ] **Step 2: Remove from `main.go`**

Delete lines 729-1087.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test search**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/search?q=tag:work" | head -c 200
curl -s "http://$IP:8080/api/search?q=path:agents+modified:%3E30d" | head -c 200
```

Expected: JSON arrays of search results.

- [ ] **Step 5: Commit**

```bash
git add search.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract search.go (query parsing + searchVault)

Pure code move. Operators tag:, path:, title:, modified:>Nd parsed by
parseSearchQuery; ranking (×20 title, ×5 filename, ×1 body, +0..3
recency) computed in searchVault.
Smoke-tested: /api/search with operators returns ranked results.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.6: Extract `admin.go` — config + admin handlers + writable paths

**Files:**
- Create: `admin.go`
- Modify: `main.go` (remove admin section)

Includes (current `main.go:1170-1335`):
- `AdminConfig` struct
- `server` struct (NOTE: this stays here — it's strongly tied to admin config; many other files reference it but they reference its receivers, not the type definition itself)
- `(s *server) configPath`, `loadConfig`, `saveConfig`, `isWritable`
- `requireAdminToken`, `handleWritablePaths`, `handleAdminConfig`, `handleHealth`, `handleAdminRestart`

**Note:** `server` struct definition migrates here because it's the data model that holds the config + share store. Other files reference `*server` receivers but only this file defines the type itself.

- [ ] **Step 1: Create `admin.go`**

```go
package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)
```

Body: lines 1170-1335 from current `main.go` (the "Admin config" + "Admin handlers" sections, including the `server` struct definition).

- [ ] **Step 2: Remove from `main.go`**

Delete lines 1170-1335.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test admin endpoints**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/writable-paths"
curl -sI "http://$IP:8080/health" | head -1
# Without admin token, /api/admin/config should return 403
curl -s -o /dev/null -w "%{http_code}\n" "http://$IP:8080/api/admin/config"
```

Expected: writable-paths returns JSON; /health returns 200; /api/admin/config returns 403.

- [ ] **Step 5: Commit**

```bash
git add admin.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract admin.go (server struct, config, admin handlers)

Pure code move. server struct definition + AdminConfig + load/save +
isWritable + admin endpoints all in one place.
Smoke-tested: /api/writable-paths public; /api/admin/config still
gated; /health returns 200.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.7: Extract `shares.go` — ShareStore + share handlers + share-page rendering

**Files:**
- Create: `shares.go`
- Modify: `main.go` (remove share section)

Includes (current `main.go:1337-1781`):
- `ShareEntry`, `ShareStore` types
- `newShareStore`, `load`, `save`, `create`, `get`, `revoke`, `revokeAll`, `list` methods
- `handleShareCreate`, `handleShareList`, `handleShareRevoke`, `handleShareRevokeAll`
- `handleShareView` (the big one — renders the share page)
- `handleShareAsset`, `handleShareFile`
- `shareAssetAllowlist` map
- `rewriteShareImageURLs`

This is the second-largest single file (~440 lines).

- [ ] **Step 1: Create `shares.go`**

```go
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)
```

Body: lines 1337-1781 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 1337-1781.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test share flow end-to-end**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# Create a test share
TOKEN=$(curl -s -X POST "http://$IP:8080/api/shares/create" -H "Content-Type: application/json" \
  -d '{"vault":"pessoal","path":"Welcome.md","writable":false,"ttl":0,"label":"refactor-test"}' \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")
echo "Token: $TOKEN"
# Render the share view
curl -s "http://$IP:8080/share/$TOKEN" | grep -E '<base href|<title>' | head -5
# Revoke it
curl -s -X DELETE "http://$IP:8080/api/shares/revoke?token=$TOKEN" | head
```

Expected: token created; share view contains `<base href="$TOKEN/">` + `<title>`; revoke returns `{"status":"revoked"}`.

- [ ] **Step 5: Commit**

```bash
git add shares.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract shares.go (ShareStore + handlers + share-page renderer)

Pure code move. ShareEntry, ShareStore (newShareStore, load, save,
create, get, revoke, revokeAll, list), all 4 admin handlers, the
big handleShareView, plus handleShareAsset, handleShareFile, and
rewriteShareImageURLs.
Smoke-tested: create → render → revoke flow works end-to-end.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.8: Extract `trash.go` — trash naming + trash handlers

**Files:**
- Create: `trash.go`
- Modify: `main.go` (remove trash section)

Includes (current `main.go:2765-2935`):
- `trashSentinel` const
- `makeTrashName`, `decodeTrashName`, `legacyDecodeTrashName`
- `handleTrashList`, `handleTrashRestore`, `handleTrashEmpty`

- [ ] **Step 1: Create `trash.go`**

```go
package main

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)
```

Body: lines 2765-2935 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 2765-2935.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test trash listing**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/trash" | head -c 200
```

Expected: JSON array (empty or with trash entries).

- [ ] **Step 5: Commit**

```bash
git add trash.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract trash.go (naming + handlers)

Pure code move. makeTrashName, decodeTrashName, legacyDecodeTrashName
(VRTRASH_<base64>_<unix><ext> scheme + legacy fallback) plus the 3
HTTP handlers (list/restore/empty).
Smoke-tested: /api/trash returns expected shape.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.9: Extract `attachments.go` — attachment list + refcount

**Files:**
- Create: `attachments.go`
- Modify: `main.go` (remove attachments section)

Includes (current `main.go:2937-3118`):
- `AttachmentItem`, `AttachmentRef` types
- `imageExtensions` map var
- `handleAttachments`
- `countWithRefs` helper

- [ ] **Step 1: Create `attachments.go`**

```go
package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)
```

Body: lines 2937-3118 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 2937-3118.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/attachments?vault=pessoal" | head -c 300
```

Expected: JSON shape with `items` array.

- [ ] **Step 5: Commit**

```bash
git add attachments.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract attachments.go (list + refcount)

Pure code move. AttachmentItem/AttachmentRef types, imageExtensions
map, handleAttachments, countWithRefs.
Smoke-tested: /api/attachments returns items array.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.10: Extract `notes.go` — note CRUD + folder ops + getnote/putnote/upload

**Files:**
- Create: `notes.go`
- Modify: `main.go` (remove notes section)

Includes:
- `(s *server) handleNote` (line 1930-1944)
- `(s *server) handleCreateNote` (1945-1991)
- `(s *server) handleDeleteNote` (1992-2034)
- `(s *server) handleFolder` (2035-2047)
- `(s *server) handleRenameFolder` (2048-2117)
- `(s *server) handleCreateFolder` (2118-2151)
- `(s *server) handleDeleteFolder` (2152-2210)
- `(s *server) handleMove` (2211-2283)
- `(s *server) handleGetNote` (2284-2337)
- `(s *server) handlePutNote` (2338-2408)
- `(s *server) handleUpload` (2409-2511)
- `sanitizeFilename` (2512-2522)
- `(s *server) handleSearch` (2524-2557) — note: this is the HTTP handler that wraps `searchVault`; it lives here despite the name because it's a note-related endpoint
- `(s *server) handleResolve` (2558-2573)
- `(s *server) handleTemplates` (2574-2617)
- `(s *server) handleBacklinks` (2618-2635)
- Plus `saveNote` + `normalizeMarkdown` from lines 1089-1129 (originally in "Save" section)

- [ ] **Step 1: Create `notes.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)
```

Body: All sections listed above, in this order:
1. Lines 1089-1129 (`saveNote`, `normalizeMarkdown`)
2. Lines 1930-2635 (all the note handlers + sanitizeFilename + handleSearch + handleResolve + handleTemplates + handleBacklinks)

- [ ] **Step 2: Remove from `main.go`**

Delete:
- Lines 1089-1129 (saveNote, normalizeMarkdown)
- Lines 1930-2635 (all the handlers in this group)

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test note CRUD**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# Read
curl -s "http://$IP:8080/api/note?vault=pessoal&path=Welcome.md" | python3 -c "import sys,json; print('keys:', list(json.load(sys.stdin).keys()))"
# Create + delete a test note
curl -s -X POST "http://$IP:8080/api/note?vault=pessoal&path=__refactor_test.md" -H "Content-Type: text/plain" -d "# refactor test" | head
curl -s -X DELETE "http://$IP:8080/api/note?vault=pessoal&path=__refactor_test.md" | head
```

Expected: GET returns keys including `raw`, `html`, `frontmatter`, `mtime`; create returns 200; delete returns `{"movedTo":"..."}`.

- [ ] **Step 5: Commit**

```bash
git add notes.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract notes.go (CRUD + folder ops + saveNote)

Pure code move. All note CRUD handlers (handleNote/CreateNote/
DeleteNote/GetNote/PutNote), folder ops (handleFolder/CreateFolder/
RenameFolder/DeleteFolder/Move), upload + sanitization, search HTTP
handler, resolve, templates, backlinks, plus saveNote/normalizeMarkdown.
Smoke-tested: GET/POST/DELETE /api/note round-trip works.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.11: Extract `files.go` — buildTree + handleFile + handleVaultIcon + safePath/vaultPath

**Files:**
- Create: `files.go`
- Modify: `main.go`

Includes:
- `shouldSkip` (657-663)
- `buildTree` (665-727)
- `(s *server) handleFile` (1783-1814)
- `(s *server) handleVaultIcon` (1815-1842)
- `(s *server) vaultPath` (1844-1858)
- `(s *server) safePath` (1860-1882)

These all touch the filesystem layer / file lookups; grouping them keeps the path-traversal-protection (`safePath`) close to its callers in `handleFile` and `handleVaultIcon`.

- [ ] **Step 1: Create `files.go`**

```go
package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)
```

Body: paste the listed line ranges from current `main.go`, in order.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 657-727 (file tree section), 1783-1882 (file/vault-icon handlers + safePath/vaultPath).

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test file serving + tree**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/tree?vault=pessoal" | head -c 200
# safePath should reject ..
curl -s -o /dev/null -w "%{http_code}\n" "http://$IP:8080/api/file?vault=pessoal&path=../etc/passwd"
# vault icon
curl -sI "http://$IP:8080/api/vault-icon?vault=pessoal" | head -1
```

Expected: tree returns JSON, traversal returns 4xx (not 200), vault icon returns 200 or 204.

- [ ] **Step 5: Commit**

```bash
git add files.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract files.go (buildTree, handleFile, handleVaultIcon, safePath, vaultPath)

Pure code move. Filesystem-layer handlers grouped together so
safePath stays adjacent to its primary callers.
Smoke-tested: /api/tree returns expected shape; path traversal still
blocked; vault icons served.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.12: Extract `vaults.go` — handleVaults + handleTree + handleIndex + vaultOrder

**Files:**
- Create: `vaults.go`
- Modify: `main.go`

Includes:
- `vaultOrder` var (1884)
- `(s *server) handleVaults` (1886-1914)
- `(s *server) handleTree` (1915-1929)
- `(s *server) handleIndex` (3416-3424)

- [ ] **Step 1: Create `vaults.go`**

```go
package main

import (
	"net/http"
	"os"
	"sort"
)
```

Body: paste listed sections in order.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 1884-1929 and 3416-3424.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/vaults"
diff <(curl -s "http://$IP:8080/api/vaults") /tmp/vr-baseline/vaults.json
```

Expected: empty diff with baseline.

- [ ] **Step 5: Commit**

```bash
git add vaults.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract vaults.go (vault listing + tree + index)

Pure code move. handleVaults, handleTree, handleIndex, vaultOrder.
Smoke-tested: /api/vaults matches baseline byte-for-byte.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.13: Extract `stats.go` — VaultStat + StatsResponse + handleStats

**Files:**
- Create: `stats.go`
- Modify: `main.go`

Includes (lines 2636-2696):
- `VaultStat` struct
- `StatsResponse` struct
- `(s *server) handleStats`

- [ ] **Step 1: Create `stats.go`**

```go
package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)
```

Body: lines 2636-2696 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 2636-2696.

- [ ] **Step 3: Build + smoke test**

```bash
go build .
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/stats" | head -c 200
```

Expected: succeeds; stats JSON has `vaults` + `total_notes`.

- [ ] **Step 4: Commit**

```bash
git add stats.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract stats.go (vault stats handler)

Pure code move. VaultStat, StatsResponse, handleStats.
Smoke-tested: /api/stats returns expected shape.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.14: Extract `sync.go` — Syncthing status

**Files:**
- Create: `sync.go`
- Modify: `main.go`

Includes (lines 2698-2755):
- `SyncStatus` struct
- `syncHTTPClient` var
- `(s *server) handleSyncStatus`

- [ ] **Step 1: Create `sync.go`**

```go
package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)
```

Body: lines 2698-2755 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 2698-2755.

- [ ] **Step 3: Build**

```bash
go build .
```

Expected: succeeds.

- [ ] **Step 4: Smoke test**

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/sync-status" | head -c 200
```

Expected: JSON with `state` + `connected` fields (likely `state:unknown` if syncthing env vars aren't set).

- [ ] **Step 5: Commit**

```bash
git add sync.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract sync.go (Syncthing status handler)

Pure code move. SyncStatus, syncHTTPClient, handleSyncStatus.
Smoke-tested: /api/sync-status returns expected shape.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.15: Extract `tags.go` — tag aggregation handler

**Files:**
- Create: `tags.go`
- Modify: `main.go`

Includes (lines 3120-3231):
- `(s *server) handleTags`

- [ ] **Step 1: Create `tags.go`**

```go
package main

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)
```

Body: lines 3120-3231 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 3120-3231.

- [ ] **Step 3: Build + smoke test**

```bash
go build .
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/tags" | head -c 300
```

Expected: succeeds; JSON array of tags with `tag`, `count`, `vaults`.

- [ ] **Step 4: Commit**

```bash
git add tags.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract tags.go (tag aggregation handler)

Pure code move. handleTags walks all vaults, aggregates frontmatter
tags + tag fields.
Smoke-tested: /api/tags returns sorted aggregated tag list.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.16: Extract `graph.go` — graph data builder

**Files:**
- Create: `graph.go`
- Modify: `main.go`

Includes (lines 3233-3390):
- `(s *server) handleGraph` — handles `?center=`, `?folder=`, `?vault=`, `?depth=` params

- [ ] **Step 1: Create `graph.go`**

```go
package main

import (
	"net/http"
	"strconv"
	"strings"
)
```

Body: lines 3233-3390 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 3233-3390.

- [ ] **Step 3: Build + smoke test**

```bash
go build .
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s "http://$IP:8080/api/graph?vault=pessoal" | python3 -c "import sys,json; d=json.load(sys.stdin); print('nodes:', len(d.get('nodes', [])), 'edges:', len(d.get('edges', [])))"
```

Expected: succeeds; `nodes`/`edges` counts non-zero.

- [ ] **Step 4: Commit**

```bash
git add graph.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract graph.go (graph data builder)

Pure code move. handleGraph supports center=, folder=, vault=, depth=
scopes for the cytoscape graph view.
Smoke-tested: /api/graph?vault=… returns nodes + edges.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.17: Extract `webdav.go` — read-only WebDAV mount

**Files:**
- Create: `webdav.go`
- Modify: `main.go`

Includes (lines 3392-3414):
- `(s *server) newWebDAVHandler`

- [ ] **Step 1: Create `webdav.go`**

```go
package main

import (
	"net/http"

	"golang.org/x/net/webdav"
)
```

Body: lines 3392-3414 from current `main.go`.

- [ ] **Step 2: Remove from `main.go`**

Delete lines 3392-3414.

- [ ] **Step 3: Build + smoke test**

```bash
go build .
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# WebDAV PROPFIND on the root
curl -s -X PROPFIND "http://$IP:8080/webdav/" -H "Depth: 1" | head -c 500
```

Expected: succeeds; XML response with `<D:multistatus>`.

- [ ] **Step 4: Commit**

```bash
git add webdav.go main.go
git -c commit.gpgsign=false commit -m "refactor: extract webdav.go (read-only WebDAV mount)

Pure code move. newWebDAVHandler, method allowlist (GET/HEAD/OPTIONS/
PROPFIND).
Smoke-tested: PROPFIND /webdav/ returns XML.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.18: Final review — `main.go` is now thin

After all 17 prior tasks, `main.go` should contain:
- `package main`
- `import (...)` (likely just `flag`, `log`, `net/http`, `time`)
- `func main()` — flag parsing, server initialization, mux setup, ListenAndServe

**Files:**
- Modify: `main.go` (clean up imports + verify thinness)

- [ ] **Step 1: Verify size**

```bash
wc -l main.go
```

Expected: under 200 lines.

- [ ] **Step 2: Tidy all imports**

```bash
gofmt -s -w *.go
which goimports && goimports -w *.go
```

- [ ] **Step 3: Final integrated build + full smoke test**

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')

# Compare every captured baseline endpoint
diff <(curl -s "http://$IP:8080/api/vaults")            /tmp/vr-baseline/vaults.json
diff <(curl -s "http://$IP:8080/api/writable-paths")    /tmp/vr-baseline/writable.json
diff <(curl -s "http://$IP:8080/api/stats")             /tmp/vr-baseline/stats.json

# Spot-check additional endpoints
curl -s "http://$IP:8080/api/note?vault=pessoal&path=Welcome.md" | python3 -c "import sys,json;print('OK' if 'html' in json.load(sys.stdin) else 'FAIL')"
curl -s "http://$IP:8080/api/search?q=tag:work" | python3 -c "import sys,json;d=json.load(sys.stdin);print('OK' if isinstance(d,list) else 'FAIL')"
curl -s "http://$IP:8080/api/graph?vault=pessoal" | python3 -c "import sys,json;d=json.load(sys.stdin);print('OK' if 'nodes' in d else 'FAIL')"
```

Expected: empty diffs against all 3 baseline files; all spot-checks print `OK`.

- [ ] **Step 4: Headless-browser smoke test**

Following CLAUDE.md verification pattern:

```bash
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
echo "Open http://$IP:8080/ in playwright-browser to verify SPA loads"
```

Use the playwright-browser tools to:
1. Navigate to `http://$IP:8080/`
2. Verify console has zero errors
3. Open a note, switch to edit mode, save
4. Open the graph view
5. Click an image in a folder (verify viewer)
6. Click `.btn-share` (verify popover)

If any step fails, the relevant Stage 1 task likely broke something. Inspect the diff for that task.

- [ ] **Step 5: Update CHANGELOG**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
# Edit CHANGELOG.md, add entry at the top:
```

```markdown
## YYYY-MM-DD — Stage 1: backend split

`main.go` (3,588 lines) split into ~19 focused `package main` files
(`http.go`, `markdown.go`, `index.go`, `data.go`, `search.go`,
`admin.go`, `shares.go`, `trash.go`, `attachments.go`, `notes.go`,
`files.go`, `vaults.go`, `stats.go`, `sync.go`, `tags.go`, `graph.go`,
`webdav.go`, `main.go`). Pure organisational change — same compiler
input due to Go's package scope, byte-identical runtime. Each file
now answers one question (where does shares live? share.go. Where's
markdown rendering? markdown.go. Etc.) Adding a feature now usually
touches one file ≤500 lines instead of scrolling main.go.
```

- [ ] **Step 6: Update CLAUDE.md "Where things live" map**

In `CLAUDE.md`, replace the existing "Where things live" table with:

```markdown
| Want to change… | Look in… |
|---|---|
| A route | `main.go` (mux setup) |
| Markdown rendering / wikilinks / callouts | `markdown.go` |
| Note CRUD / save / upload | `notes.go` |
| Search ranking + operators | `search.go` |
| The wikilink index | `index.go` (methods) + `data.go` (NoteIndex type) |
| Share creation / view / asset / file | `shares.go` |
| Trash naming / handlers | `trash.go` |
| Attachment listing / refcount | `attachments.go` |
| Graph data builder | `graph.go` |
| Tag aggregation | `tags.go` |
| Vault listing / tree | `vaults.go` + `files.go` |
| File serving + safePath / vaultPath | `files.go` |
| Stats endpoint | `stats.go` |
| Syncthing status | `sync.go` |
| WebDAV mount | `webdav.go` |
| Admin token + config + writable paths | `admin.go` |
| Gzip + rate limit + JSON helpers | `http.go` |
| Shared types + regexes + goldmark init + embed.FS | `data.go` |
| Alpine state | `static/index.html` ~L1010 (`function vaultApp()`) — until Stage 2 |
```

- [ ] **Step 7: Commit + merge**

```bash
git add CHANGELOG.md CLAUDE.md
git -c commit.gpgsign=false commit -m "docs: update CHANGELOG + CLAUDE.md after Stage 1 backend split

main.go is now ~150 lines (CLI + main()); 18 sibling .go files hold
the rest. Each file has one clear responsibility. Pure organisational
change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"

# Merge to main
git checkout main
git merge --no-ff refactor/backend-split -m "refactor: backend modularization (Stage 1)

Squashes 18 incremental refactor commits. main.go went from 3,588
lines → ~150; 18 sibling .go files in package main now hold the rest,
each with one clear responsibility. Runtime byte-identical."
git push origin main
```

---

# Stage 2 — Frontend split

**Goal:** Extract the inline `<script>` (`vaultApp()` factory, ~3,550 lines) from `static/index.html` into ~24 native ES modules under `static/js/`. Mixin pattern: each feature module exports an object with state defaults + methods; `app.js` assembles them via `Object.assign({}, ...mixins)` into the factory.

**Approach:** Single PR (one big commit) per the spec — incremental approach would force interim states with cross-mixin calls broken. Validation done end-to-end via the headless browser smoke test.

**Pre-flight for Stage 2:**

- [ ] **Step S2.0a: Confirm Stage 1 is merged + clean**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
git status                                  # clean
git log --oneline -5                        # Stage 1 merge commit visible
ls *.go | wc -l                             # ~19 files
wc -l main.go                               # ~150
```

Expected: Stage 1 is in main, working tree clean.

- [ ] **Step S2.0b: Create Stage 2 branch**

```bash
git checkout -b refactor/frontend-modules
```

- [ ] **Step S2.0c: Capture frontend baseline**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
wc -l static/index.html static/style.css > /tmp/vr-baseline-frontend-sizes.txt
# Save the inline script content for a sanity reference
sed -n '1501,5048p' static/index.html > /tmp/vr-baseline-vaultapp.js
node --check /tmp/vr-baseline-vaultapp.js 2>&1 | head -3 || echo "(expected: parses OK)"
cat /tmp/vr-baseline-frontend-sizes.txt
```

Expected: `index.html` ~5051 lines; `vaultApp` script extracted; `node --check` passes.

---

### Task 2.1: Create `static/js/` directory + helper script

**Files:**
- Create: `static/js/` directory
- Create: `tools/check-mixin-collisions.js` — Node script to detect duplicate state-default keys across mixins

- [ ] **Step 1: Create directory layout**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
mkdir -p static/js/core static/js/features static/js/lib tools
```

- [ ] **Step 2: Write the collision-detection script**

Create `tools/check-mixin-collisions.js`:

```js
#!/usr/bin/env node
// Verify that no public state-default key is declared in more than one
// mixin file. The build is Object.assign({}, ...mixins) — later-spread
// silently wins, which would be a real bug. Run this in CI.
//
// Strategy: for each .js file under static/js/features and static/js/core,
// parse the exported object literal(s), collect their top-level keys
// (state defaults and method names alike — both must be unique), and
// fail if any key appears in two mixins.

const fs = require('fs')
const path = require('path')

const ROOT = path.join(__dirname, '..', 'static', 'js')

function walk(dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.isDirectory()) walk(path.join(dir, e.name), out)
    else if (e.name.endsWith('.js') && !e.name.startsWith('.')) {
      out.push(path.join(dir, e.name))
    }
  }
  return out
}

// Crude top-level key extractor: finds `export const FOO = { ... }` and
// pulls the keys at depth 1. Good enough for our hand-written mixins.
function topLevelKeys(src) {
  const m = src.match(/export\s+const\s+\w+\s*=\s*\{([\s\S]*)\}\s*$/m)
  if (!m) return []
  const body = m[1]
  // Match either bare key, "quoted key", or method shorthand foo() / foo: ...
  const keys = new Set()
  let depth = 0
  for (let i = 0; i < body.length; i++) {
    const c = body[i]
    if (c === '{' || c === '(' || c === '[') depth++
    else if (c === '}' || c === ')' || c === ']') depth--
    if (depth !== 0) continue
    // Look for an identifier or "string" followed by ':' or '(' at depth 0
    const slice = body.slice(i)
    const km = slice.match(/^\s*(?:async\s+)?(?:_?[a-zA-Z][a-zA-Z0-9_]*)/)
    if (km) {
      const after = slice.slice(km[0].length).match(/^\s*[:(]/)
      if (after) {
        const name = km[0].replace(/^async\s+/, '').trim()
        keys.add(name)
        i += km[0].length - 1
      }
    }
  }
  return [...keys]
}

const files = walk(ROOT).filter(f => !f.includes('/lib/'))
const owners = new Map() // key -> [files]
let total = 0
for (const f of files) {
  const src = fs.readFileSync(f, 'utf8')
  const keys = topLevelKeys(src)
  for (const k of keys) {
    if (!owners.has(k)) owners.set(k, [])
    owners.get(k).push(path.relative(ROOT, f))
    total++
  }
}

const collisions = [...owners.entries()].filter(([, files]) => files.length > 1)
if (collisions.length === 0) {
  console.log(`OK — ${total} keys across ${files.length} mixin files; no collisions.`)
  process.exit(0)
}
console.error(`FAIL — ${collisions.length} key collision(s):`)
for (const [k, fs] of collisions) {
  console.error(`  "${k}" declared in: ${fs.join(', ')}`)
}
process.exit(1)
```

- [ ] **Step 3: Smoke-test the collision script**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
node tools/check-mixin-collisions.js
```

Expected: prints `OK — 0 keys across 0 mixin files; no collisions.` (no mixins yet).

- [ ] **Step 4: Commit**

```bash
git add static/js tools/check-mixin-collisions.js
git -c commit.gpgsign=false commit -m "chore(refactor): scaffold static/js/ + collision detector

Empty static/js/{core,features,lib} dirs + tools/check-mixin-collisions.js
that walks the mixin tree and fails CI if two mixins declare the same
top-level key.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2.2: Move `__cmAPI` to `static/js/lib/codemirror-bridge.js`

The CodeMirror wrapper IIFE (lines 1431-1499 of `index.html`, ~70 lines) is non-module — it bridges to the bundled `codemirror.bundle.js` (a non-module global) and sets `window.__cmAPI`. It moves to a separate plain-script file so we can keep the inline `<script>` block in `index.html` shrinking.

**Files:**
- Create: `static/js/lib/codemirror-bridge.js`
- Modify: `static/index.html` (replace inline `<script>` block at L1431-1499 with `<script src="/js/lib/codemirror-bridge.js"></script>`)

- [ ] **Step 1: Create `static/js/lib/codemirror-bridge.js`**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
sed -n '1432,1498p' static/index.html > static/js/lib/codemirror-bridge.js
# Verify
head -3 static/js/lib/codemirror-bridge.js
node --check static/js/lib/codemirror-bridge.js && echo OK
```

(The file's content is the existing IIFE body — no changes needed.)

Expected: parses OK.

- [ ] **Step 2: Replace the inline block in `index.html`**

In `static/index.html`, find the block at L1430-1499:

```html
<script src="/codemirror.bundle.js"></script>
<script>
const { EditorView, keymap, lineNumbers } = CM
... (~70 lines)
</script>
```

Replace lines 1431-1499 with:

```html
<script src="/codemirror.bundle.js"></script>
<script src="/js/lib/codemirror-bridge.js"></script>
```

- [ ] **Step 3: Build + smoke test**

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
# Verify the bridge file is served
curl -sI "http://$IP:8080/js/lib/codemirror-bridge.js" | head -1
```

Expected: container starts; bridge JS returns 200.

Use the playwright-browser tools to navigate to `http://$IP:8080/` and:
1. Open a writable note (e.g. `pessoal/Welcome.md`)
2. Switch to edit mode (toolbar pencil, or press `E`)
3. Verify the editor renders, can type, can save

Expected: editor loads + saves correctly.

- [ ] **Step 4: Commit**

```bash
git add static/js/lib/codemirror-bridge.js static/index.html
git -c commit.gpgsign=false commit -m "refactor(frontend): extract codemirror-bridge.js

The 70-line __cmAPI IIFE moves out of the inline <script> in
index.html to its own non-module file. This is a non-module
because it bridges to the bundled codemirror.bundle.js which is
itself a non-module global.
Smoke-tested: edit mode loads + saves correctly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2.3: Stub `static/js/app.js` — empty mixin assembler

This task creates the entry point with no mixins yet. After this task, `index.html` has TWO definitions of `vaultApp`: the original inline one (still active) and the module-loaded one (overridden). Future tasks will progressively move methods from inline → modules; the inline definition will shrink to nothing by Task 2.30.

**Strategy:** rather than moving everything in one giant commit, we use a **progressive override pattern**:
- `app.js` defines `window.vaultApp` to start as a copy of the original.
- Each feature task moves a thematic block of methods from the inline factory into a mixin module, AND registers the mixin in `app.js`.
- After every task, the running `vaultApp` is `Object.assign({}, originalInlineDefaults, ...mixins)`. As long as collision detection passes, the override semantics are deterministic.
- Final task removes the inline definition entirely.

This keeps each commit small, individually verifiable, and revertable — same shape as Stage 1.

**Files:**
- Create: `static/js/app.js`
- Modify: `static/index.html` (add `<script type="module" src="/js/app.js"></script>` AFTER the existing inline `vaultApp` `<script>` block)

- [ ] **Step 1: Write the stub**

Create `static/js/app.js`:

```js
// VaultReader frontend entry point.
//
// Mixin-based assembly: each feature module under ./features/ exports
// a single object containing state defaults + methods. We Object.assign
// them all into the result of vaultApp() so Alpine sees one merged
// component. Object.assign happens before Alpine wraps the result in
// a Proxy, so reactivity is unchanged from the inline-script version.
//
// Collision rule: no two mixins may declare the same top-level key.
// Verified by tools/check-mixin-collisions.js in CI.
//
// Load order: this script must define window.vaultApp BEFORE Alpine
// evaluates the x-data binding. Native ES modules are implicitly
// deferred and execute after parsing in source-order; both this and
// alpine.min.js complete before DOMContentLoaded.

const mixins = []  // populated by future tasks via mixins.push(...)

// Capture the existing inline vaultApp() factory so we can layer mixins
// on top of it without breaking what's already working. This is the key
// trick that lets us migrate one feature at a time: the original
// monolithic factory keeps providing every method until we've moved it
// into a mixin, at which point the mixin shadows the inline copy.
const _originalVaultApp = window.vaultApp

window.vaultApp = function () {
  const base = _originalVaultApp ? _originalVaultApp() : {}
  return Object.assign(base, ...mixins)
}
```

- [ ] **Step 2: Wire it into `index.html`**

In `static/index.html`, at the very end of the `<script>` block that defines `vaultApp` (which currently ends with `</script>` at line ~5048), ADD AFTER it (NOT replace):

```html
<script type="module" src="/js/app.js"></script>
```

Concretely: find `</script>` followed by Alpine's `<script defer src="/alpine.min.js"></script>`. Insert the new `<script type="module">` line BETWEEN them.

This ordering is critical — `app.js` runs after the inline script (so it sees `window.vaultApp`) but before Alpine init (because both are deferred and source-ordered).

- [ ] **Step 3: Build + smoke test**

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -sI "http://$IP:8080/js/app.js" | head -1
```

Expected: 200 OK.

Use playwright-browser:
1. Navigate to `http://$IP:8080/`
2. Open DevTools console
3. Type `typeof window.vaultApp` → should be `'function'`
4. Type `window.vaultApp().activeVault` → should be `''` (default state)
5. Verify the SPA loads and behaves identically to before

Expected: zero functional change. The factory is now `Object.assign(originalDefaults, ...[])` which equals the original.

- [ ] **Step 4: Commit**

```bash
git add static/js/app.js static/index.html
git -c commit.gpgsign=false commit -m "refactor(frontend): scaffold app.js with progressive-override pattern

Entry-point module that wraps the existing inline vaultApp() factory
and Object.assigns mixins on top. With zero mixins registered the
result is byte-identical to the inline version.

Future feature tasks will populate the mixins array. Final task will
remove the inline vaultApp definition entirely.

Smoke-tested: SPA loads identically; window.vaultApp typeof === 'function'.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2.4: Migrate `core/util.js` — pure helpers

The lowest-risk first migration: stateless, no `this` references. These functions don't depend on anything in the Alpine component.

**Files:**
- Create: `static/js/core/util.js`
- Modify: `static/js/app.js` (import + push)
- Modify: `static/index.html` (delete the moved methods from the inline `vaultApp()` factory)

**Methods migrating:**
- `_wordCount` (~L2900)
- `_countOutgoingLinks` (~L2909)
- `_formatSize` (~L2885)
- `_relativeTime` (~L2890)
- `_stripFrontmatter` (~L3640)

Note: these are referenced as `this._wordCount(...)` etc. by other methods. Mixin-pattern preserves `this` semantics because Object.assign keeps them as methods on the merged object. ✓

- [ ] **Step 1: Create `static/js/core/util.js`**

```js
// Pure helpers used across many mixins. Stateless — no this.foo
// references. Exported as a mixin object so they end up callable as
// this.<name>(...) on the merged Alpine component, matching the
// existing call sites without any rewrite.

export const utilMixin = {
  _wordCount(raw) {
    if (!raw) return 0
    return raw.trim().split(/\s+/).filter(Boolean).length
  },

  _countOutgoingLinks(raw) {
    if (!raw) return 0
    const m = raw.match(/\[\[[^\]|]+(?:\|[^\]]+)?\]\]/g)
    return m ? m.length : 0
  },

  _formatSize(bytes) {
    if (!bytes) return '0 B'
    if (bytes < 1024) return bytes + ' B'
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
    return (bytes / 1024 / 1024).toFixed(1) + ' MB'
  },

  _relativeTime(unix) {
    if (!unix) return ''
    const now = Date.now() / 1000
    const diff = now - unix
    if (diff < 60) return 'just now'
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago'
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago'
    if (diff < 30 * 86400) return Math.floor(diff / 86400) + 'd ago'
    return new Date(unix * 1000).toLocaleDateString()
  },

  _stripFrontmatter(raw) {
    if (!raw.startsWith('---\n') && !raw.startsWith('---\r\n')) return raw
    const rest = raw.slice(raw.startsWith('---\r\n') ? 5 : 4)
    const end = rest.indexOf('\n---')
    if (end < 0) return raw
    let body = rest.slice(end + 4)
    if (body.startsWith('\r\n')) body = body.slice(2)
    else if (body.startsWith('\n')) body = body.slice(1)
    return body
  },
}
```

(Verify the bodies match the current inline implementations exactly. If any differs, use the inline version.)

- [ ] **Step 2: Wire into `app.js`**

```js
// At the top of app.js:
import { utilMixin } from './core/util.js'

const mixins = [utilMixin]
```

- [ ] **Step 3: Delete migrated methods from inline `vaultApp` in `index.html`**

In `static/index.html`, find each of these 5 methods inside the `vaultApp()` factory and DELETE them:
- `_wordCount(raw) { ... }` block
- `_countOutgoingLinks(raw) { ... }` block
- `_formatSize(bytes) { ... }` block
- `_relativeTime(unix) { ... }` block
- `_stripFrontmatter(raw) { ... }` block

Be careful with trailing commas: each method is followed by `,` separating it from the next entry in the `vaultApp() { return { ... } }` object literal. Remove the trailing comma along with the method.

- [ ] **Step 4: JS syntax check**

```bash
cd /home/joao/docker/stacks/office/images/vaultreader
node -e "
const fs = require('fs');
const html = fs.readFileSync('static/index.html', 'utf8');
const idx = html.lastIndexOf('<script>');
const end = html.lastIndexOf('</script>');
fs.writeFileSync('/tmp/vr-check.js', html.substring(idx + 8, end));
" && node --check /tmp/vr-check.js && echo "Inline JS OK"
node --check static/js/app.js && echo "app.js OK"
node --check static/js/core/util.js && echo "util.js OK"
node tools/check-mixin-collisions.js
```

Expected: all 4 lines print OK / "no collisions."

- [ ] **Step 5: Build + browser smoke test**

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
```

Use playwright-browser:
1. Navigate to `http://$IP:8080/`
2. Open a note with frontmatter and content
3. Open the frontmatter+stats panel — verify word-count / outgoing-link-count / size / mtime all display correctly (these all call the migrated helpers)
4. Console: zero errors

Expected: indistinguishable from before.

- [ ] **Step 6: Commit**

```bash
git add static/js/core/util.js static/js/app.js static/index.html
git -c commit.gpgsign=false commit -m "refactor(frontend): migrate util.js (5 pure helpers)

_wordCount, _countOutgoingLinks, _formatSize, _relativeTime,
_stripFrontmatter — all stateless, no this. references — moved out of
the inline vaultApp() factory into static/js/core/util.js.
Smoke-tested: frontmatter+stats panel renders correct counts/sizes.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2.5: Migrate `features/copy-paste.js`

A self-contained feature module: clipboard helpers + state flags. Verified to work end-to-end already in recent feature work.

**Files:**
- Create: `static/js/features/copy-paste.js`
- Modify: `static/js/app.js` (import + push)
- Modify: `static/index.html` (delete migrated methods + state)

**Methods + state migrating:**
- State: `copyLinkDone`, `copyPathDone`, `copyBodyDone`
- Methods: `copyNoteLink`, `copyNoteBody`, `copyNotePath`, `pasteAppendToNote`

- [ ] **Step 1: Create `static/js/features/copy-paste.js`**

```js
// Toolbar copy-and-paste helpers: copy wikilink, copy body (no FM),
// copy path (vault/path/note.md), paste-append (clipboard → end of
// note → undo toast).

export const copyPasteMixin = {
  copyLinkDone: false,
  copyPathDone: false,
  copyBodyDone: false,

  async copyNoteLink() {
    const name = this.activePath.split('/').pop().replace(/\.md$/, '')
    const link = '[[' + name + ']]'
    try {
      await navigator.clipboard.writeText(link)
    } catch(e) {
      const ta = document.createElement('textarea')
      ta.value = link
      ta.style.position = 'fixed'; ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.focus(); ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    this.copyLinkDone = true
    setTimeout(() => { this.copyLinkDone = false }, 2000)
  },

  async copyNoteBody() {
    if (!this.activePath) return
    const body = this._stripFrontmatter(this.noteRaw || '')
    try {
      await navigator.clipboard.writeText(body)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = body
      ta.style.position = 'fixed'; ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.focus(); ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    this.copyBodyDone = true
    setTimeout(() => { this.copyBodyDone = false }, 2000)
  },

  async copyNotePath() {
    let path
    if (this.activePath) {
      path = this.activeVault + '/' + this.activePath
    } else if (this.activeVault && this.cwd) {
      path = this.activeVault + '/' + this.cwd
    } else {
      path = this.activeVault || ''
    }
    try {
      await navigator.clipboard.writeText(path)
    } catch(e) {
      const ta = document.createElement('textarea')
      ta.value = path
      ta.style.position = 'fixed'; ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.focus(); ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    this.copyPathDone = true
    setTimeout(() => { this.copyPathDone = false }, 2000)
  },

  async pasteAppendToNote() {
    if (!this.activePath) return
    if (!this.isWritable(this.activeVault, this.activePath)) {
      this.modal = { open: true, title: 'Read-only path',
        body: 'This note is in a read-only path. Add it to rw_paths in admin settings to enable writes.',
        confirmOnly: true, confirmLabel: 'OK' }
      return
    }
    let pasted = ''
    try {
      pasted = await navigator.clipboard.readText()
    } catch(e) {
      this.modal = { open: true, title: 'Clipboard unavailable',
        body: 'Browser refused clipboard access. Click somewhere in the page first, then try again.',
        confirmOnly: true, confirmLabel: 'OK' }
      return
    }
    pasted = (pasted || '').trim()
    if (!pasted) return
    const before = this.noteRaw || ''
    const sep = before.endsWith('\n\n') ? '' : (before.endsWith('\n') ? '\n' : '\n\n')
    const after = before + sep + pasted + '\n'
    this.noteRaw = after
    try {
      await this._saveNote(after)
      const r = await fetch('/api/note?vault=' + encodeURIComponent(this.activeVault) +
        '&path=' + encodeURIComponent(this.activePath))
      if (r.ok) {
        const d = await r.json()
        this.noteHtml = d.html || ''
        this.noteMTime = d.mtime || 0
        this.$nextTick(() => { this.rerenderMermaid(); this.rerenderMath(); this.decorateCodeBlocks() })
      }
    } catch(e) {
      console.error('paste-append save:', e)
      this.noteRaw = before
      this.modal = { open: true, title: 'Append failed', body: String(e),
        confirmOnly: true, confirmLabel: 'OK' }
      return
    }
    const vault = this.activeVault, path = this.activePath
    this.showActionToast('Pasted ' + pasted.length + ' chars · appended', async () => {
      try {
        await this._saveNote(before, { vault, path })
        if (this.activeVault === vault && this.activePath === path) {
          this.noteRaw = before
          const r2 = await fetch('/api/note?vault=' + encodeURIComponent(vault) +
            '&path=' + encodeURIComponent(path))
          if (r2.ok) {
            const d2 = await r2.json()
            this.noteHtml = d2.html || ''
            this.noteMTime = d2.mtime || 0
            this.$nextTick(() => { this.rerenderMermaid(); this.rerenderMath(); this.decorateCodeBlocks() })
          }
        }
      } catch(e) { console.error('undo paste-append:', e) }
    })
  },
}
```

- [ ] **Step 2: Wire into `app.js`**

```js
import { utilMixin } from './core/util.js'
import { copyPasteMixin } from './features/copy-paste.js'

const mixins = [utilMixin, copyPasteMixin]
```

- [ ] **Step 3: Delete from inline `vaultApp()`**

Remove from `static/index.html`:
- The 3 state lines: `copyLinkDone: false,`, `copyPathDone: false,`, `copyBodyDone: false,`
- The 4 method blocks: `async copyNoteLink() {...}`, `async copyNoteBody() {...}`, `async copyNotePath() {...}`, `async pasteAppendToNote() {...}`

- [ ] **Step 4: JS check + collision check**

```bash
node -e "/* extract inline JS */ const fs = require('fs'); const html = fs.readFileSync('static/index.html', 'utf8'); const idx = html.lastIndexOf('<script>'); const end = html.lastIndexOf('</script>'); fs.writeFileSync('/tmp/vr-check.js', html.substring(idx + 8, end));" && node --check /tmp/vr-check.js
node --check static/js/features/copy-paste.js
node tools/check-mixin-collisions.js
```

Expected: all OK.

- [ ] **Step 5: Build + browser smoke test**

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
```

Use playwright-browser:
1. Open a note in `pessoal` (writable vault)
2. Click `.btn-copylink` — verify `.copied` class flips on
3. Click `.btn-copybody` — verify `.copied` class flips on
4. Click `.tb-copypath` — verify `.copied` class flips on
5. Click `.btn-pasteappend` (clipboard contains "test paste") — verify undo toast appears, click Undo, verify content reverted

Expected: all 4 buttons function exactly as before.

- [ ] **Step 6: Commit**

```bash
git add static/js/features/copy-paste.js static/js/app.js static/index.html
git -c commit.gpgsign=false commit -m "refactor(frontend): migrate features/copy-paste.js

3 state flags (copyLinkDone, copyPathDone, copyBodyDone) + 4 methods
(copyNoteLink, copyNoteBody, copyNotePath, pasteAppendToNote) moved
to a mixin module. Calls through this.<method> from elsewhere
unchanged.
Smoke-tested: all 4 toolbar buttons work, paste-append undo restores.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2.6 — 2.27: Migrate remaining feature mixins (one task each)

For each remaining feature, follow the same 6-step pattern as Task 2.5:
1. Create the mixin file
2. Wire into `app.js`
3. Delete from inline factory
4. JS check + collision check
5. Build + browser smoke test (with feature-specific verification)
6. Commit

The mapping is fixed by the spec; here is the full task list with target file, line ranges in the current `index.html`, and feature-specific smoke tests:

| Task | Module | Source line range | Smoke test |
|---|---|---|---|
| 2.6 | `features/undo-toasts.js` | 2378-2431 (`_toastSeq`, `showUndoToast`, `dismissToast`, `undoToast`, `showActionToast`) | Delete a note → toast appears → Undo restores |
| 2.7 | `features/viewer.js` | 2509-2614 (`openItem`, `_viewerKindFor`, `fileKind`, `fileIconSvg`, `openViewer`, `closeViewer`) + state (`viewerKind`, `viewerSize`, `viewerMTime`, `viewerText`) | Click image in sidebar → viewer renders |
| 2.8 | `features/render.js` | 2693-2804 (`rerenderPreview`, `rerenderMath`, `rerenderMermaid`, `decorateCodeBlocks`, `_copyCodeBlock`, `handlePreviewClick`, `handlePreviewContextMenu`) | Open note with code blocks → copy button works; mermaid renders; right-click on wikilink → context menu |
| 2.9 | `features/outline.js` | 3375-3423 (`outlineItems`, `scrollToHeading`, `_initOutlineScrollSpy`) + state (`outlineActiveId`) | Open long note → outline rail shows headings |
| 2.10 | `features/notes-io.js` | 2616-2667 (`openNote`) + 3425-3534 (`scheduleSave`, `_saveNote`, `saveNow`, `_showConflictModal`, `takeTheirs`, `saveStatusText`) + state (`isDirty`, `saveStatus`, `saveTimer`, `noteMTime`, `noteSize`) | Edit + save a note → mtime updates; Edit two tabs simultaneously → conflict modal |
| 2.11 | `features/copy-paste.js` (already done in Task 2.5) | — | — |
| 2.12 | `features/templates.js` | 2189-2260 (`openTemplatePicker`, `_expandTemplate`, `useTemplate`) + state (`tplPicker`) | Toolbar `+ New → From template` → picker shows; insert template |
| 2.13 | `features/breadcrumb.js` | 3796-3818 (`activeBreadcrumbs`, `navigateToSegment`, `sbContext`) | Verify breadcrumb segments render + click navigates |
| 2.14 | `features/sidebar.js` | 1964-2060 (`enterDir`, `flattenTree`, `currentDirs`, `currentFiles`, `sortedFolderItems`, `fvMeta`) + 2069-2086 (`cwdSegments`, `cwdUpTo`, `goUp`) + 2262-2376 (bulk select + drag-drop: `isSelected`, `toggleSelect`, `rangeSelect`, `handleSidebarRowClick`, `clearSelection`, `bulkDelete`, `bulkMove`, `_parentDirOfCwd`, `handleSidebarDragStart`, `handleSidebarDrop`, `revealInSidebar`) + 4173-4202 (sidebar resize) + state (`bulkSelected`, `_lastSelectedPath`, `cwd`) | Drag a note → drop in folder; bulk-select 3 → bulk delete; resize sidebar |
| 2.15 | `features/search.js` | 3820-3858 (`doSearch`, `highlightMatch`) + 2915-2951 (saved searches: `_persistSavedSearches`, `saveCurrentSearch`, `runSavedSearch`, `deleteSavedSearch`) + state (`searchQuery`, `searchResults`, `searchFocus`, `searchOpen`, `savedSearches`) + chipSearch (1602-1618) | Ctrl+K → search → operators work; saved searches persist |
| 2.16 | `features/tags.js` | 2953-2979 (`openTags`, `closeTags`, `filteredTags`, `selectTag`) + state (`tagsOpen`, `tagsLoading`, `tagsList`, `tagsFilter`) | Open tags pane → click tag → filtered search opens |
| 2.17 | `features/graph.js` | 2981-3373 (the big one: `openGraph`, `openGraphFromCurrentNote`, `openGraphSmart`, `openGraphFromCurrentFolder`, `setGraphScopeWholeVault`, `setGraphScopeAllVaults`, `expandGraphDepth`, `contractGraphDepth`, `graphScopeLabel`, `closeGraph`, `_graphLayoutOptions`, `renderGraph`) + state (`graphOpen`, `graphLoading`, `graphVault`, `graphFolder`, `graphCenter`, `graphDepth`, `_cy`) | Open graph → drag node → simulation reflows; expand depth |
| 2.18 | `features/editor.js` | 4204-4456 (toolbar: `tbWrap`, `tbHeading`, `tbLinePrefix`, `tbLink`, `tbWikilink`, `tbTable`, `tbInsertMermaid`; mermaid menu state) + 4337-4456 (paste/drop image: `handleEditorPaste`, `handleEditorDrop`, `uploadImage`, `uploadImageToPreview`, `handlePreviewDrop`, `_uploadAndAppendEmbed`) + 4458-4524 (wikilink autocomplete: `_attachAutocomplete`, `_searchForAutocomplete`, `_completeWikilink`, `_renderWikiPopup`, `_advanceWikiPopup`, `closeWikiPopup`, `maybeOpenWikiPopup`) + state (`mermaidMenuOpen`, `wikiPop`) | Edit mode → click bold → wraps selection; type `[[` → autocomplete shows; paste image → uploads + inserts |
| 2.19 | `features/shares.js` | 4526-4671 (full share system: modal + popover + create + copy + revoke + bulk: `openShareModal`, `createShare`, `copyShareLink`, `loadActiveShares`, `revokeShare`, `revokeAllShares`, `shareExpiry`, `noteShares`, `canWriteCurrent`, `toggleSharePopover`, `quickShare`, `copyShareLinkFor`, `revokeShareFromPop`) + state (`shareModal`, `activeShares`, `sharePopoverOpen`, `sharePopX`, `sharePopY`, `sharePopCopied`) | Click share button → popover; quick-share read-only; revoke; bulk-revoke from settings |
| 2.20 | `features/settings.js` | 4166-4172 (`openSettings`) + parts of init that load settings tabs + state (`settingsOpen`, `settingsTab`) | Open settings → switch tabs |
| 2.21 | `features/admin.js` | 4707-4930 (admin: `fetchAdminConfig`, `saveAdminConfig`, `restartServer`, `loadWritablePaths`) + state (`adminConfig`) | Settings → Admin → save config (with token); writable-paths loaded on init |
| 2.22 | `features/attachments.js` | parts of settings tab implementation pulled out (filter, bulk-delete) + state (`attachments`, `attachmentsFilter`) | Settings → Attachments → filter by name; orphan filter |
| 2.23 | `features/trash.js` | parts of settings tab (trash list, restore single, empty all) + state (`trashItems`) | Settings → Trash → restore an item |
| 2.24 | `features/notes-ops.js` | 3921-3989 (`promptCreateNote`, `deleteNote`) + 3991-4050 (`promptRenameNote`, `_doRenameNote`, `_promptRenameWithBacklinkWarning`) + 4052-4137 (folder ops: `promptCreateFolder`, `deleteFolder`, `promptRenameFolder`) + 4140-4163 (`openCtxMenu`) + 4932-end (move-picker: `openMovePicker`, `pickerCurrentDirs`, `pickerEnterDir`, `pickerUp`, `pickerSelect`, etc.) + state (`movePicker`, `ctxMenu`) | Right-click note → Rename → completes; Delete → undo; Move → picker works |
| 2.25 | `features/daily.js` | 2807-2860 (`openDailyNote`) | Ctrl+D → opens or creates today's daily |
| 2.26 | `features/stats.js` | 1927-1962 (`refreshStats`, `refreshSyncStatus`, `syncLabel`, `syncTitle`) + state (`statsBar`, `syncStatus`) | Stats bar at bottom shows correct counts |
| 2.27 | `features/note-properties.js` | 2861-2913 (`noteProps`) + state (`noteFrontmatter`, `noteSize`, `noteMTime`, `frontmatterOpen`) | Open note → expand fm-toggle → properties row shows correct stats |

For each task in 2.6 — 2.27:
- [ ] Apply the same 6-step pattern as Task 2.5
- [ ] Verify collision script passes after every commit
- [ ] Verify the feature-specific smoke test in the table

---

### Task 2.28: Migrate `core/state.js` — remaining shared state defaults

After tasks 2.4 — 2.27, the inline `vaultApp()` factory still holds whatever state defaults aren't covered by any feature mixin. These are the cross-cutting fields: `vaults`, `activeVault`, `activePath`, `noteRaw`, `noteHtml`, `noteFrontmatter`, `backlinks`, `mode`, `sidebarOpen`, `backlinksOpen`, `outlineOpen`, `recentFiles`, `_CHIP_KEYS`, etc.

**Files:**
- Create: `static/js/core/state.js`
- Modify: `static/js/app.js`
- Modify: `static/index.html`

- [ ] **Step 1: Create `static/js/core/state.js`**

```js
// Cross-cutting state fields. Owned here when no single feature
// "owns" them (vaults, activeVault, activePath are read by sidebar,
// search, share, render, ...). Returns a function so each Alpine
// instance starts with fresh objects/arrays.

export function initialState() {
  return {
    vaults: [],
    activeVault: '',
    activePath: '',
    allNodes: [],
    noteRaw: '',
    noteHtml: '',
    noteFrontmatter: {},
    backlinks: [],
    mode: 'preview',
    sidebarOpen: true,
    backlinksOpen: false,
    outlineOpen: false,
    recentFiles: [],
    _CHIP_KEYS: ['tags', 'tag', 'aliases', 'alias', 'category', 'categories', 'topic', 'topics', 'status', 'project'],
    // ... whatever else is left after Tasks 2.4 — 2.27
  }
}
```

(Verify by reading the inline `vaultApp()` factory at this point and copying every remaining state-default field.)

- [ ] **Step 2: Wire into `app.js`**

Now `app.js` looks like:

```js
import { initialState } from './core/state.js'
import { utilMixin } from './core/util.js'
import { copyPasteMixin } from './features/copy-paste.js'
import { undoToastsMixin } from './features/undo-toasts.js'
// ... all imports

const mixins = [
  utilMixin,
  copyPasteMixin,
  undoToastsMixin,
  // ... in the order they were added
]

const _originalVaultApp = window.vaultApp
window.vaultApp = function () {
  const base = _originalVaultApp ? _originalVaultApp() : {}
  return Object.assign({}, initialState(), base, ...mixins)
}
```

- [ ] **Step 3: Delete the migrated state from inline factory**

In `static/index.html`, delete every state-default field listed in `initialState()`.

- [ ] **Step 4-6:** Standard JS check + collision check + browser smoke test + commit.

---

### Task 2.29: Migrate `core/init.js`, `core/routing.js`, `core/modal.js` — last remaining methods

Whatever methods are left in the inline factory at this point — `init()`, `_routePath`, `_routeHash`, `_noteURL`, `popstate` handler, modal helpers, focus trap.

**Files:**
- Create: `static/js/core/init.js`, `static/js/core/routing.js`, `static/js/core/modal.js`
- Modify: `static/js/app.js`
- Modify: `static/index.html`

For each of these 3 modules:
- [ ] Same 6-step pattern as Task 2.5
- [ ] Verify smoke test:
  - **init.js:** SPA loads, vaults list populates
  - **routing.js:** Deep link `/n/<vault>/<path>` opens correct note; back/forward works
  - **modal.js:** A modal opens (e.g. delete confirm), focus is trapped, Escape closes

After Task 2.29 completes, the inline factory should contain ONLY the empty `return { }` shell.

---

### Task 2.30: Final cleanup — remove inline `vaultApp` definition

**Files:**
- Modify: `static/index.html` (delete the inline `<script>` block from L1500-end)
- Modify: `static/js/app.js` (drop the `_originalVaultApp` indirection)

- [ ] **Step 1: Verify inline factory is empty**

Inspect `static/index.html` — the `<script>` block that opens `function vaultApp() { return {` should now have nothing between the `{` and the closing `} }`. If it's not empty, find the leftover and migrate it to the appropriate mixin.

- [ ] **Step 2: Delete the inline `<script>` block**

In `static/index.html`, delete the entire block:

```html
<script>
function vaultApp() {
  return {
  }
}
</script>
```

- [ ] **Step 3: Simplify `app.js`**

```js
import { initialState } from './core/state.js'
import { utilMixin } from './core/util.js'
// ... all imports

const mixins = [ /* all the mixins */ ]

window.vaultApp = function () {
  return Object.assign({}, initialState(), ...mixins)
}
```

The `_originalVaultApp` indirection is no longer needed.

- [ ] **Step 4: JS check + collision check**

```bash
node --check static/js/app.js
node tools/check-mixin-collisions.js
```

Expected: OK.

- [ ] **Step 5: Final integrated browser smoke test**

```bash
cd /home/joao/docker && bash -lic 'dc-office-up -d --build vaultreader' 2>&1 | tail -3
```

Use playwright-browser to walk every major user flow:
1. Load → vaults list populates
2. Open a note → preview renders (mermaid + math + callouts + wikilinks)
3. Switch to edit → save → mtime updates
4. Search with operators (`tag:work modified:>7d`) → results ranked
5. Open graph view → drag a node → graph reflows
6. Create share → copy URL → open in new tab → renders correctly
7. Click sidebar attachment (image) → viewer pane shows
8. Click code-block copy button → text copied
9. Settings → Shared tab → revoke a link
10. Bulk-select 3 notes → bulk-delete → undo toast → undo restores

Expected: every flow works; **zero console errors** at any point.

- [ ] **Step 6: Update CHANGELOG**

```markdown
## YYYY-MM-DD — Stage 2: frontend modularization

The inline `<script>` `vaultApp()` factory in `static/index.html`
(~3,550 lines) split into ~24 native ES modules under `static/js/`.
Mixin pattern: each feature exports an object with state + methods,
all `Object.assign`'d into the factory. No bundler, no transpiler —
native browser modules + `<script type="module">`. The
`tools/check-mixin-collisions.js` script verifies in CI that no two
mixins declare the same top-level key.

`static/index.html` shrunk from 5051 lines → ~1500 (just markup +
codemirror-bridge.js + `<script type="module" src="/js/app.js">`).
Adding a feature now usually touches one focused module ≤450 lines.
```

- [ ] **Step 7: Update CLAUDE.md "Where things live"**

Replace the Alpine-state line in the table:

```markdown
| Alpine state defaults (cross-cutting) | `static/js/core/state.js` |
| Alpine init / routing / modal | `static/js/core/{init,routing,modal}.js` |
| Search ranking + UI state | `static/js/features/search.js` |
| Graph view (Cytoscape + cola) | `static/js/features/graph.js` |
| Editor toolbar + autocomplete + paste-image | `static/js/features/editor.js` |
| Share modal + popover | `static/js/features/shares.js` |
| Sidebar tree + drag-drop + bulk | `static/js/features/sidebar.js` |
| Note CRUD + autosave | `static/js/features/notes-{io,ops}.js` |
| Inline file viewer (image/PDF/text/audio/video) | `static/js/features/viewer.js` |
| Render passes (mermaid + math + code-copy + wikilink click) | `static/js/features/render.js` |
| Copy buttons + paste-append + undo toasts | `static/js/features/copy-paste.js` + `features/undo-toasts.js` |
| __cmAPI bridge to bundled CodeMirror | `static/js/lib/codemirror-bridge.js` |
| Mixin assembler entry-point | `static/js/app.js` |
| Mixin collision detector | `tools/check-mixin-collisions.js` |
```

- [ ] **Step 8: Commit + merge**

```bash
git add static/js/app.js static/index.html CHANGELOG.md CLAUDE.md
git -c commit.gpgsign=false commit -m "refactor(frontend): finalize Stage 2 modularization

Inline vaultApp() factory removed. app.js now does:
  Object.assign({}, initialState(), ...mixins)
…with ~24 mixins covering every feature surface. Tools collision-
check in CI guarantees no two mixins declare the same top-level key.
Smoke-tested every user flow end-to-end via headless browser.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"

git checkout main
git merge --no-ff refactor/frontend-modules -m "refactor: frontend modularization (Stage 2)

Squashes the per-mixin commits. static/index.html went from 5051 lines
→ ~1500; ~24 ES modules under static/js/ now hold the JS. Mixin
pattern + native modules; no bundler. Runtime byte-equivalent."
git push origin main
```

---

## Self-review

Re-read [the spec](../specs/2026-04-29-modularization-design.md) and verify each requirement maps to a task:

- ✓ "Stage 1 — Backend split into ~19 .go files" → Tasks 1.1 — 1.18
- ✓ "Stage 2 — Frontend split into ~24 ES modules" → Tasks 2.1 — 2.30
- ✓ "Mixin pattern (Object.assign-based)" → defined in Task 2.3, used in Tasks 2.4 — 2.30
- ✓ "No bundler, no transpiler" — preserved throughout (only `<script type="module">` + `import`)
- ✓ "Verification at the end of Stage 1: go build + docker build + headless smoke" → Task 1.18 step 4
- ✓ "State-default collision linter" → Task 2.1 step 2 (`tools/check-mixin-collisions.js`)
- ✓ "Module load order matters — app.js before Alpine init" → Task 2.3 step 2 explicit instruction
- ✓ "PR per stage" → Stage 1 ends with merge in Task 1.18 step 7; Stage 2 ends in Task 2.30 step 8
- ✓ "Documentation updates (CLAUDE.md, CHANGELOG.md, README.md)" → Tasks 1.18 step 5-6 and 2.30 step 6-7

**Placeholder scan:** No "TBD", "TODO", "implement later" — all task content is concrete.

**Type/method consistency:**
- `_saveNote(content, target)` is called by `pasteAppendToNote` (Task 2.5) and defined in `features/notes-io.js` (Task 2.10) — name matches.
- `showActionToast(message, undoFn)` is called by `pasteAppendToNote` (Task 2.5) and defined in `features/undo-toasts.js` (Task 2.6) — name matches.
- `decorateCodeBlocks`, `rerenderMermaid`, `rerenderMath` called by `pasteAppendToNote`'s reload chain (Task 2.5) and defined in `features/render.js` (Task 2.8) — names match.
- `isWritable(vault, path)` called by `pasteAppendToNote` (Task 2.5) and defined in `features/admin.js` (Task 2.21) or `features/notes-io.js` (Task 2.10) — verify during execution; either location is fine since they're all merged via mixin.

**Spec coverage check:** every section of the spec has at least one task. The "Out of scope" items (tests, search index, refcount index) are correctly NOT in the plan.

**One ambiguity flagged:** the spec lists ~24 modules but my table in Task 2.6 — 2.27 doesn't perfectly match the spec's enumeration. Spec lists: `state, routing, modal, util, search, graph, editor, shares, sidebar, settings, render, outline, tags, trash, attachments, undo-toasts, copy-paste, viewer, notes-io, notes-ops, daily, templates, stats, admin, breadcrumb` = 25. My plan covers all 25 across Tasks 2.4 — 2.29. **Note:** I used "note-properties.js" in Task 2.27 instead of folding it into another module — this is a minor improvement over the spec (separating the noteProps method from generic state defaults). If the executor prefers to fold note-properties into another mixin during execution, that's also fine.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-29-modularization.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
