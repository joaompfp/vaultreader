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
├── config.json     — admin token + RW paths
├── shares.json     — active share tokens (auto-managed, do not edit by hand)
└── icons/
    ├── pessoal.png
    ├── work.svg
    └── …
```

This directory is not in git. Mount it as a writable volume in production.

### `appdata/config.json`

```json
{
  "admin_token": "long-random-string-or-empty",
  "rw_paths": [
    "pessoal",
    "pessoal/agents",
    "work/jll/active"
  ]
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `admin_token` | string | `""` | Required for `/api/admin/*` endpoints. Empty disables the admin API entirely (returns `403 admin not configured`). |
| `rw_paths` | string[] | `[]` | Path prefixes (vault-relative) that allow writes. Read-only by default — adding `"pessoal"` makes the entire `pessoal` vault writable; adding `"pessoal/agents"` only allows writes inside that subtree. |

Writes affected by `rw_paths`:
- `PUT /api/note` (save note content)
- `POST /api/note` (create new note)
- `DELETE /api/note` (move to trash)
- `POST /api/upload` (image upload)
- `POST /api/move`, `POST /api/folder`, `DELETE /api/folder`
- `DELETE /api/attachments`

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
      "vault": "pessoal",
      "path": "viagens/Lisbon trip.md",
      "writable": false,
      "created_at": 1775590996,
      "expires_at": 0,
      "label": "For Joana"
    }
  ]
}
```

`expires_at: 0` = never expires. Otherwise unix seconds.

To manually nuke all shares: delete the file and restart.

### `appdata/icons/`

Drop image files named after each vault. Supported extensions: `.png`, `.svg`, `.jpg`, `.jpeg`, `.webp`. The first matching file wins.

If no icon exists, a generic folder SVG is shown.

Icons are served live (no restart) via `/api/vault-icon?name=<vault>`.

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

The share-link generation in the UI hardcodes `/notas/<token>` for `joao.date`; if you fork, update `static/index.html` to match your prefix.

## Tuning

### Rate limit

Hardcoded at 240 req/min per IP in `main.go:2080`-ish. Bump if needed and rebuild — there's no env var.

### Body cap (uploads)

Hardcoded 10MB in `handleUpload`. Same — change in source.

### Search results cap

Hardcoded 20 per vault in `searchVault`. Same.

### Mermaid bundle size

The bundled `mermaid.min.js` is 3.1MB. To trim, use a Mermaid build with only the diagram types you use:
1. Build a custom bundle on a machine with Node.
2. Drop it in `static/mermaid.min.js`.
3. Rebuild.

### KaTeX fonts

`static/fonts/` has all 60 font files (TTF + WOFF + WOFF2 for each of 20 fonts). To save ~700KB, you can keep only the `.woff2` variants — modern browsers all support woff2. The `.css` will throw 404s for the missing variants but degrade silently.

## CI / release

There's no automated release. Build + push manually:

```bash
docker build -t ghcr.io/joaompfp/vaultreader:latest -t ghcr.io/joaompfp/vaultreader:$(git rev-parse --short HEAD) .
docker push ghcr.io/joaompfp/vaultreader:latest
docker push ghcr.io/joaompfp/vaultreader:$(git rev-parse --short HEAD)
```

GitHub Actions workflow in `.github/workflows/` if/when added.
