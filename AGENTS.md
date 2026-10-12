# AGENTS.md — VaultReader contributor and agent guide

A Go web app that serves Obsidian-compatible vaults: ~25 sibling files in `internal/server/` (one `package server`; route table in `routes.go`, `run.go` is startup) behind a flags-only `cmd/vaultreader/main.go`, plus a large `web/static/index.html` (Alpine.js SPA, embedded via `web/embed.go`). The runtime image also carries `officecli` for DOCX export. User-facing docs live in `README.md` and `docs/`; this file is the map and the list of traps.

## Where things live

Go files below are in `internal/server/` unless a path is given. They share `package server`, so cross-file references need no imports.

| Want to change… | Look in… |
|---|---|
| Command-line flags, build `version` | `cmd/vaultreader/main.go` |
| Startup: index build, rescanner, graceful shutdown (`Run`) | `run.go` |
| URL → handler table, `feature()` gating, middleware chain (`handler()`) | `routes.go` |
| Per-instance config validation, feature flags (`knownFeatures`), `share_base_url` | `config.go` |
| `Remote-User` attribution (`userFrom`) and fail-closed `requireUser` middleware | `identity.go` |
| `GET /api/instance`, `/health`, `version` (set by `Run` from `-ldflags -X main.version`) | `instance.go` |
| Append-only `appdata/audit.jsonl` (`AuditEntry`, `s.audit(...)`) | `audit.go` |
| 30 s stat-only rescan of every vault, shared `walkVaultFiles` | `rescan.go` |
| Markdown rendering, wikilinks, callouts, embeds, frontmatter | `markdown.go` |
| Note CRUD, save, upload, search HTTP, resolve, templates, backlinks | `notes.go` |
| Search ranking and operators (`parseSearchQuery`, `searchVault`) | `search.go` |
| Wikilink index (`buildAll`, resolve, `getBacklinks`) | `index.go` |
| Share creation, view, asset, file (`rewriteShareImageURLs`) | `shares.go` |
| Trash naming (`VRTRASH_…`) and handlers | `trash.go` |
| Attachment listing and refcount | `attachments.go` |
| Graph data (vault/folder/ego scopes) | `graph.go` |
| Tag aggregation | `tags.go` |
| Vault listing, tree, SPA index | `vaults.go` |
| File serving, `safePath` / `vaultPath`, vault icons | `files.go` |
| Stats endpoint | `stats.go` |
| Syncthing status | `sync.go` |
| WebDAV mount (read-only via method allowlist) | `webdav.go` |
| `server` struct, `AdminConfig`, admin token, writable paths | `admin.go` |
| Gzip middleware, JSON helpers, rate limiter | `http.go` |
| Shared types, regexes, goldmark init, `staticFiles` | `data.go` |
| DOCX export | `export.go` |
| Alpine state | `web/static/index.html`, `function vaultApp()`, organised by `// ── Section ─` dividers |
| CodeMirror wrapper (`__cmAPI`) | `web/static/index.html`, second `<script>` block |
| Stylesheet | `web/static/style.css` |
| Bundled libraries | `web/static/*.js`, `web/static/fonts/` |

## Conventions

- Flat `package server`, one concern per file. Add a file only for a new concern. `cmd/vaultreader` stays flags-only.
- Few dependencies (goldmark, `golang.org/x/net/webdav`, `gopkg.in/yaml.v3`). A new one needs a justification.
- No JS build step. The frontend is hand-written plus bundled libraries loaded with `<script src>`.
- Tests use real temporary directories, never filesystem mocks. `go test ./...` also runs in the Docker builder stage, so a failing test fails the image build.
- Every change to `web/static/index.html` must pass a syntax check of the inline script before commit; a typo blanks the whole SPA:
  ```bash
  node -e "
  const fs = require('fs');
  const html = fs.readFileSync('web/static/index.html', 'utf8');
  const idx = html.lastIndexOf('<script>');
  const end = html.lastIndexOf('</script>');
  fs.writeFileSync('/tmp/vr-check.js', html.substring(idx + 8, end));
  " && node --check /tmp/vr-check.js
  ```
- Do not run repo-wide formatters; they rewrite files outside your change.
- Update `CHANGELOG.md` for user-visible features and fixes.
- Any new write endpoint must call `safePath` and `isWritable`, and call `s.audit(...)` next to the `isWritable` check. Mirror `handlePutNote` / `handleUpload`.
- Any new optional route belongs behind `s.feature(...)` in `routes.go`. `AuditEntry` is a contract with external consumers of `audit.jsonl`; change it deliberately.
- Never commit `appdata/`: it holds the admin token and share data.

## Things that must not change

- Wikilink syntax and semantics: notes stay Obsidian-compatible. No VaultReader-specific markup.
- The split between user-owned vaults and VaultReader-owned `appdata/`.
- The `pinned: true` frontmatter convention and the per-note `attachments/` folder convention.

## Gotchas

### Backend
- `isWritable` is false when `rw_paths` is empty, so writes return 403 until entries are added.
- `safePath` rejects `..`, absolute paths and Windows-style `\…`. Every endpoint taking a path must call it.
- Dot-directories (including `.trash`) are never indexed; search, graph and backlinks skip them.
- `PUT /api/note` returns 200 with `{"mtime"}`. `POST /api/note` returns 409 if the file exists (different conflict semantics; conflict detection lives only in PUT).
- `DELETE /api/note` and `/api/folder` return `{"movedTo": ".trash/VRTRASH_<b64>_<unix>…"}`; the frontend undo flow passes that value to `/api/trash/restore`. Do not change the shape without updating `_showUndoToast`.
- Trash names use base64-url of the original path, decoded by `decodeTrashName` with a `legacyDecodeTrashName` fallback.
- `saveNote` runs `normalizeMarkdown` (trailing whitespace stripped, one trailing newline) on every save path. Do not bypass it.
- Search operators: `tag:`, `path:`, `title:`, `modified:>Nd` / `<YYYY-MM-DD`. Image attachments honour only `path:` and `modified:`.
- `/share/<token>/…` has sub-routes `/file?path=…` (image embeds, extension allowlist) and `/asset?name=…` (mermaid/katex/fonts, name allowlist). Adding another? Update the `len(parts) == 2` switch in `handleShareView`.
- Share-page asset and file URLs are path-relative, anchored to `<base href="<token>/">`, so the page works under any proxy prefix. Do not emit absolute `/share/<token>/…` URLs.
- Plain markdown images `![alt](path)` in shared notes are rewritten alongside `![[…]]` embeds (second pass in `rewriteShareImageURLs`). Keep it.
- Goldmark HTML-escapes `&` to `&amp;` in attribute values; decode entities before parsing URLs out of rendered HTML.

### Routing
- `/api/trash` is GET-only (405 otherwise); delete via `/api/trash/empty` (optional `?path=`).
- `/api/shares/revoke` is DELETE-only with `?token=`; bulk revoke is `/api/shares/revoke-all`.
- WebDAV at `/webdav/` is read-only through a method allowlist; the `Allow:` header on OPTIONS still lists mutating verbs (cosmetic).

### Alpine.js
- `:class="[a, {b: c}]"` degrades to `[object Object]` in the bundled Alpine; use string concatenation.
- `$watch('searchOpen', …)` resets `searchQuery` on open. Programmatic openers must open the overlay, set the query in `$nextTick`, then call `doSearch()`.
- Opening a modal triggers `trapFocus('modalOverlay')`. The `secondaryLabel` / `onSecondary` slots give three-button modals.

### CodeMirror
- `web/static/codemirror.bundle.js` is a curated one-shot esbuild bundle of the public `@codemirror/*` packages (`esbuild entry.js --bundle --format=iife --global-name=CM --minify --target=es2020`); there is no in-repo build step. To rebuild, use a temp dir with an `entry.js` re-exporting the needed symbols and a `package.json` pinning versions, then copy the output back to `web/static/`. Do not redirect stderr into the bundle.
- Find/replace is the stock CM6 panel (`search({ top: true })` + `searchKeymap`). Bracket pairing is `closeBrackets()`. Heading fold is `foldGutter()`.

## Known limits

- Search is a full filesystem walk per query; attachment refcount is O(notes × attachments); the index is rebuilt in full at startup (plus the 30 s stat-only rescan).
- `/api/tags` sees only frontmatter tags, not inline `#tags`.
- `\[…\]` math blocks do not render (goldmark eats the leading `\`); `$$…$$` and `\(…\)` do, bare `$…$` is deliberately off.
- Test coverage is partial: `safePath`/`isWritable` table tests, `expandEmbeds`, `resolveWikilinkTarget` and `handleUpload` are still missing.

## Boundaries

- Always: run `node --check` on the inline script before committing `web/static/index.html`; verify UI changes in a real browser, not just from the diff.
- Never: mock the filesystem in tests; add a JS build step; skip `safePath` / `isWritable` on a write endpoint; commit `appdata/`.
