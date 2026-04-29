# API reference

Every HTTP endpoint, in route-registration order. Auth notes apply to all endpoints unless overridden.

**Auth:** writes are gated by `isWritable(vault, path)` against `appdata/config.json` → `rw_paths`. Admin endpoints (`/api/admin/*`) require an `X-Admin-Token` header matching `appdata/config.json` → `admin_token`. Everything else is open (typically protected by a reverse proxy + forward auth like Authelia).

**Conventions:**
- All paths are vault-relative unless noted.
- All `vault` query params use the vault directory name, not its display label.
- Request/response bodies are JSON unless stated otherwise.
- All errors come back as `{"error": "<msg>"}` with the appropriate status code.

---

## Health & meta

### `GET /health`
Liveness check.
**Response:** `200 OK` with body `OK`. No auth, no work.

### `GET /api/stats`
Vault stats — note counts.
**Response:**
```json
{
  "totalNotes": 1623,
  "vaults": [
    {"name": "pessoal", "noteCount": 581},
    {"name": "work", "noteCount": 720}
  ]
}
```

### `GET /api/sync-status`
Proxies the configured Syncthing `/rest/db/completion` endpoint.
**Response:**
```json
{"available": true, "state": "synced", "message": "Up to date"}
```
States: `synced` / `syncing` / `error` / `unknown`.

---

## Vault discovery

### `GET /api/vaults`
List vault directory names, ordered by `vaultOrder` then alphabetical.
**Response:** `["pessoal", "work", "pcp", …]`

### `GET /api/tree?vault=<vault>`
Full directory tree for a vault. Skips `.trash/`, `.obsidian/`, hidden dirs.
**Response:**
```json
[
  {"name": "agents", "path": "agents", "isDir": true, "children": [...]},
  {"name": "foo.md", "path": "foo.md", "isDir": false, "mtime": 1777411898, "size": 1234}
]
```

### `GET /api/vault-icon?name=<vault>`
Serves the vault's icon file from `appdata/icons/<vault>.<ext>`. Returns `image/*`. 404 if missing — frontend falls back to a generic SVG.

---

## Notes

### `GET /api/note?vault=<vault>&path=<path>`
Read a note.
**Response:**
```json
{
  "raw": "---\ntitle: Foo\n---\n# Body",
  "html": "<h1>Body</h1>",
  "frontmatter": {"title": "Foo"},
  "backlinks": [{"vault": "pessoal", "path": "other.md", "title": "Other", "excerpt": "…"}],
  "mtime": 1777411898,
  "size": 234
}
```
Errors: `400 invalid vault/path`, `404 note not found`.

### `POST /api/note?vault=<vault>&path=<path>`
Create a new note. Won't overwrite — returns `409 note already exists` if the path exists.
**Body:** `{"raw": "initial content"}`
**Response:** `201 Created` with `{"path": "foo.md", "status": "created"}`.

### `PUT /api/note?vault=<vault>&path=<path>[&ifMTime=<unix>]`
Save an existing note.
**Body:** raw markdown (`Content-Type: text/plain; charset=utf-8`)
**Response:** `200 OK` with `{"mtime": <new_unix>}` so the client stays in sync.

If `ifMTime` is supplied and the on-disk file is newer (with 1-second slop), returns `409 Conflict` with body:
```json
{
  "error": "file changed on disk",
  "diskMtime": 1777411900,
  "diskRaw": "<current file content>"
}
```
The client uses this to render the conflict-resolution modal.

### `DELETE /api/note?vault=<vault>&path=<path>`
Move to `<vault>/.trash/` using the `VRTRASH_<base64url(originalPath)>_<unix><ext>` naming scheme. Soft delete, recoverable via `/api/trash/restore`.
**Response:** `{"status": "deleted", "movedTo": ".trash/VRTRASH_…<unix>.md"}`. The `movedTo` value is what to pass to `/api/trash/restore` to undo (used by the undo-toast flow).

### `POST /api/upload`
Image upload. Multipart form. Cap: 10MB.
**Form fields:** `vault`, `notePath` (the *containing note*), `file`.
**Validation:** `Content-Type` must start with `image/`; ext inferred from MIME (`png`/`jpg`/`gif`/`webp`/`svg`).
**Side effect:** writes to `<note-dir>/attachments/<note-base>-<unix>.<ext>` (creating `attachments/` if missing).
**Response:** `{"path": "attachments/<filename>"}` (relative to the note's dir, ready to embed as `![[…]]`).

### `POST /api/move`
Rename or move a note/folder.
**Body:** `{"vault": "...", "fromPath": "...", "toPath": "..."}`
**Response:** `204 No Content`.

### `POST /api/folder`
Create folder.
**Body:** `{"vault": "...", "path": "..."}`
**Response:** `201 Created`.

### `DELETE /api/folder?vault=<vault>&path=<path>`
Move folder to trash. Same `VRTRASH_<base64>_<unix>` naming as note delete.
**Response:** `{"status": "deleted", "movedTo": ".trash/VRTRASH_…<unix>"}`.

---

## Search & resolution

### `GET /api/search?vault=<vault>&q=<query>[&allVaults=true]`
Search over notes + image attachments in the vault. Results scored and sorted; capped at 20 per vault.

**Query language:**

Plain text (`my note`) does substring matching against filename + title + body. Add operators to filter:

| Operator | Behavior |
|---|---|
| `tag:foo` | Frontmatter tags contain `foo` (substring + hierarchical match) |
| `path:bar` | Vault-relative path contains `bar` |
| `title:baz` | First-H1 title contains `baz` |
| `modified:>7d` | Modified within last N days/weeks/months/years (`d`/`w`/`m`/`y`) |
| `modified:<2026-01-01` | Modified before this absolute date |
| `modified:>2026-01-01` | Modified after this absolute date |
| `modified:=2026-04-29` | Modified on this date |

Operators AND together. Plain text after operators acts as the body substring filter. Operator-only queries (`tag:work modified:>7d`) return everything matching, sorted by recency.

**Scoring** for note hits (kind="note" or omitted):
- exact title equals query: +20
- title substring: +10
- filename substring: +5
- per body occurrence (capped 5): +1
- recency (linear over 30 days): +0–3

**Image attachment hits** (kind="image"): scored at 3 base + 1 if filename starts with the query + recency boost. Strictly lower than note matches so notes rank first when the query matches both.

**Response:**
```json
[
  {"vault": "pessoal", "path": "leituras/Caffeine.md", "title": "Caffeine", "excerpt": "…"},
  {"vault": "pessoal", "path": "misc/screenshot-x.png", "title": "screenshot-x.png", "excerpt": "", "kind": "image"},
  …
]
```

The `kind` field is only set for image results; notes omit it (or set it to `"note"`).

### `GET /api/resolve?vault=<vault>&name=<wikilink-target>`
Resolve a wikilink target to `{vault, path}`.
**Response:**
```json
{"vault": "pessoal", "path": "leituras/Caffeine.md"}
```
404 if no match.

### `GET /api/backlinks?vault=<vault>&path=<path>`
Backlinks for a single note. Reads the in-memory inverted index — no disk read of the note itself, much cheaper than a full `GET /api/note` when you only need the link list (used by the rename-warning flow).
**Response:**
```json
{
  "backlinks": [
    {"vault": "pessoal", "path": "leituras/Coffee.md", "title": "Coffee", "excerpt": "…"}
  ]
}
```

### `GET /api/templates?vault=<vault>`
List `.md` files under `<vault>/templates/` for the "From template…" picker.
**Response:**
```json
{
  "templates": [
    {"path": "templates/Meeting.md", "name": "Meeting", "body": "---\\ndate: {{date}}\\n---\\n\\n# {{title}}\\n"}
  ]
}
```
Empty array if the templates folder doesn't exist.

### `GET /api/file?vault=<vault>&path=<path>`
Serves any file in the vault verbatim. Used by image embeds — the renderer rewrites `![[image.png]]` to `<img src="/api/file?vault=…&path=…">`.

---

## Tags & graph

### `GET /api/tags[?vault=<vault>]`
Aggregate every frontmatter `tags:` / `tag:` value across all notes.
**Response:**
```json
{
  "tags": [
    {"tag": "AI-Processed", "count": 1080, "vaults": ["pcp", "pessoal", "projects", "sosracismo", "work"]},
    {"tag": "jll", "count": 346, "vaults": ["work"]}
  ],
  "total": 463
}
```
Sorted by count desc, then tag asc.

### `GET /api/graph[?vault=<v>][&folder=<f>][&center=<vault:path>][&depth=<N>]`
Wikilink graph. Three scopes (mutually exclusive — `center` wins):

| Mode | Query | Meaning |
|---|---|---|
| All vaults | `(no params)` | Every note across every vault |
| Vault | `?vault=pessoal` | Just that vault |
| Folder | `?vault=pessoal&folder=daily` | Only notes under `daily/` |
| Ego | `?center=pessoal:foo.md&depth=1` | `foo.md` plus everyone within N hops via outbound + inbound |

**Response:**
```json
{
  "nodes": [
    {"id": "pessoal:leituras/Caffeine.md", "label": "Caffeine", "vault": "pessoal", "path": "leituras/Caffeine.md", "refs": 3, "isCenter": true}
  ],
  "edges": [
    {"id": "pessoal:leituras/Caffeine.md->pessoal:leituras/Tea.md",
     "source": "pessoal:leituras/Caffeine.md",
     "target": "pessoal:leituras/Tea.md"}
  ],
  "vault": "pessoal",
  "folder": "",
  "center": "pessoal:leituras/Caffeine.md",
  "depth": 1
}
```

`refs` is the count of incoming edges *within the current scope*. `isCenter` is set on the center node for ego graphs only. Depth is clamped to `[0, 5]`.

---

## Sharing

### `POST /api/shares/create`
Mint a new share token.
**Body:** `{"vault": "...", "path": "...", "writable": false, "ttl": 3600, "label": "..."}`
TTL is in seconds; `0` = never expires.
**Response:** `{"token": "94a75c66...", "url": "/share/94a75c66..."}`.

### `GET /api/shares`
List all active (non-expired) shares.
**Response:** array of `ShareEntry { token, vault, path, writable, created_at, expires_at, label }`.

### `DELETE /api/shares/revoke?token=<token>`
Delete a single share.
**Response:** `{"status": "revoked"}`.

### `DELETE /api/shares/revoke-all`
Bulk-revoke every active share in one call. Returns `{"revoked": <count>}`. Avoids the rate-limit cliff that a per-token loop would hit on 100+ shares.

### `GET /share/<token>`
Public share-link page. Returns the rendered note with no editor, no sidebar, no navigation. If the token is expired or revoked → 404. Includes:
- Inline image embeds (rewritten to `/share/<token>/file?…` URLs).
- Mermaid diagrams + KaTeX math, lazy-loaded from `/share/<token>/asset?…` only when the page actually contains them.

If `writable: true`, the page also includes a CodeMirror editor with autosave (uses `PUT /share/<token>` with the share token as auth).

### `GET /share/<token>/file?path=<path>`
Serves a file inside the shared note's vault. Used by image embeds; the URL rewrite happens server-side in `handleShareView`. Strict checks: `safePath` against the share's vault, **file-extension allowlist** (images / PDF / common audio + video). Anything else returns 403 / 404 — a leaked token cannot be used to read arbitrary `.md` files.

### `GET /share/<token>/asset?name=<name>`
Serves a bundled static asset for the share-page renderer. Strict allowlist:
- `mermaid.min.js`
- `katex.min.js`, `katex.min.css`, `katex-auto-render.min.js`
- `fonts/<KaTeX font>.woff2|woff|ttf`

The KaTeX CSS is post-processed at-serve-time to rewrite relative `url(fonts/…)` to absolute `/share/<token>/asset?name=fonts/…` so fonts load under the same auth context. Anything outside the allowlist → 404, even if it exists in the embedded static bundle.

---

## Trash

### `GET /api/trash?vault=<vault>`
List trash entries for a single vault.
**Response:** `{"items": [{"name": "<original-path>", "path": ".trash/VRTRASH_<b64>_<unix>.md", "isDir": "false"}]}`.

The `name` field is the decoded original vault-relative path (display-friendly). The `path` field is the actual trash entry (use this for restore / delete).

### `POST /api/trash/restore?vault=<vault>&path=<path>`
Move a trash item back to its original location, decoded from the `VRTRASH_<base64>_<unix>` filename. Falls back to the legacy `__→/` decoder for entries created before that scheme existed.
**Response:** `204 No Content`.

### `DELETE /api/trash/empty?vault=<vault>[&path=<path>]`
With `path` → permanently delete one item.
Without `path` → empty the entire vault's `.trash/`.
**Response:** `204 No Content`.

---

## Attachments

### `GET /api/attachments?vault=<vault>`
Walk the vault, return every image with metadata + reference count from a scan of all `.md` files.
**Response:**
```json
{
  "items": [
    {"vault": "pessoal", "path": "misc/foo.png", "name": "foo.png", "size": 12345, "mtime": 1777411898, "ext": ".png", "refCount": 0}
  ],
  "total": 73,
  "orphan": 1
}
```
Sorted: orphans first, then mtime desc.

### `DELETE /api/attachments?vault=<vault>&path=<path>`
Soft-delete to `<vault>/.trash/`. Same convention as note delete.
**Response:** `204 No Content`.

---

## Admin (require `X-Admin-Token` header)

### `GET /api/admin/config`
**Response:** `{"admin_token": "REDACTED", "rw_paths": [...]}` (token redacted in the response).

### `POST /api/admin/config`
**Body:** new config (JSON). Atomic write to `appdata/config.json`.
**Response:** `204 No Content`.

### `POST /api/admin/restart`
Re-execs the binary in place via `syscall.Exec`. Container orchestrator brings it back up automatically (or it just restarts in-process).
**Response:** `204 No Content`.

When `admin_token` is empty, all admin endpoints return `403 admin not configured`.

---

## WebDAV

### `* /webdav/<vault>/<path>`
Read-only WebDAV mount over the entire vaults dir. Method allowlist:
- `GET`, `HEAD` — fetch a file or list a collection
- `OPTIONS` — capabilities (advertises `Dav: 1, 2`)
- `PROPFIND` — enumerate

Mutating verbs (`PUT`, `DELETE`, `MKCOL`, `COPY`, `MOVE`, `LOCK`, `PROPPATCH`, `UNLOCK`) return `405 Method Not Allowed`.

Use case: point Obsidian Mobile, GoodReader, Files.app, or any generic WebDAV client at `https://notes.example.com/webdav/<vault>/`.

The endpoint exposes everything under the vaults dir, including `.obsidian/`, `.trash/`, `.smart-env/`. Filter at your reverse proxy if you want to hide them.

---

## Static assets

Anything not matched by the routes above falls through to `http.FileServer(http.FS(staticFiles))`:

- `/index.html`, `/style.css` — the SPA shell
- `/codemirror.bundle.js`, `/mermaid.min.js`, `/katex.min.js`, `/katex-auto-render.min.js`, `/cytoscape.min.js`, `/alpine.min.js` — third-party libs
- `/katex.min.css`, `/fonts/*.woff2|.woff|.ttf` — KaTeX styling
- `/obsidian.svg` — fallback vault icon

Served straight from the embedded FS — no disk read, no network.

---

## Headers honored

- `X-Real-IP`, `X-Forwarded-For` — used by the rate limiter to identify the real client behind a proxy.
- `X-Admin-Token` — admin auth.
- `Content-Type` — required on uploads (must start with `image/`).
- `If-Match`, `If-None-Match` — **not** honored. Conflict detection uses `?ifMTime=` query param instead.

## Headers emitted

- `Content-Type: application/json` for all JSON responses.
- `Content-Type: text/html; charset=utf-8` for the SPA shell.
- `Content-Type: <inferred>` for `/api/file` and `/api/vault-icon`.
- Gzip is applied transparently by the `gzipMiddleware` when the request advertises `Accept-Encoding: gzip`.

## Status codes used

| Code | When |
|---|---|
| 200 | Successful GET / PUT |
| 201 | Successful note/folder create |
| 204 | Successful destructive op (delete, restore, etc.) |
| 400 | Invalid vault/path/body shape |
| 403 | Admin auth failure or write to a non-RW path |
| 404 | Note / share / file not found |
| 405 | Wrong HTTP method on a method-restricted endpoint |
| 409 | Conflict (note already exists, or file changed on disk) |
| 500 | Server error (rare; usually fs problems) |
