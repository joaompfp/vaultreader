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

Authelia injects these on forwarded requests, which VaultReader **does not consume** (it has no concept of users) but which the rate limiter does honor for IP attribution:

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
- **Custom share prefix in the share modal.** The frontend hardcodes `joao.date/notas/` for share links. Fork the relevant `static/index.html` line if you use a different prefix.
