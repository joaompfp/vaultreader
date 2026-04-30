# Claude Agent: vaultreader

A Go web app serving Obsidian vaults. ~2700-line `main.go` + 3000-line `static/index.html` (Alpine SPA). 8MB scratch container. Lives at `notes.joao.date`.

## Repository

- **Path:** `~/docker/stacks/office/images/vaultreader` (a git submodule of the docker repo, also pushed to `github.com/joaompfp/vaultreader`).
- **Docs:** `README.md` + `docs/` cover the feature surface, API, architecture, config, security, theming. Read those for *what* exists. This file is for *how to work* on it.

## Where things live

The Go server is split into ~18 sibling files in `package main`. The Go compiler concatenates package files at build time, so cross-file references just work — no imports, no exports.

| Want to change… | Look in… |
|---|---|
| Routes / mux setup / `func main()` | `main.go` (~140 lines) |
| Markdown rendering / wikilinks / callouts / embeds / frontmatter | `markdown.go` |
| Note CRUD / save / upload / search HTTP / resolve / templates / backlinks | `notes.go` |
| Search ranking + operators (parseSearchQuery + searchVault) | `search.go` |
| The wikilink index (buildAll, resolve, getBacklinks) | `index.go` |
| Share creation + view + asset + file (rewriteShareImageURLs) | `shares.go` |
| Trash naming (VRTRASH_…) + handlers | `trash.go` |
| Attachment listing + refcount | `attachments.go` |
| Graph data builder (vault/folder/ego scopes) | `graph.go` |
| Tag aggregation across vaults | `tags.go` |
| Vault listing + tree + SPA index | `vaults.go` |
| File serving + safePath / vaultPath / vault icons | `files.go` |
| Stats endpoint | `stats.go` |
| Syncthing status (TLS-insecure for self-signed) | `sync.go` |
| WebDAV mount (read-only via method allowlist) | `webdav.go` |
| `server` struct + AdminConfig + admin token + writable paths | `admin.go` |
| Gzip middleware + JSON helpers + rate limiter | `http.go` |
| Shared types + regexes + goldmark init + `embed.FS` | `data.go` |
| Alpine state | `static/index.html` ~L1010 (`function vaultApp()`) — until Stage 2 |
| The `__cmAPI` (CodeMirror wrapper) | `static/index.html` second `<script>` block, ~L944 |
| Stylesheet | `static/style.css` (organized by section comments) |
| Asset bundles | `static/{codemirror.bundle,mermaid.min,katex.min,cytoscape.min,cola.min,cytoscape-cola,alpine.min}.js` + `static/fonts/` |
| Dockerfile | Already does `go mod tidy` in builder, so adding a Go dep is a one-file change |

## Standing rules

### File operations
- **The shell aliases `mv` to `mv -i`** in this user's profile. `mv` will hang on overwrite prompts. Use `/bin/mv -f` to force or use `Edit`/`Write` tools.
- **`appdata/` is gitignored.** It contains `config.json` (admin token!), `shares.json`, and `icons/`. Never commit it.
- **`.worktrees/` is gitignored.** Past multi-agent design sessions left litter; the gitignore prevents recurrence.
- **Multi-line bash commands can lose CWD** when something earlier crashes mid-pipeline (saw it after a `git commit -m "$(cat <<EOF…)"` failed). When a follow-up command says "fatal: not a git repository", `cd /home/joao/docker/stacks/office/images/vaultreader && …` to recover.
- **Every JS edit must pass `node --check` against the inline `<script>`** before commit. Pattern is in this file's "Verification" section. A broken inline JS = blank-page SPA on next deploy.

### Container ops
- **Deploy:** from `~/docker`, `bash -lic 'dc-office-up -d --build vaultreader'`. The `-lic` is required — the shell aliases (`dc-*`) are loaded from `~/.bashrc`.
- **Bridge IP rotates across rebuilds.** Don't hard-code `172.10.0.X`. To get the current IP: `docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}'`. Use this for headless smoke tests since `notes.joao.date` is gated by Authelia.
- **Distroless final image.** Cannot `docker exec` into a shell — no `wget`, no `curl`, no `sh`. Smoke-test via the host hitting the bridge IP.
- **Vaults live at `/home/joao/vaults/`** mounted into `/vaults`. `/home/joao/.hermes/skills` is a sub-mount as `/vaults/pessoal/agents/hermes/skills:ro`.

### Code style
- **Single-file Go.** Don't split `main.go` without a strong reason. Search/grep is faster than file navigation here.
- **Don't add libs lightly.** Three deps total: `goldmark`, `golang.org/x/net/webdav`, `gopkg.in/yaml.v3`. Each new one needs to justify itself.
- **Don't add a JS build step.** Frontend is hand-written + bundled libs as `<script src>`. Adding webpack/rollup/vite would be a large regression in clarity.

### CodeMirror gotchas
- **The bundled `codemirror.bundle.js` is curated.** It exports: `EditorState`, `EditorView`, `defaultKeymap`, `history`, `historyKeymap`, `indentWithTab`, `keymap`, `lineNumbers`, `markdown`, `oneDark`, `syntaxHighlighting`, `defaultHighlightStyle`, `highlightActiveLine`, `drawSelection`. **Does NOT export** `EditorSelection`, `@codemirror/autocomplete`, `CompletionContext`. The wikilink popup is hand-rolled because of this.
- **Use plain `{anchor: N, head: M}` for selections** — `view.dispatch({selection: {anchor, head}})` works without the `EditorSelection` export.

### Alpine.js gotchas
- **`:class="[a, {b: c}]"` array+object form silently degrades to `[object Object]`** in the bundled Alpine version. Use string-concat instead: `:class="'foo ' + (cond ? 'bar' : '')"`.
- **`$watch('searchOpen', …)` resets `searchQuery = ''` on open.** Programmatic openers (chip click, tag click, saved-search) must use the `$nextTick` dance: open the overlay first, set the query in `$nextTick`, then call `doSearch()`.
- **Modal opens are watched too** — `modal.open` flipping true triggers `trapFocus('modalOverlay')`. The optional `secondaryLabel`/`onSecondary` slots support 3-button modals (Cancel · secondary · primary).

### Backend gotchas
- **`isWritable` returns false when `rw_paths` is empty.** Default-deploy has empty `rw_paths`, so writes 403 until the user adds entries. The empty-state copy now hints at this; previously said "all vaults are read-only" which was technically right but misleading.
- **`safePath` blocks `..`, absolute paths, Windows-style `\…`.** Every endpoint that takes a path must call this.
- **PUT `/api/note` returns 200 + JSON `{"mtime"}` now**, not 204. Old clients that ignore the body still work.
- **DELETE `/api/note` and `/api/folder` return `{"movedTo": ".trash/VRTRASH_<b64>_<unix>…"}`.** The `movedTo` value is what the undo-toast flow passes to `/api/trash/restore`. Don't change the response shape without updating the frontend's `_showUndoToast` flow.
- **Trash naming uses base64-url-encoded original path** (`VRTRASH_<b64>_<unix><ext>`). Decoded by `decodeTrashName` with a `legacyDecodeTrashName` fallback for entries from before that scheme. The legacy `__→/` flatten was bug-prone for paths containing `__` or starting with `_`.
- **Conflict detection lives only in PUT.** POST `/api/note` (create) returns 409 if file exists; that's a different conflict semantics.
- **`saveNote` runs `normalizeMarkdown`** — strips trailing whitespace per line, ensures one trailing newline. Applies to every save path (note PUT, share PUT, paste-image-append). Don't bypass it; reduces git-diff noise.
- **Search has operators**: `tag:`, `path:`, `title:`, `modified:>Nd / <YYYY-MM-DD`. Parsed by `parseSearchQuery`. Plain text after operators acts as the body substring filter. Image attachments only honor `path:` and `modified:` (no frontmatter) and only when `plain != ""`.
- **The `/share/<token>/...` namespace has sub-routes**: `/file?path=…` (image embeds, ext allowlist) and `/asset?name=…` (mermaid/katex/fonts, name allowlist). Both depend on the reverse-proxy bypassing `/share/*`. Adding more sub-routes? Update `handleShareView`'s `if len(parts) == 2` switch.
- **Share-page asset/file URLs are path-relative**, anchored to `<base href="<token>/">`. The container is reachable at both `/share/<token>` (private, on `notes.joao.date`) and `/notas/<token>` (public alias on `joao.date`, Traefik strips `/notas/` → `/share/`). Emitting absolute `/share/<token>/...` URLs would 404 on the `/notas/` alias because `joao.date` has no `/share/` route. Don't bake the prefix into rewritten share-page HTML — keep relying on `<base href>`.
- **Plain markdown images `![alt](path)` in shared notes** are rewritten alongside `![[…]]` embeds — the second pass in `rewriteShareImageURLs` resolves vault- or note-relative paths against the note's directory and emits `file?path=…`. Don't drop this; the image-heavy converted-from-PDF case (e.g. RIBA reports) depends on it.

### Routing gotchas
- **`/api/trash` is GET-only**, returns 405 on DELETE. Use `/api/trash/empty` for delete (with optional `?path=` for single item).
- **`/api/shares/revoke` is DELETE-only** and takes `?token=`. For bulk-revoke, use `/api/shares/revoke-all` (also DELETE) — single call, no rate-limit cliff. Don't loop over `/revoke` for bulk.
- **WebDAV at `/webdav/` is read-only via method allowlist.** The `OPTIONS` response still advertises mutating verbs in the `Allow:` header (because `webdav.Handler` writes it before the wrapper can intercept) — cosmetic, not functional.

### Goldmark output gotcha
- **Goldmark HTML-escapes `&` to `&amp;`** in attribute values. When post-processing rendered HTML for URL rewrites (e.g. `rewriteShareImageURLs`), decode entities before parsing as URL. Lost a deploy cycle to this.

## Recently shipped (2026-04-29/30)

Don't re-implement these — they exist. Most live behind specific commits cited in CHANGELOG.md and ROADMAP.md (see "Recently shipped" section there for the full list).

Quick summary of what's there now: cola live-simulation graph (with ego/folder/vault scopes), search operators (`tag:`, `path:`, `modified:>7d`), undo toasts, bulk select + bulk move/delete, drag-and-drop in sidebar, paste-in-preview, rename warning, daily-note edit-mode, save normalization, attachment back-references, mermaid+katex in shared notes (via `/share/<token>/asset?…`), unambiguous trash naming, two-row toolbar, collapsible properties strip, reveal-in-sidebar (`Ctrl+Shift+L`), Alt+←/→ history.

For *what* exists user-facing, point at `docs/features.md`.
For *how* it's wired, the sections in this file are still the agent-friendly map.

## Standing hooks for future work

### Things that should NOT change
- **Wikilink syntax / semantics.** Notes are Obsidian-compatible; users edit them in Obsidian on other devices. Don't add VaultReader-specific markup.
- **`appdata/` / vault layout split.** Vaults are user-owned; appdata is VaultReader-owned. Mixing them breaks the round-trip.
- **The `pinned: true` frontmatter convention.** It's user-settable from any editor (Obsidian, VaultReader, vim) — do not invent a parallel "pinning" mechanism.
- **The `attachments/` per-note folder convention.** Matches Obsidian's default. Don't move uploads elsewhere without flagging.

### Things that NEED change someday
- **No tests yet.** Highest-priority targets: `safePath` (security), `isWritable` (security), `expandEmbeds` + `resolveWikilinkTarget` (rendering correctness), `handleUpload` (security). ~200 lines of table tests would cover most of the risk surface.
- **Search is a full filesystem walk** per query. Bleve / Tantivy index for vaults > a few thousand notes.
- **Attachment refcount** is O(notes × attachments). Reverse index would make it instant; pessoal takes ~4s today.
- **Index rebuild on startup** (always full walk). Not yet bottleneck-y.
- **Inline `#tag` detection.** `/api/tags` only sees frontmatter today.
- **`\[…\]` math block** doesn't render because goldmark eats the leading `\`. Currently shipped: `$$…$$` block + `\(…\)` inline; bare `$…$` deliberately disabled.

## Verification before committing

For UI changes, the headless smoke pattern:
1. `bash -lic 'dc-office-up -d --build vaultreader'`
2. `IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')`
3. Use the `mcp__playwright__browser_*` tools against `http://$IP:8080/`
4. Exercise the new flow with `browser_evaluate` (call into Alpine state directly via `window.Alpine.$data(…)`), check rendered DOM, verify console errors
5. **Always clean up test notes** — they're in the real `/home/joao/vaults/` dir, not a sandbox. Use a `__vr_…` prefix so they're easy to glob+delete.

For backend changes, `curl` against the bridge IP after deploy. Check at minimum: success path + 1-2 negative paths (path traversal, wrong method, missing param).

For JS changes, **always run a syntax check before committing**:
```bash
node -e "
const fs = require('fs');
const html = fs.readFileSync('static/index.html', 'utf8');
const idx = html.lastIndexOf('<script>');
const end = html.lastIndexOf('</script>');
fs.writeFileSync('/tmp/vr-check.js', html.substring(idx + 8, end));
" && node --check /tmp/vr-check.js
```
A typo in the inline JS will silently break the entire SPA — the browser shows a blank page with a syntax error in console. Catching it pre-commit saves a deploy+rollback cycle.

## Boundaries

- **Always:** Update CHANGELOG.md when shipping a feature or fix worth users knowing about.
- **Always:** Run `node --check` on inline JS before committing.
- **Always:** Verify smoke-test results from headless browser, not just from the diff.
- **Never:** Mock the filesystem in tests. Vaults live on disk; tests should hit real (temp) directories.
- **Never:** Add a JS build step.
- **Never:** Bundle without an explicit user-visible reason (each MB the user pays for must be justified).
- **Never:** Skip `isWritable` / `safePath` checks on a new write endpoint. New endpoints must mirror the existing patterns from `handlePutNote` / `handleUpload`.
- **Never:** Commit or share the contents of `appdata/`. The admin token is a secret.
