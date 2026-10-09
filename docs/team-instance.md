# Sharing a folder with a teammate (team instance)

This guide gives a colleague (here: `bob`) read/write access to **one folder** of your vaults, for example `work/team`, without exposing anything else. You keep using your own instance for everything; bob uses a second one.

VaultReader has no per-user permissions inside one process. Isolation comes from running **one instance per audience**:

| Concern | How |
|---|---|
| What bob can see | The team container mounts only the shared folder. Nothing else exists in its filesystem. |
| Who can reach it | The container sits on its own Docker network that only the reverse proxy joins. No published port. |
| Who bob is | Your forward-auth proxy (Authelia, oauth2-proxy, ...) logs him in and sends `Remote-User: bob`. With `require_user: true` the instance answers `401` to any request without that header. |
| What bob can do | `rw_paths` and the `features` flags in the team instance's `config.json`. |
| What bob did | Every change is appended to the team instance's `appdata/audit.jsonl` with his user name. |

Both instances run the same image and read the same files on disk. Edits made on either side reach the other within about 30 seconds (each instance rescans its vaults every 30 s), and an open note re-checks for changes when the browser tab regains focus.

## 1. Second container

One compose file per instance. The paths, host names and network names below are examples.

```yaml
# compose/vaultreader-team.yml
services:
  vaultreader-team:
    image: ghcr.io/joaompfp/vaultreader:latest
    container_name: vaultreader-team
    user: "1000:1000"                 # owner of the vault files on the host
    security_opt:
      - no-new-privileges:true
    volumes:
      # Only the shared folder, mounted as vault "team".
      - /srv/vaults/work/team:/vaults/team:rw
      # Its own appdata: config, shares and audit log are per instance.
      - /srv/vaultreader-team/appdata:/appdata:rw
    networks:
      - vr_team
    restart: unless-stopped
    # No "ports:" — only the proxy may reach this container.

networks:
  vr_team:
    name: vr_team
```

The proxy must join `vr_team` (or reach the container from the host network); no other container should. If the team container were on a shared network, any other container on it could send a forged `Remote-User` header.

## 2. Team instance config

`/srv/vaultreader-team/appdata/config.json`:

```json
{
  "require_user": true,
  "rw_paths": ["team"],
  "share_base_url": "https://team.example.com/share/",
  "features": {
    "share_writable": false,
    "webdav": false,
    "sync_status": false,
    "trash_empty": false
  }
}
```

- `require_user: true`: fail closed. Every request except `/share/*` and `/health` needs a non-empty `Remote-User`.
- `rw_paths: ["team"]`: bob can edit inside the team vault. Use `[]` for read-only access, or a sub-path such as `"team/drafts"`.
- `features`: switch off what a guest does not need. A disabled feature's routes answer `404` and the UI hides the controls. Set `"shares": false` as well if bob should not create public links at all.
- An unknown or misspelt key stops the container at startup with a `config:` error, so a typo cannot silently disable `require_user`.

All options: [configuration.md](configuration.md#per-instance-options-all-optional).

## 3. Proxy and forward-auth

Two routes for `team.example.com`:

1. Everything: forward-auth, then the container.
2. `/share/` only: no auth, so read-only share links work for people without an account. Leave this route out if you turned `shares` off.

### Traefik + Authelia

Labels on `vaultreader-team` (Traefik in Docker on the `vr_team` network):

```yaml
    labels:
      - traefik.enable=true
      - traefik.docker.network=vr_team
      - traefik.http.services.vaultreader-team.loadbalancer.server.port=8080

      - traefik.http.routers.vr-team.rule=Host(`team.example.com`)
      - traefik.http.routers.vr-team.entrypoints=websecure
      - traefik.http.routers.vr-team.tls=true
      - traefik.http.routers.vr-team.middlewares=authelia@docker
      - traefik.http.routers.vr-team.priority=10

      - traefik.http.routers.vr-team-share.rule=Host(`team.example.com`) && PathPrefix(`/share/`)
      - traefik.http.routers.vr-team-share.entrypoints=websecure
      - traefik.http.routers.vr-team-share.tls=true
      - traefik.http.routers.vr-team-share.priority=20
      # Defence in depth: drop any client-supplied Remote-User on the public route
      - traefik.http.routers.vr-team-share.middlewares=vr-no-user
      - traefik.http.middlewares.vr-no-user.headers.customRequestHeaders.Remote-User=
```

The `authelia` middleware is the usual forward-auth definition (on the Authelia container):

```yaml
      - traefik.http.middlewares.authelia.forwardAuth.address=http://authelia:9091/api/authz/forward-auth
      - traefik.http.middlewares.authelia.forwardAuth.trustForwardHeader=true
      - traefik.http.middlewares.authelia.forwardAuth.authResponseHeaders=Remote-User,Remote-Groups,Remote-Email,Remote-Name
```

Traefik copies `Remote-User` from Authelia's response onto the request and overwrites any value the client sent. `Remote-User` is only trustworthy on routes that go through forward-auth. On the public `/share/` route nothing sets it, so VaultReader ignores it there: changes made through a writable share link are recorded as user `share`, whatever headers the visitor sends. (An empty value in `customRequestHeaders` removes the header.)

Authelia `access_control.rules` (first match wins; put these **above** any rule without a `subject`):

```yaml
# 1. Public share links on the team site
- domain: "team.example.com"
  resources: ['^/share/.*$']
  policy: bypass
# 2. The team site, for the team group only
- domain: "team.example.com"
  subject: "group:team"
  policy: two_factor
# 3. Wall: guests get nothing else on your domain
- domain: ["*.example.com", "example.com"]
  subject: "group:external"
  policy: deny
```

bob is a member of both `team` and `external`. Rule 3 matters if you have catch-all rules such as `*.example.com → two_factor` without a `subject`: those would let any logged-in user, bob included, reach every other service. Every allow rule for the team must come before the wall. Restricting the catch-alls themselves to your admin group is the cleaner fix.

Enrolment: add bob to Authelia's `users_database.yml` with an argon2id hash (`authelia crypto hash generate argon2`). You can register his TOTP yourself with `authelia storage user totp generate bob` instead of relying on e-mail. Send the password and the QR code over different channels.

More on Authelia with VaultReader: [authelia.md](authelia.md).

### oauth2-proxy (nginx `auth_request`)

oauth2-proxy returns the user in `X-Auth-Request-User` (with `--set-xauthrequest`). VaultReader reads only `Remote-User`, so the proxy copies one into the other:

```nginx
server {
    server_name team.example.com;
    # listen / ssl_certificate lines omitted

    location /oauth2/ {
        proxy_pass http://oauth2-proxy:4180;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Auth-Request-Redirect $request_uri;
    }
    location = /oauth2/auth {
        proxy_pass http://oauth2-proxy:4180;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header Content-Length "";
        proxy_pass_request_body off;
    }

    # Public share links: no auth
    location /share/ {
        proxy_pass http://vaultreader-team:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Remote-User "";
    }

    location / {
        auth_request /oauth2/auth;
        error_page 401 = /oauth2/sign_in;
        auth_request_set $user $upstream_http_x_auth_request_user;

        proxy_pass http://vaultreader-team:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Remote-User $user;     # overwrites any client value
    }
}
```

Limit who may log in on the oauth2-proxy side, for example `--authenticated-emails-file` or `--allowed-group` (provider-dependent). If you run oauth2-proxy behind Traefik `forwardAuth` instead, configure it to return the user name in a response header called `Remote-User` and list that header in `authResponseHeaders`; check the oauth2-proxy documentation for your version.

## 4. Check it

From outside, logged in as bob:

- `https://team.example.com/` loads; `GET /api/vaults` lists only `team`.
- The status bar shows `bob · <version>`.
- Other hosts of your domain answer `403` (the wall).

Without a session:

- `https://team.example.com/` redirects to the login page.
- `https://team.example.com/share/<token>` renders the shared note.
- From another container (not on `vr_team`), `curl http://vaultreader-team:8080/` fails to connect.

Both instances should run the same build: compare `GET /health` (`{"status":"ok","version":"..."}`) on each.

## Audit log

Each instance appends one JSON line per change to its own `appdata/audit.jsonl`:

```json
{"ts":1760000000,"user":"bob","action":"save","vault":"team","path":"project/plan.md","bytes":1834}
{"ts":1760000042,"user":"bob","action":"move","vault":"team","path":"project/old.md","to":"archive/old.md"}
```

| Field | Meaning |
|---|---|
| `ts` | Unix seconds |
| `user` | `Remote-User`, or `unknown`; `share` for changes made through a share link |
| `action` | `save`, `create`, `upload`, `move`, `delete`, `restore`, `share`, `revoke` |
| `vault` | Vault name (top-level folder under `/vaults` of that instance); empty for `revoke` |
| `path` | Vault-relative path; for `revoke`, the share token (or `*` for revoke-all) |
| `to` | Destination of a `move` (omitted otherwise) |
| `bytes` | Size written, for `save`, `create` and `upload` (omitted when 0) |

The log records attribution, not forensics: reads are not logged, lines are not signed, and anyone with host access can edit the file. A failed append is logged and the request still succeeds. Saves through a writable share link are always recorded as user `share`; the `Remote-User` header is ignored on `/share/` routes because no forward-auth runs there. Admin config changes and `trash/empty` are not recorded.

## Per-author git history

VaultReader does not run git. If the shared folder is a git repository, you can get one commit per author with a small script on the host, run by a timer every few minutes:

1. Read `audit.jsonl` from the byte offset stored by the previous run. If the file is now smaller than that offset, it was rotated or truncated: start from 0.
2. Map each line to a file in the repository: the instance's mount for `vault` plus `path` (and `to` for moves). Skip `share` and `revoke`; they do not change files.
3. Run `git reset -q` so nothing staged earlier leaks into the next commit.
4. For each user in the window, stage the files they touched that `git status --porcelain -z` reports as changed, and commit with `--author "bob <bob@users.noreply.example.com>"`. Set `GIT_LITERAL_PATHSPECS=1` so file names are not read as patterns.
5. Commit whatever is still changed (edits from Obsidian, sync, or lines lost to rotation) as a fallback author.
6. Store the new offset.

Changes made in the same window to the same file by two people end up in the commit of whoever comes first; for a small team that is usually acceptable.

### Don't let teammates write code that something else runs

A host script that runs git in a folder a teammate can write runs whatever that folder's `.git` says: a teammate who could write `.git/hooks/post-commit` or `.git/config` could run commands on your host. VaultReader refuses every write to a path with a segment starting with `.` (`.git`, `.obsidian`, `.trash`, …) since v0.1.1, whatever `rw_paths` say; the trash is changed only by VaultReader's own delete, restore and empty. Even so, keep the repository's `.git` outside the shared mount (`git --git-dir=/srv/team-history.git --work-tree=/srv/vaults/team …`), so that no other way into the folder (sync, a file share, an older build) reaches it either.

The same goes for other things that run what the folder holds:

- **Obsidian desktop.** A vault's `.obsidian/plugins/` holds JavaScript that Obsidian runs with your user's rights once community plugins are on. Don't open a shared folder in Obsidian with community plugins enabled unless you trust everyone who can write it.
- **Coding agents.** Agent instruction files such as `CLAUDE.md` or `AGENTS.md` are read as instructions by AI coding agents. One a teammate writes in a shared folder can steer an agent you run there (prompt injection). Don't point an agent at a shared folder, or check those files before you do.
