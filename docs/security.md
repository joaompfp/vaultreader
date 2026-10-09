# Security model

VaultReader is a personal-vault tool. The threat model below assumes:
- **Trusted operator** — the person deploying owns the vaults and trusts themselves.
- **Single operator, optionally one small trusted team per instance** — behind a forward-auth proxy (Authelia, oauth2-proxy, Tailscale, etc.). Everyone who can reach an instance can read everything that instance mounts and write wherever its `rw_paths` allow.
- **Untrusted internet** — anyone may probe `/share/<token>` URLs.

Isolation between audiences is done **per instance**, not per user: a second container mounts only the folder that audience may see (e.g. `work/team` as vault `team`), sits on its own Docker bridge, and sets `require_user: true`. There are no per-user ACLs inside one process.

It is **not** designed for:
- Multi-tenant deployments where users of *one* instance have different access levels (run one instance per audience instead).
- Sharing a deployment with strangers without a reverse proxy.
- Hostile-input scenarios where the vault contents themselves are adversarial.

### Guiding principle: data is private, the system is not
Security never relies on obscurity. Code, compose files, proxy layout and docs may be shared freely; access control alone protects the **data** (notes outside the mounted folder, `admin_token`, API keys, `appdata/` of other instances). 404 for disabled routes is UI consistency, not a security control.

### Team instance model
| Concern | Mechanism |
|---|---|
| What the team can see | Mount isolation: the instance only has the team folder under `/vaults` |
| Who can reach it | Dedicated bridge network (e.g. `vr_team_net`), not the shared proxy network; containers on other networks cannot connect. Traefik (host network) still can |
| Fail-closed identity | `require_user: true` → every request except `/share/*` and `/health` without a non-empty `Remote-User` gets `401` |
| Feature surface | `features.*` flags remove routes at registration (404): WebDAV, sync status, trash purge, writable shares |
| Authorization at the proxy | Authelia rule per group, plus a `deny` wall for the `external` group on every other `*.example.com` host (see [authelia.md](authelia.md)) |

`Remote-User` is set by Traefik forwardAuth, which overwrites any client-supplied value. On an instance **without** `require_user` (the main one, reachable from every container on the shared proxy network) the header is **attribution-only**: it labels audit lines and share `created_by`, and is never used for authorization. A spoofed header there can only mislabel an audit line.

## Trust boundaries

```
Public internet
  │
  ▼
Reverse proxy (Authelia / oauth2-proxy / cloudflared / Tailscale Funnel)
  │  ←  here is where SSL terminates and where most auth happens
  ▼
VaultReader container
  │  ←  here is where rw_paths + admin_token apply
  ▼
Filesystem (vaults + appdata)
```

VaultReader assumes the proxy in front of it has done the user-auth. Its own protections are layered on top, not as a replacement.

## Auth surfaces

### Reverse-proxy auth (primary)
**Recommended.** Put Authelia / oauth2-proxy / Cloudflare Access in front. VaultReader has no user database and no sessions: identity is the `Remote-User` header the proxy forwards, used for attribution (and, with `require_user: true`, as a fail-closed presence check). Anyone who reaches the container can read everything it mounts and write where `rw_paths` allows.

> **Open follow-up:** `/api/shares/create|revoke|revoke-all` and `GET /api/shares` are unauthenticated inside the app and rely entirely on the proxy. They are not authenticated on the main instance because in-network automation clients use them today.

### Admin token (secondary)
For **admin endpoints only** (`/api/admin/*`):

- Generate: `openssl rand -hex 32`
- Configure: drop into `appdata/config.json` → `admin_token`
- Use: `X-Admin-Token: <token>` header on every admin request

If `admin_token` is empty, all admin endpoints return `403 admin not configured`. The browser UI doesn't expose an interactive way to set the header — admin operations require curl. This is intentional friction.

Compared with `subtle.ConstantTimeCompare` to prevent timing attacks.

### Share-link tokens (delegated read access)
Each share is a 24-hex-character token from `crypto/rand` that grants:
- **Read** access to one specific note (path baked into the token's record).
- Optionally **write** access if `writable: true` was set at creation.
- For the duration of `expires_at` (`0` = never).

Tokens are stored server-side in `appdata/shares.json`. They're not signed — possession is sufficient. **Anyone with the URL can access the shared note**, including following a forwarded email or browser-history-leaked link.

Use `expires_at` for sensitive shares. Treat share URLs like API keys.

### Rate limit
240 requests/minute per IP, sliding window. Identifies the IP via `X-Real-IP`, falling back to `X-Forwarded-For` (left-most hop), falling back to `r.RemoteAddr`.

GET/HEAD of `/api/file`, `/api/vault-icon` and embedded static assets are exempt (`rateExempt` in `routes.go`): they are read-only and one page view of an image-heavy note fans out into hundreds of them. Every other request — all mutating `/api/*`, `/share/*`, `/webdav/`, the shell — counts.

This is a soft DoS mitigation, not a security boundary. A determined attacker can rotate IPs.

## Data plane

### Path traversal
Every endpoint that takes a `path` parameter runs it through `safePath(vault, path)`:

```go
func (s *server) safePath(vaultP, notePath string) (string, bool) {
    if notePath == "" || strings.HasPrefix(notePath, "/") || strings.HasPrefix(notePath, "\\") {
        return "", false
    }
    full := filepath.Clean(filepath.Join(vaultP, notePath))
    rel, err := filepath.Rel(filepath.Clean(vaultP), full)
    if err != nil || strings.HasPrefix(rel, "..") {
        return "", false
    }
    if full != vaultP && !strings.HasPrefix(full, vaultP+string(filepath.Separator)) {
        return "", false
    }
    return full, true
}
```

This blocks `../etc/passwd`, absolute paths, and Windows-style `\foo\bar`. Verified by integration test against `?path=../etc/foo.md` returning 400.

### Writable paths
`isWritable(vault, path)` checks the supplied `vault/path` against every entry in `rw_paths`:

```go
for _, rw := range rwPaths {
    if rw == vault || full == rw || strings.HasPrefix(full, rw+"/") {
        return true
    }
}
return false
```

When the list is empty, every write returns 403. Add `"personal"` to allow the entire `personal` vault. Add `"personal/agents/skills"` to only allow writes inside that subtree.

This applies to:
- `PUT /api/note`, `POST /api/note`, `DELETE /api/note`
- `POST /api/upload`
- `POST /api/move`, `POST /api/folder`, `DELETE /api/folder`
- `DELETE /api/attachments`

### Body caps
- **Note PUT**: no explicit cap, relies on the rate limiter and Go's default `MaxBytesReader` (none). A pathological client could flood with huge bodies. Add a cap if your deployment is exposed.
- **Upload POST**: 10MB hard cap via `http.MaxBytesReader`.
- **Admin POST**: 32KB cap.

### Atomic config write
`appdata/config.json` and `appdata/shares.json` are written via tempfile + `os.Rename` — never partial-write a corrupted config. Crash during write leaves the previous version intact.

### Trash naming
Soft-deleted files use the `VRTRASH_<base64url(originalPath)>_<unix><ext>` scheme. The base64-encoded full path makes restore exact for any filename — no round-trip ambiguity, even for paths containing `__` or starting with `_`. The legacy `__→/` flatten scheme is still readable for entries created before this change (via `legacyDecodeTrashName`) but new entries use the safer scheme.

This isn't an authorization boundary — anything in `.trash/` was already deletable by the user — but it's a correctness property: undo via the toast UI relies on the original path being recoverable.

### Trash restore hardening
`POST /api/trash/restore` used to trust the supplied `path` and the original path decoded from the trash filename, so a crafted request could move arbitrary files or restore into non-writable areas. It now only accepts `.trash/<name>` (a direct child of the vault's `.trash/`), validates both source and decoded destination with `safePath`, and requires `isWritable` on the **destination**.

Restore never overwrites: if anything exists at the decoded destination (e.g. a note recreated after the delete) it answers `409` and leaves both files untouched.

### Note HTML sanitisation
Markdown is rendered server-side with raw HTML allowed (goldmark `WithUnsafe`) because real notes use `<img>`, `<div>`, `<table>`, `<details>` etc. That makes every note author able to emit `<script>` / `onerror=` — and on the team instance the author is a colleague whose notes the owner opens on the main instance, an origin with access to every vault. So the SPA treats note HTML as untrusted:

- `static/purify.min.js` (DOMPurify, vendored, no build step) loads before the app; `vaultApp()._safeHtml()` sanitises every vault-derived HTML sink — each `noteHtml` assignment, the iOS `innerHTML` fallback, the outline parser, search excerpts. It **fails closed**: without DOMPurify nothing is rendered. Wikilink `data-*`, callouts, `style`, tables, `<details>` and `language-mermaid` code blocks survive; scripts, event-handler attributes and `javascript:` URLs do not.
- Mermaid runs with `securityLevel: 'strict'`, and its parse-error text (which echoes diagram source) is inserted as text, never HTML.
- The share page inlines the rendered note server-side, so it is served with `Content-Security-Policy: script-src 'nonce-<random>'; object-src 'none'; base-uri 'self'; form-action 'none'; frame-src 'none'; child-src 'none'`: only the page's own KaTeX/Mermaid scripts carry the nonce, so script in a note is inert there, and a note cannot frame `/api/file` or anything else. The page title is HTML-escaped.

### Raw vault files
`/api/file`, `/share/<token>/file` and direct file shares stream vault bytes. They always send `X-Content-Type-Options: nosniff` and a `Content-Type` chosen by the server (never sniffed, never from `/etc/mime.types`): images, PDF, audio and video get their own type from a hardcoded map; everything else — `.md`, text, source, markup, extensionless, unknown — gets `Content-Security-Policy: sandbox` (opaque origin: no cookies, no same-origin API access) and is served as `text/plain; charset=utf-8` (text-like extensions, including `.html`/`.xml`), `image/svg+xml` (`.svg`, so `<img>` embeds keep working) or `application/octet-stream`.

### Dot-directories and the index
The index and rescanner never descend into dot-directories (`.trash`, `.git`, `.obsidian`, `.vr`, …). Before this, `.trash` contents were searchable — a deleted note could still turn up in search results. Symlinked *files* are indexed; symlinked directories are not followed. (A symlink inside a vault pointing at an out-of-vault regular file is indexed — only the host or Syncthing can create one; the team user has no API to.)

### DOCX template listing
`GET /api/templates/docx` walks the note's ancestor directories looking for `.vr/*.docx|dotx`; each ancestor is now confined to the vault with `safePath`, so a `path=../..` can no longer list templates outside it.

### Save normalization
Every write through `saveNote` runs `normalizeMarkdown`: strip trailing whitespace per line, ensure exactly one trailing newline. This is a quality-of-life behavior for git diffs (round-tripping cleanly between editors), not a security control.

### Directory listing
`http.FileServer` over the embedded FS doesn't allow directory listings (Go's default behavior emits 404 for missing paths but lists for collections). For the **WebDAV** mount, listing IS exposed via `PROPFIND` — that's the protocol's whole point. WebDAV GET/HEAD use the same server-chosen `Content-Type` as `/api/file`, and every WebDAV response carries `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`.

### Share-asset allowlist
The `/share/<token>/asset?name=<X>` route serves bundled JS/CSS/fonts so shared notes can render mermaid + KaTeX. The allowlist is **strict by name**:

```go
var shareAssetAllowlist = map[string]string{
    "mermaid.min.js":           "static/mermaid.min.js",
    "katex.min.js":             "static/katex.min.js",
    "katex.min.css":            "static/katex.min.css",
    "katex-auto-render.min.js": "static/katex-auto-render.min.js",
}
```

Plus a regex-equivalent allow for `fonts/<name>.{woff2,woff,ttf}`. Anything else returns 404 — even files that exist in the embedded FS (`index.html`, `style.css`, `logo.svg`, etc.). A leaked share token cannot be used to fetch the SPA shell or any other private static file via this route.

### Share-file scoping
The `/share/<token>/file?path=<X>` route (used by image embeds in shared notes) enforces **two layers**:
1. `safePath` against the share's vault — blocks path traversal.
2. **File-extension allowlist** — only images, PDF, common audio + video formats. Notes (`.md`) and arbitrary other files return 403, so a leaked token can't be used to read other notes via this route.

## Headers

VaultReader sets the content-level headers described above (`nosniff` + `sandbox` on raw files, a nonce CSP on the share page) and, on the SPA shell (`/` and `/n/*`, `spaCSP` in `vaults.go`), a second layer behind DOMPurify:

```
Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'
X-Content-Type-Options: nosniff
Referrer-Policy: same-origin
```

`script-src` keeps `'unsafe-inline'` (the inline `<script>` blocks have no nonce strategy without a build step) and `'unsafe-eval'` (the bundled Alpine build compiles expressions with `Function`). The value is in the rest: `connect-src 'self'` stops fetch/XHR exfiltration, `form-action`/`base-uri`/`object-src` close form, `<base>` and plugin hijacks, and `frame-ancestors 'self'` prevents clickjacking. `img-src` allows `https:` because notes embed external images. `/api/*` and `/webdav/` carry no shell CSP. HSTS is the proxy's job.

## What's deliberately weak

- **No request signing.** Share links are bearer tokens — no HMAC, no rotation. Replays work.
- **No login flow.** VaultReader has no notion of session. The proxy's session is the session.
- **No file watcher.** If you edit a note via WebDAV (write) it would race the in-memory index. (Currently moot since WebDAV is read-only.)
- **Audit log is write-only attribution, not forensics.** Each instance appends one JSON line per mutation to `appdata/audit.jsonl` (`{ts,user,action,vault,path,to?,bytes?}`; actions `save create upload move delete restore share revoke`). Reads are not logged, nothing is signed, and the file can be edited by anyone with host access. A failed append is logged and the request still succeeds (attribution is not security). A host timer turns it into per-author git commits (`vr(<user>)`). `trash/empty` (main instance only; disabled on the team instance) and admin config changes are not audited.
- **No CSRF token.** Mutating endpoints rely on browser same-origin policy. If you put a CORS-permissive proxy in front, you've broken this.
- **`appdata/shares.json` keeps revoked entries** in the file (the in-memory store filters them on every list, but the JSON contains all entries until next save).
- **WebDAV exposes `.obsidian/`, `.smart-env/`, `.trash/`** — internal directories. Filter at the proxy if you need to hide them.

## Recommended deployment posture

1. **Reverse proxy with forward auth** (Authelia, oauth2-proxy). Don't expose VaultReader directly.
2. **Path exemption for `/share/`** so shared links don't require login.
3. **No exemption for `/webdav/`** — gate behind the same auth as the rest unless you specifically want anonymous WebDAV (don't).
4. **Set `admin_token`** to a long random string. Never commit it.
5. **Use `expires_at` on share links** for anything sensitive.
6. **`rw_paths` only what you need to edit from the web.** Vaults you only browse don't need to be writable.
7. **Run as non-root** in the container: the image runs as root unless you set `user: "1000:1000"` in compose (recommended for every instance).
8. **Mount `/vaults` read-only** if you only want web-side reading. `rw_paths` becomes a no-op then.
9. **One instance per audience.** To give someone access to a subset, mount just that subset into a second container on its own network with `require_user: true` and the feature flags you need off. Deploy all instances from the same image so they run the same build (compare `/health` versions).

## Reporting issues

For security issues, **don't open a public GitHub issue**. Email the maintainer directly (see `git log --format='%ae' | sort -u`). For everything else, GitHub issues are fine.
