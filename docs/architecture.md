# Architecture

A short tour of how VaultReader is put together.

## At a glance

```
┌────────────────────┐         ┌──────────────────────────────────┐
│ Browser            │  HTTP   │ Single Go binary (~8MB)          │
│ • Alpine.js SPA    │◀───────▶│ • net/http server                │
│ • CodeMirror 6     │         │ • goldmark renderer              │
│ • Mermaid v11      │         │ • In-memory wikilink index       │
│ • KaTeX 0.16       │         │ • golang.org/x/net/webdav        │
│ • Cytoscape 3      │         │ • embed.FS for static + fonts    │
└────────────────────┘         └──────────────────────────────────┘
                                             │
                                             ▼
                       ┌────────────────────────────────────────┐
                       │ Filesystem (mounted into container)    │
                       │ /vaults/<vault>/path/to/note.md        │
                       │ /appdata/{config,shares}.json + icons/ │
                       └────────────────────────────────────────┘
```

The vault on disk is the source of truth. VaultReader reads it on startup, builds an in-memory wikilink index, then serves HTTP requests against the live filesystem (no caching of file contents).

## Backend

### One-file Go server
Everything lives in [`main.go`](../main.go) (~2700 lines after the recent feature wave). No framework — just `net/http` + a flat `ServeMux`. Each route is a method on the `*server` struct.

### Dependencies (3 total)
```go
require (
    github.com/yuin/goldmark v1.7.1   // markdown rendering
    golang.org/x/net v0.30.0          // WebDAV
    gopkg.in/yaml.v3 v3.0.1           // frontmatter parsing
)
```

Goldmark is configured with: GFM, Tables, Strikethrough, TaskList, AutoHeadingID, Unsafe HTML pass-through. No syntax highlighting (we hand off to the client for Mermaid).

### Server struct
```go
type server struct {
    vaultsDir  string         // -vaults flag
    appdataDir string         // -appdata flag
    cfg        Config         // appdata/config.json (admin token + RW paths)
    cfgMu      sync.RWMutex   // guards cfg
    idx        *NoteIndex     // wikilink index
    shares     *ShareStore    // appdata/shares.json
}
```

### NoteIndex
In-memory bidirectional wikilink graph, built once at startup by walking every `.md` file under every vault.

```go
type NoteIndex struct {
    mu       sync.RWMutex
    outbound map[string][]string  // vaultKey → []normalized target name
    inbound  map[string][]string  // normalized name → []vaultKey that link to it
    allNotes map[string]NoteRef   // normalized name → {vault, path, title}
}
```

`vaultKey(vault, path)` is `"vault:path"`. Normalized name is `lowercase(basename without .md)`.

`allNotes` is intentionally double-keyed: the bare normalized name AND the compound `vault:name` form. Lookup falls back through both, allowing `[[foo]]` to resolve regardless of vault.

The index is **rebuilt from scratch on startup**. There's no on-disk cache — startup walks the entire vaults dir. For typical sizes (few thousand notes) this is sub-second. The index is also **mutated incrementally** on note save/delete via `idx.updateNote()` / `idx.removeNote()`, so it stays fresh between full rebuilds.

### Reverse lookup (inbound)
Built at the same time as `outbound`. Used by `getBacklinks(vault, path)` to find every note that links to `<path>`. The neighborhood-graph endpoint also walks `inbound` to collect the BFS frontier.

### ShareStore
Plain JSON file at `appdata/shares.json`, atomic write via tempfile+rename. Each entry: `{token, vault, path, writable, created_at, expires_at, label}`. Tokens are 24 hex chars from `crypto/rand`. The store filters expired entries on every list call; expired entries stay in the JSON until a save happens, but they're never served.

### Config
Plain JSON file at `appdata/config.json`. Atomic write same as shares. Two fields: `admin_token` (string) and `rw_paths` (string array of vault-relative path prefixes).

### Routing

```
/                        → handleIndex (serves index.html for SPA routing)
/health                  → handleHealth (200 OK)

/api/vaults              → list vaults
/api/tree                → vault directory tree
/api/note                → GET/POST/PUT/DELETE single note
/api/upload              → POST multipart image upload
/api/move                → POST rename/move
/api/folder              → folder ops
/api/search              → substring search
/api/resolve             → wikilink resolution
/api/stats               → vault stats (note counts)
/api/sync-status         → Syncthing API proxy
/api/vault-icon          → serves appdata/icons/<vault>.<ext>
/api/file                → serves any vault file (image embeds use this)
/api/admin/config        → GET/POST admin config
/api/admin/restart       → POST trigger re-exec
/api/shares              → list active shares
/api/shares/create       → POST create
/api/shares/revoke       → POST revoke
/api/trash               → list per-vault trash
/api/trash/restore       → POST restore from trash
/api/trash/empty         → DELETE single item or whole trash
/api/attachments         → GET list + ref counts; DELETE soft-delete
/api/graph               → GET wikilink graph (whole / folder / ego)
/api/tags                → GET frontmatter tag aggregation

/n/<vault>/<path>        → SPA route (renders the same index.html, JS handles routing)
/share/<token>           → public share-link page
/webdav/                 → read-only WebDAV (golang.org/x/net/webdav)

/<everything else>       → http.FileServer over embed.FS (static assets)
```

### Middleware stack

```
gzipMiddleware
    └─ rateLimiter (240 req/min per IP, sliding window)
        └─ mux
```

The rate limiter inspects `X-Real-IP` and `X-Forwarded-For` (left-most hop), falling back to `r.RemoteAddr`. Behind Traefik / Authelia each user gets a real per-IP bucket; without those headers everyone behind the same proxy shares one bucket.

### Embed.FS

```go
//go:embed static
var staticFiles embed.FS
```

`static/` contains `index.html`, `style.css`, all the third-party libs, and `static/fonts/` (60 KaTeX font files). Everything is served via `http.FileServer(http.FS(subFS))` where `subFS, _ := fs.Sub(staticFiles, "static")`. No separate static dir to deploy.

## Frontend

### Single-page app, no build step

`static/index.html` is the entire application:
- ~3000 lines of HTML (Alpine.js bindings) + inline `<script>` containing the Alpine state object (~1600 lines of JS).
- Loads Alpine, CodeMirror, Mermaid, KaTeX, Cytoscape as separate `<script src="…">` tags.
- No webpack, no rollup, no transpilation. The whole frontend works in any modern browser.

### Alpine.js state

One root component (`#app` with `x-data="vaultApp()"`) holds the entire UI state. Examples of state fields:

```javascript
{
  vaults, activeVault, allNodes, cwd, activePath,
  noteRaw, noteHtml, noteFrontmatter, noteMTime, noteSize,
  backlinks, mode, sidebarOpen, backlinksOpen, outlineOpen,
  searchQuery, searchResults, searchOpen,
  modal, ctxMenu, newMenuOpen, settingsOpen, settingsTab,
  shareModal, activeShares, trashItems, attachments,
  graphOpen, graphVault, graphFolder, graphCenter, graphDepth,
  tagsOpen, tagsList, savedSearches,
  // ... ~80 fields total
}
```

Every overlay (modal, search, settings, graph, tags, etc.) is a single boolean flag; opening = setting it true.

### Watchers

`init()` registers `$watch` listeners that bridge state and side effects:
- `searchOpen`, `modal.open`, etc. → trap focus when open, untrap on close.
- `noteHtml` → re-bind the outline scroll-spy after re-render.
- `outlineOpen`, `sidebarOpen` → persist to localStorage.

### CodeMirror

Wrapped in `window.__cmAPI` (a tiny IIFE shim around CodeMirror 6's `EditorView`). Created lazily when the user enters edit mode; destroyed on exit. Extensions enabled: `history`, `lineNumbers`, `markdown`, `lineWrapping`, plus `EditorView.domEventHandlers` for paste/drop and an `updateListener` for autocomplete.

Bundled `static/codemirror.bundle.js` exposes only a curated subset of CodeMirror's exports. Notably **does NOT export `@codemirror/autocomplete`** — the wikilink popup is hand-rolled (CodeMirror `updateListener` + `coordsAtPos` + Alpine-driven popup div) rather than using the official autocomplete extension.

### Mermaid / KaTeX / Cytoscape

All loaded as separate scripts. Mermaid + Cytoscape use `defer` since they're only needed when the user opens a note containing diagrams or opens the graph view. KaTeX loads eagerly because math could appear in any note's preview.

`rerenderMermaid()` walks `#preview` for `pre code.language-mermaid` blocks and replaces each with a rendered SVG via `mermaid.render()`. `rerenderMath()` calls KaTeX's `renderMathInElement` over the same container. Both run on every preview render after a 200ms debounce.

## Persistence model

What goes where:

| Data | Storage | Ownership |
|---|---|---|
| Note content | `<vault>/path/to/note.md` | User (Obsidian-compatible) |
| Frontmatter | YAML at top of `.md` | User |
| Image embeds | `<note-dir>/attachments/foo.png` | User |
| Soft-deleted notes | `<vault>/.trash/foo_<unix>` | User |
| Wikilink index | RAM | VaultReader (rebuilt on startup) |
| Admin token, RW paths | `appdata/config.json` | VaultReader (gitignored) |
| Active shares | `appdata/shares.json` | VaultReader |
| Vault icons | `appdata/icons/<vault>.{png,svg,…}` | User-customized |
| Per-browser prefs | localStorage (sidebar width, dark mode, sort, recents, pinned, saved searches) | User-browser |

The split is deliberate: **everything in `<vault>/` is Obsidian-compatible** so you can run Obsidian on the same files without conflict. Only `appdata/` contains VaultReader-specific metadata.

## Build & deploy

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY . .
RUN go mod tidy                                    # resolve deps in-image
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" -trimpath -o vaultreader .

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/vaultreader /vaultreader
VOLUME ["/vaults", "/appdata"]
EXPOSE 8080
ENTRYPOINT ["/vaultreader"]
```

Final image is `scratch` + the static binary + CA bundle (for outbound HTTPS to Syncthing). ~8MB total after the `-s -w` strip.

`go mod tidy` in the builder stage means adding a Go dependency is a one-file change to `main.go` — no host-side `go mod tidy` round-trip needed before pushing.

## Performance notes

- **Index rebuild on startup** is the dominant cost. ~100k tiny `.md` files would take a few seconds; a few hundred is instant.
- **Search** does a full filesystem walk per query (no cache). Capped at 20 results to bound work. For larger vaults a Bleve / Tantivy index would help.
- **Attachment scan** is O(notes × attachments). For pessoal (581 notes × 73 images) it takes ~4s on first call. A reverse-index built at startup time + maintained on save would make it instant.
- **Graph endpoint** reuses the in-memory wikilink index — fast for any scope.
- **Mermaid render** is the slowest client-side cost; most diagrams render in 50-200ms.

None of these are urgent. The whole system is designed for personal-vault scale (~thousands of notes), not knowledge-base scale (~millions).

## Where to look for things

- Need to change a route? `main.go` around L2580 (`mux.HandleFunc` block).
- Need to add Alpine state? `static/index.html` around L1010 (`function vaultApp() { return { … } }`).
- Need to add a watcher? Look for `this.$watch(…)` in `init()`.
- Adding a CodeMirror feature? `__cmAPI.create()` near the top of the second `<script>` block.
- Adding a settings tab? Search for `settingsTab === '` to see how existing tabs are wired (button + body + lazy-load hook in `openSettings`).
