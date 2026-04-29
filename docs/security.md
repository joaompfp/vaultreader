# Security model

VaultReader is a personal-vault tool. The threat model below assumes:
- **Trusted operator** — the person deploying owns the vaults and trusts themselves.
- **Single primary user** behind a forward-auth proxy (Authelia, oauth2-proxy, Tailscale, etc.).
- **Untrusted internet** — anyone may probe `/share/<token>` URLs.

It is **not** designed for:
- Multi-tenant deployments where users have different access levels.
- Sharing a deployment with strangers without a reverse proxy.
- Hostile-input scenarios where the vault contents themselves are adversarial.

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
**Recommended.** Put Authelia / oauth2-proxy / Cloudflare Access in front. VaultReader has no concept of users. Anyone who reaches the container can read everything, write where `rw_paths` allows, and create share links if they have an admin token.

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

When the list is empty, every write returns 403. Add `"pessoal"` to allow the entire `pessoal` vault. Add `"pessoal/agents/hermes/skills"` to only allow writes inside that subtree.

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

### Directory listing
`http.FileServer` over the embedded FS doesn't allow directory listings (Go's default behavior emits 404 for missing paths but lists for collections). For the **WebDAV** mount, listing IS exposed via `PROPFIND` — that's the protocol's whole point.

## Headers

VaultReader doesn't set CSP / HSTS / X-Frame-Options itself — that's the proxy's job. If you're deploying without a proxy (don't), at minimum add:

```nginx
add_header X-Frame-Options "DENY";
add_header Referrer-Policy "strict-origin-when-cross-origin";
add_header Content-Security-Policy "default-src 'self' 'unsafe-inline' data:; script-src 'self'; style-src 'self' 'unsafe-inline';";
```

Note: the inline `<script>` blocks in `index.html` require `'unsafe-inline'` for scripts — there's no nonce strategy. CodeMirror / Mermaid / KaTeX also use eval/Function dynamically. If strict CSP is required, adopt a build-step that nonces the inlines.

## What's deliberately weak

- **No request signing.** Share links are bearer tokens — no HMAC, no rotation. Replays work.
- **No login flow.** VaultReader has no notion of session. The proxy's session is the session.
- **No file watcher.** If you edit a note via WebDAV (write) it would race the in-memory index. (Currently moot since WebDAV is read-only.)
- **No audit log.** There's no record of who read what, who shared what, or who changed `rw_paths`.
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
7. **Run as non-root** in the container (the scratch image runs as root by default; consider `USER nobody`).
8. **Mount `/vaults` read-only** if you only want web-side reading. `rw_paths` becomes a no-op then.

## Reporting issues

For security issues, **don't open a public GitHub issue**. Email the maintainer directly (see `git log --format='%ae' | sort -u`). For everything else, GitHub issues are fine.
