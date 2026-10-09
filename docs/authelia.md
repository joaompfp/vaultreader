# Deploying behind Authelia

VaultReader has no built-in user system; it expects a forward-auth proxy in front of it. This guide covers the most common setup: **Traefik + Authelia**, with public exemptions for share links.

The principles transfer to other reverse proxies (Caddy, Nginx) and other forward-auth providers (oauth2-proxy, authentik, Pomerium).

## Goals

- Authenticated users get full access to `notes.example.com`.
- Public users hitting `notes.example.com/share/<token>` (or your custom prefix like `/notas/<token>`) get the read-only share-link page **without** an Authelia challenge.
- Everything else is gated.

## docker-compose.yml — Traefik labels

```yaml
services:
  vaultreader:
    image: ghcr.io/joaompfp/vaultreader:latest
    container_name: vaultreader
    volumes:
      - /srv/vaults:/vaults:rw
      - /srv/vaultreader-appdata:/appdata:rw
    environment:
      SYNCTHING_API_KEY: ${SYNCTHING_API_KEY}
      SYNCTHING_API_URL: https://syncthing:8384
    networks: [proxy]
    labels:
      - traefik.enable=true

      # ─── Authenticated router (everything except /share/* and /notas/*) ─────
      - traefik.http.routers.vaultreader.rule=Host(`notes.example.com`)
      - traefik.http.routers.vaultreader.entrypoints=websecure
      - traefik.http.routers.vaultreader.tls.certresolver=letsencrypt
      - traefik.http.routers.vaultreader.middlewares=authelia@docker
      - traefik.http.routers.vaultreader.priority=100

      # ─── Public share-link router (no auth) ─────────────────────────────────
      - traefik.http.routers.vaultreader-shares.rule=Host(`notes.example.com`) && (PathPrefix(`/share/`) || PathPrefix(`/notas/`))
      - traefik.http.routers.vaultreader-shares.entrypoints=websecure
      - traefik.http.routers.vaultreader-shares.tls.certresolver=letsencrypt
      - traefik.http.routers.vaultreader-shares.middlewares=notas-rewrite@docker
      - traefik.http.routers.vaultreader-shares.priority=200          # higher than the auth router

      # /notas/<token> → /share/<token>
      - traefik.http.middlewares.notas-rewrite.replacePathRegex.regex=^/notas/(.*)$$
      - traefik.http.middlewares.notas-rewrite.replacePathRegex.replacement=/share/$1

      # Service
      - traefik.http.services.vaultreader.loadbalancer.server.port=8080

networks:
  proxy:
    external: true
```

Key bits:
- The two routers share a host, but the more specific `vaultreader-shares` router has `priority=200` so it wins for `/share/*` and `/notas/*`.
- The `vaultreader-shares` router has **no authelia middleware** — those paths are public.
- `notas-rewrite` is optional — only if you want a friendlier prefix.

## Authelia config — `access_control`

```yaml
access_control:
  default_policy: deny
  rules:
    # Public share links
    - domain: notes.example.com
      resources:
        - '^/share/.*$'
        - '^/notas/.*$'
      policy: bypass

    # Everything else — single-factor (or two_factor if you want)
    - domain: notes.example.com
      policy: one_factor
```

If you skip the bypass, Authelia will challenge anyone hitting a share URL — defeating the purpose.

## Authelia headers passed downstream

Traefik copies the user identity from Authelia's response onto the forwarded request via `authResponseHeaders` (`Remote-User`, `Remote-Groups`). VaultReader reads **`Remote-User` only**: for attribution (`audit.jsonl`, share `created_by`) and, with `require_user: true`, as a fail-closed check. Because Traefik overwrites any client-supplied value, the header is trustworthy as long as no one can reach the container except through the proxy (see [Team instance](#team-instance)). The rate limiter additionally honors these for IP attribution:

```yaml
authelia:
  forwarded_headers:
    - X-Forwarded-User
    - X-Forwarded-Groups
    - X-Forwarded-Email
```

Traefik must set `X-Real-IP` / `X-Forwarded-For` automatically (it does, by default). VaultReader's rate limiter reads these to give each user a real bucket; without them, every Authelia-authenticated user shares the bridge IP's bucket.

## Caddy alternative

```caddy
notes.example.com {
    handle_path /share/* {
        reverse_proxy vaultreader:8080
    }
    handle_path /notas/* {
        rewrite * /share{uri}
        reverse_proxy vaultreader:8080
    }

    handle {
        forward_auth authelia:9091 {
            uri /api/verify?rd=https://auth.example.com/
            copy_headers Remote-User Remote-Groups Remote-Email
        }
        reverse_proxy vaultreader:8080
    }
}
```

## Nginx alternative

```nginx
server {
    listen 443 ssl http2;
    server_name notes.example.com;
    ssl_certificate /etc/letsencrypt/live/notes.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/notes.example.com/privkey.pem;

    # Public share links
    location ~ ^/(share|notas)/ {
        rewrite ^/notas/(.*)$ /share/$1 break;
        proxy_pass http://vaultreader:8080;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Authelia-gated everything else
    location / {
        auth_request /authelia;
        auth_request_set $user $upstream_http_remote_user;
        proxy_set_header X-Forwarded-User $user;

        proxy_pass http://vaultreader:8080;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location = /authelia {
        internal;
        proxy_pass http://authelia:9091/api/verify;
        proxy_pass_request_body off;
        proxy_set_header Content-Length "";
        proxy_set_header X-Original-URL $scheme://$http_host$request_uri;
    }
}
```

## Validating the setup

1. Hit `https://notes.example.com/` → Authelia login.
2. Hit `https://notes.example.com/share/<some-token>` (with a real token from `appdata/shares.json`) → renders the note with no login.
3. Hit `https://notes.example.com/share/invalid` → 404.
4. Confirm `X-Forwarded-For` is reaching the container:
   ```bash
   docker exec -it vaultreader sh -c 'echo "request reached"'
   # Then trigger 250 fast requests; you should NOT get 429 immediately
   # because rate limit is per-IP (240/min). If everyone shares one bucket,
   # the proxy isn't forwarding the real client IP.
   ```
5. WebDAV check (if enabled in your proxy):
   ```bash
   curl -u $USER:$PASS https://notes.example.com/webdav/ -X PROPFIND -H "Depth: 1"
   ```

## Common pitfalls

- **Authelia bypass rule placement matters.** Authelia evaluates rules top-to-bottom; the bypass for `/share/.*` must come before the `one_factor` catch-all.
- **Traefik priority matters.** Router priority is what determines which router wins when multiple match. The default is "longer rule wins"; setting `priority` explicitly removes ambiguity.
- **Authelia session cookies and share links.** Share links should NEVER include the Authelia session cookie domain logic; they're meant to work for users who aren't logged into Authelia at all. Make sure your bypass works for anonymous browsers (test in incognito).
- **`X-Forwarded-Proto`** isn't strictly required, but if you serve mixed http/https, VaultReader's `share/<token>` URL generator uses the `Host` header. Set `proxy_set_header Host $host` in nginx.
- **Custom share prefix in the share modal.** Set `share_base_url` in `appdata/config.json` (e.g. `https://notes.example.com/share/`); see [configuration.md](configuration.md).

## Team instance

A second VaultReader container can give a small team access to **one folder** without exposing anything else. Example: `team.example.com` served by a container `vaultreader-team` (same image as the main instance) that mounts only `~/vaults/work/team` as vault `team`, for the Authelia group `team`.

**Container side** (its own compose file):
- Joins only its own Docker bridge (e.g. `vr_team_net`) with `traefik.docker.network=vr_team_net`. Traefik in host network mode still reaches it; containers on the shared proxy network cannot, so `Remote-User` cannot be spoofed from inside the network.
- `config.json` sets `require_user: true`, `rw_paths: ["team"]`, `share_base_url: https://team.example.com/share/` and turns off `share_writable`, `webdav`, `sync_status`, `trash_empty`.
- Two routers: the authenticated app (`chain-authelia@file`, priority 10) and a public `PathPrefix(/share/)` router (no auth, priority 20) so read-only share links work for anonymous visitors.

**Authelia side** (`access_control.rules`, first match wins — these must sit **above** the subject-less `*.example.com` catch-alls):

```yaml
# 1. Public share links on the team site
- domain: "team.example.com"
  resources: ['^/share/.*$']
  policy: bypass
# 2. The team site (and any other site the team needs) for the team group only
- domain: "team.example.com"
  subject: "group:team"
  policy: two_factor
# 3. Wall: external collaborators get nothing else
- domain: ["*.example.com", "example.com"]
  subject: "group:external"
  policy: deny
```

**Why the `external` wall exists.** Catch-all rules such as `*.example.com` → `two_factor` have no `subject`, so any user who can log in with 2FA would reach *every* service. Rules 1–2 grant the team its site; rule 3 denies the `external` group everything not matched earlier (hence the ordering: every allow rule for the team must come before it). The team user is a member of both `team` and `external`. Tightening the catch-alls themselves to `group:admins` is the cleaner fix; until then the wall is what keeps a team member out of the rest of the domain.

**Enrolment.** The user's argon2id hash goes into `users_database.yml`; TOTP can be pre-registered by the operator with `authelia storage user totp generate <user>` instead of relying on e-mail delivery. Send the password and the QR code over separate channels.

**Verify** (from outside, as the team user): `team.` loads and `/api/vaults` lists only `team`; any other `*.example.com` host returns 403; with no session `team.` redirects to Authelia but `team./share/<token>` renders.
