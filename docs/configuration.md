# Configuration

VaultReader has very few knobs. Most behavior is driven by the directory structure under `/vaults` and the JSON files under `/appdata`.

## Command-line flags

| Flag | Default | Description |
|---|---|---|
| `-vaults` | `/vaults` | Path to the directory containing one subdirectory per vault. |
| `-appdata` | `/appdata` | Path to the writable runtime data directory (config, shares, icons). |
| `-port` | `8080` | TCP port to listen on. |

Inside the Docker image these are the defaults; mount real paths over them.

## Environment variables

| Var | Required | Description |
|---|---|---|
| `SYNCTHING_API_KEY` | no | API key for your Syncthing instance. Without it, the sync indicator stays in `unknown` state. |
| `SYNCTHING_API_URL` | no | URL of the Syncthing API (e.g. `https://syncthing:8384`). TLS verification is skipped (`InsecureSkipVerify: true`) for self-signed setups. |

## `appdata/` layout

```
appdata/
├── config.json     — admin token, RW paths, per-instance options
├── shares.json     — active share tokens (auto-managed, do not edit by hand)
├── audit.jsonl     — append-only log of who changed what (auto-managed)
└── icons/
    ├── personal.png
    ├── work.svg
    └── …
```

This directory is not in git. Mount it as a writable volume in production.

### `appdata/config.json`

```json
{
  "admin_token": "long-random-string-or-empty",
  "rw_paths": [
    "personal",
    "personal/agents",
    "work/team/active"
  ]
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `admin_token` | string | `""` | Required for `/api/admin/*` endpoints. Empty disables the admin API entirely (returns `403 admin not configured`). |
| `rw_paths` | string[] | `[]` | Path prefixes (vault-relative) that allow writes. Read-only by default — adding `"personal"` makes the entire `personal` vault writable; adding `"personal/agents"` only allows writes inside that subtree. |

#### Per-instance options (all optional)

Defaults reproduce the original single-user behaviour, so an existing `config.json` keeps working unchanged. These let one image serve different audiences (main instance vs. team instance).

```json
{
  "require_user": true,
  "rw_paths": ["team"],
  "share_base_url": "https://team.example.com/share/",
  "default_note": "",
  "features": {
    "shares": true,
    "share_writable": false,
    "webdav": false,
    "sync_status": false,
    "trash_empty": false
  }
}
```

| Field | Default | Team instance | Effect |
|---|---|---|---|
| `require_user` | `false` | `true` | `401` for any request without a non-empty `Remote-User` header, except `/share/*` and `/health`. Fail-closed for instances that must only be reached through the auth proxy |
| `share_base_url` | `""` (derived) | `https://team.example.com/share/` | Prefix the UI puts in front of a share token. Must end with `/`. Empty = derived from the request as `<scheme>://<host><base_path>/share/` (scheme from `X-Forwarded-Proto` when the peer is a trusted proxy, else TLS state); set it for a public alias such as `https://example.com/notes/` |
| `base_path` | `""` | `""` | Public URL prefix when served under a sub-path, e.g. `"/vaultreader"` → `https://example.com/vaultreader/`. Leading `/`, no trailing `/`, no `.`/`..` segments, URL-safe characters only. Requests are accepted with or without the prefix (so a proxy may strip it or pass it through); redirects, note/share/WebDAV links and the derived `share_base_url` include it |
| `default_note` | `""` | `""` | `"vault/path.md"` opened when no note is restored (e.g. `personal/inbox/todo.md`). Must contain a `/` |
| `demo_mode` | `false` | `false` | `true` → the UI shows a slim, per-session dismissible banner "Public demo — changes are reset every hour". Display only: it does not reset anything; schedule the reset yourself |
| `analytics` | absent | absent | `{"umami_script": "/js/u/script.js", "umami_website_id": "<uuid>"}` → the SPA shell's `<head>` gets `<script defer src="…" data-website-id="…">` for [Umami](https://umami.is). Both keys required. `umami_script` is a site-root path (leading `/`, never given the `base_path` prefix — point it at your proxy's Umami route) or an `https://` URL, whose origin is then added to the shell CSP's `script-src` and `connect-src`; a same-origin path needs no CSP change. `umami_website_id` must be a UUID. Not added to `/share/*` pages |
| `vault_order` | `[]` | `[]` | Vault names listed first in the sidebar and stats, in this order (absent names skipped); the rest follow alphabetically |
| `features.shares` | `true` | `true` | `/api/shares*` and `/share/*` |
| `features.share_writable` | `true` | `false` | `false` → creating a share with `writable: true` returns `403`; existing writable shares are served read-only |
| `features.webdav` | `true` | `false` | `/webdav/` |
| `features.sync_status` | `true` | `false` | `/api/sync-status` |
| `features.trash_empty` | `true` | `false` | `/api/trash/empty` |

A disabled feature's route answers **404** (as if it did not exist) and the UI hides the matching controls. The resolved flags, `rw_paths`, `share_base_url`, `base_path`, `default_note`, `demo_mode`, current user and build version are exposed at `GET /api/instance`.

**Validation is fail-fast at startup:** an unknown key (top-level or under `features`, e.g. a misspelt `require_usr`), a `default_note` without a `/`, a `share_base_url` that does not end in `/`, a malformed `base_path`, or an incomplete or malformed `analytics` block makes the process exit with a `config:` error (check `docker logs`). The admin `POST /api/admin/config` only edits `admin_token` and `rw_paths`; change the options above by editing the file and restarting.

`Remote-User` identifies the actor in `audit.jsonl` and in each share's `created_by`. On an instance without `require_user` it is attribution only — see [security.md](security.md).

Writes affected by `rw_paths`:
- `PUT /api/note` (save note content)
- `POST /api/note` (create new note)
- `DELETE /api/note` (move to trash)
- `POST /api/upload` (image upload)
- `POST /api/move`, `POST /api/folder`, `DELETE /api/folder`
- `DELETE /api/attachments`
- `POST /api/trash/restore` (the destination must be writable)

Reads (everything else) are always allowed.

### Generating the admin token

```bash
openssl rand -hex 32
```

Drop the result into `appdata/config.json` as `admin_token`. Restart (or wait for atomic config write to take effect — the server polls `config.json` mtime).

The token is compared with `subtle.ConstantTimeCompare` against the `X-Admin-Token` header. There's no in-browser flow to set this (yet); use curl:

```bash
curl -H "X-Admin-Token: $TOKEN" \
     https://notes.example.com/api/admin/config
```

### `appdata/shares.json`

Auto-managed. Do not edit by hand — the format is:

```json
{
  "entries": [
    {
      "token": "94a75c66...",
      "vault": "personal",
      "path": "travel/Lisbon trip.md",
      "writable": false,
      "created_at": 1775590996,
      "expires_at": 0,
      "label": "For Bob"
    }
  ]
}
```

`expires_at: 0` = never expires. Otherwise unix seconds.

To manually nuke all shares: delete the file and restart.

### `appdata/icons/`

Drop image files named after each vault. Supported extensions: `.png`, `.svg`, `.jpg`, `.jpeg`, `.webp`. The first matching file wins.

If no icon exists, a generic folder SVG is shown.

Icons are served live (no restart) via `/api/vault-icon?vault=<name>`.

## Per-vault conventions

These aren't configured globally — they're conventions VaultReader looks for inside each vault directory.

### `<vault>/templates/*.md`

Any `.md` file under a vault's `templates/` folder appears in the toolbar's `+ New` → "From template…" picker. The picker shows each template's name (basename without `.md`) plus a one-line preview of the body.

Selecting a template prompts for a new note name; the template's body is then expanded with these placeholders before creation:

| Placeholder | Replaced with |
|---|---|
| `{{date}}` | Today as `YYYY-MM-DD` (local time) |
| `{{date:FMT}}` | Custom format using `YYYY MM DD HH mm ss` tokens |
| `{{time}}` | `HH:mm` |
| `{{title}}` | The new note's name (sans `.md`) |

Obsidian's Templater syntax (`<% tp.date.now(...) %>`) renders literally — VaultReader doesn't parse it. Use the placeholder syntax above for cross-tool-aware templates if you want the same file to work in both VaultReader and Obsidian (Templater will run its own pass on top).

### `appdata/audit.jsonl`

One JSON line per mutation, appended by the app: `{"ts":…,"user":"alice","action":"save","vault":"team","path":"project/x.md","to":"","bytes":1234}`. Actions: `save`, `create`, `upload`, `move`, `delete`, `restore`, `share`, `revoke`. VaultReader itself never runs git. If you want per-author history, a host-side timer can read the file from a stored byte offset and turn new lines into git commits (see [team-instance.md](team-instance.md#per-author-git-history)). Such a reader should restart from byte 0 when the file is smaller than its stored offset, so rotating or truncating the log stays safe.

### `<vault>/.trash/`

Soft-deleted files land here under `VRTRASH_<base64url(originalPath)>_<unix><ext>` filenames. Auto-managed; safe to delete the whole directory if you want to reclaim space and don't need restore.

### `<vault>/<note-dir>/attachments/`

Pasted/dropped images go here per-note. E.g. pasting into `notes/foo.md` creates `notes/attachments/foo-<unix>.<ext>`. Matches Obsidian's default attachment convention.

## Reverse proxy notes

### Authelia / forward-auth

VaultReader doesn't have its own user system. Put it behind any forward-auth proxy:

```yaml
# Traefik labels
labels:
  - traefik.enable=true
  - traefik.http.routers.vaultreader.rule=Host(`notes.example.com`)
  - traefik.http.routers.vaultreader.middlewares=authelia@docker
  - traefik.http.services.vaultreader.loadbalancer.server.port=8080
```

**Important:** do NOT gate `/share/<token>` behind auth. Share links are intended to be publicly accessible (token-based). Configure a path-rule exemption:

```yaml
labels:
  - traefik.http.routers.vaultreader-shares.rule=Host(`notes.example.com`) && PathPrefix(`/share/`)
  - traefik.http.routers.vaultreader-shares.middlewares=  # no auth
  - traefik.http.routers.vaultreader-shares.priority=200  # higher than the auth router
```

See [authelia.md](authelia.md) for a complete example.

### `X-Real-IP` / `X-Forwarded-For`

Make sure your proxy sets one of these. The rate limiter uses the left-most hop from `X-Forwarded-For`, falling back to `X-Real-IP`, falling back to the TCP remote address. Without it, every user behind the same proxy shares the same rate-limit bucket.

Traefik does this automatically. Caddy does too. Nginx needs:

```nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

### Custom share URL prefix (e.g. `/notas/`)

VaultReader serves shares at `/share/<token>`. To rewrite that to `/notas/<token>` at the proxy:

```yaml
# Traefik
http:
  middlewares:
    notas-rewrite:
      replacePathRegex:
        regex: ^/notas/(.*)$
        replacement: /share/$1
  routers:
    notas:
      rule: PathPrefix(`/notas/`)
      middlewares: [notas-rewrite]
      service: vaultreader
```

Set `share_base_url` in `config.json` to match your prefix (e.g. `https://notes.example.com/share/`); the UI reads it from `/api/instance`. The only prefix left in `web/static/index.html` is the built-in default used until that response arrives.

## Tuning

### Rate limit

Hardcoded at 240 req/min per IP in `routes.go` (`handler()`). Bump if needed and rebuild — there's no env var.

### Body cap (uploads)

Hardcoded 10MB in `handleUpload`. Same — change in source.

### Search results cap

Hardcoded 20 per vault in `searchVault`. Same.

### Mermaid bundle size

The bundled `mermaid.min.js` is 3.1MB. To trim, use a Mermaid build with only the diagram types you use:
1. Build a custom bundle on a machine with Node.
2. Drop it in `web/static/mermaid.min.js`.
3. Rebuild.

### KaTeX fonts

`web/static/fonts/` has all 60 font files (TTF + WOFF + WOFF2 for each of 20 fonts). To save ~700KB, you can keep only the `.woff2` variants — modern browsers all support woff2. The `.css` will throw 404s for the missing variants but degrade silently.

## CI / release

`.github/workflows/ci.yml` runs `go vet`, `go test` and the inline-script check on every push and pull request, then builds the image (no push). `.github/workflows/release.yml` builds and publishes the multi-arch image to `ghcr.io/joaompfp/vaultreader` when a `v*` tag is pushed. To build an image locally: `make image`.
