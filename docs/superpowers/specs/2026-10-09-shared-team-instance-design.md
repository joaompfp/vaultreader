# Shared team instance — design

**Date:** 2026-10-09
**Status:** Approved (written spec reviewed 2026-10-09)
**Goal:** Give a colleague collaborative web access to `work/jll` — read, create, edit, move, soft-delete, upload, export (.docx), and create read-only share links — without exposing any other vault or any other `*.joao.date` service.

## At a glance

| Decision | Choice |
|---|---|
| Scope the colleague sees | `~/vaults/work/jll` only (mounted as vault `jll`), including `_admin/` and `_reference/` |
| Architecture | **Second container, same image** (`vaultreader-jll`), physically isolated mount — not per-user ACLs in one process |
| Domain | `team.joao.date` |
| Auth | Authelia user `<colleague>`, groups `jll-team` + `external`, **two_factor** |
| Network | Own bridge `vr_jll_net`, **not** `t2_proxy` (Traefik is host-mode, reaches it anyway) |
| Identity in app | `Remote-User` header; `require_user: true` → 401 if absent (fail-closed) |
| Collaboration model | Asynchronous; existing mtime conflict detection; no realtime |
| Shares in team instance | On, **read-only only**; `created_by` recorded; separate `shares.json` |
| Delete | Soft-delete to `.trash` with undo; **no trash purge** |
| History / authorship | App writes `audit.jsonl`; host timer commits to `work/` git **per author** every 5 min |
| External changes | Backend stat-only rescan every 30 s; frontend re-checks open note on focus / every 60 s |
| Version parity | One build, one image tag, deploy both together, SHA in `/health`, drift alert |
| Agent connector (MCP) | **Out of scope** for now — nothing in this design blocks a token-based client later |

## Guiding principle: data is private, the system is not

The operator is happy to share every implementation detail with the colleague (code, compose files, Authelia/Traefik layout, docs, this spec) so he can build his own setup. Security must therefore **never rely on obscurity**: it rests on access control alone. What must not reach the colleague is **private data**: any note outside `work/jll`, plus secrets (Authelia hashes, `admin_token`, API keys, SOPS-decrypted env, `appdata/` of the main instance). 404-cloaking of disabled routes is kept only for UI consistency, not as a security control.

## Context

VaultReader is a single-user app: anyone past Authelia sees every vault, and the server has no notion of identity. Findings during exploration that shape this design:

1. Authelia can gate by domain/path, but the vault is a query parameter (`/api/note?vault=…`), so the proxy cannot restrict a user to one vault.
2. Authelia's catch-all rules (`*.joao.date`, `joao.date` → `two_factor`) have **no `subject`** — any new user with 2FA would reach every service.
3. `/api/shares/create|revoke|revoke-all` are unauthenticated inside the app.
4. Any container on `t2_proxy` can reach `vaultreader:8080` unauthenticated (e.g. `lugares-api`), so a trusted identity header would be spoofable there.
5. The `work` vault is a git repo rooted at `work/`; `jll/` is a subfolder. `work/jll/.vr/` already holds the JLL docx templates.
6. The wikilink index only refreshes on startup or on the app's own writes; edits from another process are invisible until restart.

Roadmap non-goals respected: no database, no JS build step, no WebDAV write, no realtime co-editing.

## Rejected alternatives

- **Per-user ACLs in one instance** (users.json → allowed paths, enforced in ~30 handlers). Search/graph/tags/stats aggregate across vaults; one missed check leaks `pessoal`. Only worth it if VaultReader becomes a multi-tenant OSS product.
- **One process serving both domains by `Host` header.** Guarantees same version but the process would mount every vault — same risk as above.
- **Syncthing + Obsidian for the colleague.** Not web; loses the JLL docx export.
- **Realtime co-editing (Yjs/CRDT).** Requires websockets and likely a build step — contradicts two non-goals.
- **Nested git repo for `jll/` / git inside the container.** Needs git or `go-git` in the image, restructures the `work` repo, and lets the colleague's container rewrite history.
- **`fsnotify` watcher.** New dependency, inotify is non-recursive, Syncthing produces event bursts. Polling is simpler and sufficient at this scale.

## 1. Identity and access

### 1.1 Authelia users and groups
- New user `<colleague>` in `users_database.yml` (argon2id hash via `authelia crypto hash generate argon2`, his email for TOTP enrolment / password reset).
- Groups: `jll-team` (members: `<colleague>`, `joao`) grants the site; `external` (member: `<colleague>`) is the wall.

### 1.2 Authelia access rules (first match wins; inserted before existing rules)
```yaml
# Public share links on the team site
- domain: "team.joao.date"
  resources: ['^/share/.*$']
  policy: bypass

# The team site
- domain: "team.joao.date"
  subject: "group:jll-team"
  policy: two_factor

# Wall: external users get nothing else
- domain: ["*.joao.date", "joao.date"]
  subject: "group:external"
  policy: deny
```
This is additive: behaviour for `joao` and `jarbas` is unchanged. Tightening the subject-less catch-alls to `group:admins` is a worthwhile separate task, not part of this one.

### 1.3 Network isolation
- `vaultreader-jll` joins only `vr_jll_net` (dedicated bridge) with label `traefik.docker.network=vr_jll_net`.
- Traefik runs in `host` network mode and reaches any bridge; Docker isolates bridges from each other, so `t2_proxy` containers cannot reach the team instance.
- Verify during implementation whether `internal: true` still allows host→container traffic; if not, use a plain bridge.

### 1.4 Trusting `Remote-User`
- Traefik's forwardAuth (`authResponseHeaders: Remote-User, Remote-Groups`) overwrites any client-supplied value. Combined with 1.3, the header is trustworthy in the team instance.
- New config field `require_user` (default `false`). When `true`, every request **except** `/share/…` and `/health` without a non-empty `Remote-User` gets **401**.
- Main instance keeps `require_user: false`; `Remote-User` is used for **attribution only, never authorization**. Missing → recorded as `unknown`. Spoofability on `t2_proxy` is a documented limit (it can only mislabel an audit line).
- New file `identity.go`: `userFrom(r) string` + `requireUser` middleware. Wired in `main.go` middleware chain inside `originGuard`.

## 2. Per-instance capabilities

### 2.1 Mount
`~/vaults/work/jll` → `/vaults/jll`. The colleague sees a single vault `jll`. Wikilinks to notes outside `jll` render as missing (correct). His deletes land in `work/jll/.trash/` — hidden from the main instance's tree (dotfile) and already gitignored by `work/.gitignore`.

### 2.2 Config (`appdata/config.json`) — all new fields optional, defaults = today's behaviour
```json
{
  "require_user": true,
  "rw_paths": ["jll"],
  "share_base_url": "https://team.joao.date/share/",
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
| `require_user` | `false` | `true` | 401 without `Remote-User` (except `/share/`, `/health`) |
| `share_base_url` | `https://joao.date/notas/` | `https://team.joao.date/share/` | Replaces the hardcoded prefix in `static/index.html` |
| `default_note` | `""` | `""` | Note opened when nothing is restored. Main instance: `pessoal/inbox/tarefas.md` (replaces the uncommitted hardcode in `index.html:1983`) |
| `features.shares` | `true` | `true` | `/api/shares*` + `/share/*` |
| `features.share_writable` | `true` | `false` | Creating a share with `writable: true` → 403 |
| `features.webdav` | `true` | `false` | `/webdav/` |
| `features.sync_status` | `true` | `false` | `/api/sync-status` |
| `features.trash_empty` | `true` | `false` | `/api/trash/empty` |

Admin stays disabled in the team instance by leaving `admin_token` empty (existing behaviour → 403).

### 2.3 Feature gate at route registration
`srv.feature(name, handler)` wrapper used in `main.go`'s mux block; disabled → **404** (cloaking, matches Traefik's `rejectStatusCode: 404`). No feature checks inside handlers. The one exception is `share_writable`, which is a field-level check in `handleShareCreate`.

### 2.4 `GET /api/instance`
Returns `{version, user, features, rw_paths, share_base_url, default_note}`. SPA reads it once in `init()` to hide disabled UI (share button/popover, WebDAV hints, sync indicator, empty-trash) and shows user + version in the footer. `/api/writable-paths` stays for compatibility.

### 2.5 Shares
- `ShareEntry` gains `created_by` (from `userFrom(r)`), shown in the share list.
- Shares are team-owned: either member can revoke any share in that instance. No per-owner permissions.
- Each instance has its own `appdata/shares.json`; personal shares and team shares never mix.

## 3. History and authorship

### 3.1 App: audit log
- New file `audit.go`: `s.audit(r, action, vault, path, extra)` appends one JSON line to `appdata/audit.jsonl`:
  `{"ts":…, "user":"alice", "action":"save", "vault":"jll", "path":"LIS10/meetings/x.md", "to":"", "bytes":1234}`
- Actions: `save`, `create`, `upload`, `move`, `delete`, `restore`, `share`, `revoke`.
- Called at the same points that call `isWritable` today (plus share create/revoke).
- Failure to write the audit line is **logged, never fails the request** — attribution is not security; losing a save is worse.
- Single writer per file (one process per instance), mutex-guarded append.

### 3.2 Host: per-author commits
- Script (location: `~/docker/scripts/vault-commit-authors` or similar, decided at implementation) + systemd timer, every 5 min.
- Reads both instances' `audit.jsonl` from the last processed offset (stored beside the script), maps vault paths to `work/` paths (`jll/…` in team instance → `jll/…`; `work/…` in main instance → `…`).
- For each author: `git add` only the files that author touched in the window, `git commit --author "<Name> <email>" -m "vr(<user>): <n> changes" ` with paths listed in the body.
- Remaining changes (Obsidian, Syncthing, unknown) → one commit as `joao (sync)`.
- Author map (`<colleague> → <Name> <email>`, `joao → …`) lives in the script's config, not the app.
- Replaces the manual `snapshot …` commits for the `work` vault.
- Known limit: if both authors touch the same file within one window, the commit is attributed to the last author in the log; `audit.jsonl` keeps the exact sequence.

Out of scope (YAGNI): history panel / "last edited by" in the UI.

## 4. Picking up external changes

### 4.1 Backend rescan
- Goroutine started in `main()`: every 30 s, stat-only walk of every vault (same skip rules as `buildTree`); compares `(mtime, size)` against a map captured at the previous pass.
- New/changed `.md` → read + `idx.updateNote`; vanished → `idx.removeNote`. Both exist (`index.go:116`, `:170`).
- Applies to both instances; also fixes Obsidian/Syncthing edits not showing in backlinks.

### 4.2 Frontend open-note check
- On `visibilitychange` → visible, and every 60 s while a note is open: `GET /api/note?vault=…&path=…&meta=1` (new param: returns only `{mtime, size}`, no render) and compare with `noteMTime`.
- Preview mode: mtime changed → silent re-render.
- Edit mode: never touch the buffer; show banner "Changed on disk — Reload / Keep mine", reusing the existing conflict flow.

Out of scope (YAGNI): presence ("the colleague is editing").

## 5. Deploy, operations, testing

### 5.1 Version parity
- `compose/vaultreader-jll.yml` has `image: vaultreader:local` and **no `build:`** — only `vaultreader` builds.
- Dockerfile: `ARG GIT_SHA` → `-ldflags "-X main.version=${GIT_SHA}"`; `/health` returns `{"status":"ok","version":"<sha>"}`; footer shows it.
- Deploy is always `deploy.sh`, which runs `dc-office-up -d --build vaultreader vaultreader-jll` (compose recreates the second container because the tag now points at a new image). This replaces the old deploy line in `CLAUDE.md` and the Hermes `vaultreader` skill.
- `f3nix-health` gets a context gatherer that compares the two containers' **image IDs** (`docker inspect -f {{.Image}}`) → Telegram alert on mismatch (existing `[SILENT]` convention). Image IDs are authoritative even if someone deploys without `deploy.sh` (SHA would read `dev`); the SHA in `/health` and the footer is for humans.
- The single deploy entry point is `images/vaultreader/deploy.sh` (stamps `VR_GIT_SHA`, builds once, recreates both).

### 5.2 Infrastructure changes
- `stacks/office/compose/vaultreader-jll.yml` (own file, per convention), added to `office/compose.yml` `include:`.
  - `user: "1000:1000"`, network `vr_jll_net`, volumes `/home/joao/vaults/work/jll:/vaults/jll:rw` and `${DOCKER_DIR}/stacks/office/appdata/vaultreader-jll:/appdata:rw`.
  - Traefik: authenticated router `Host(team.joao.date)` with `chain-authelia@file`; public router `Host(team.joao.date) && PathPrefix(/share/)` with higher priority, no auth.
  - No outbound network needed (officecli runs locally).
- `appdata/vaultreader-jll/config.json` as in 2.2, plus `icons/jll.svg`.
- Authelia: user, groups, three rules (1.2). Restart Authelia.
- Host: commit script + timer (3.2); drift check (5.1).

### 5.3 Error handling summary

| Situation | Behaviour |
|---|---|
| No `Remote-User`, `require_user: true` | 401 |
| Disabled feature route | 404 |
| Writable share requested, `share_writable: false` | 403 |
| Audit append fails | request succeeds, error logged |
| Commit script fails | retried next tick from stored offset; `f3nix-health` alerts if last successful commit > 1 h |
| Rescan read error on one file | logged, skipped, retried next pass |

### 5.4 Tests
- **Go table tests** (real temp dirs, no FS mocks):
  - `requireUser`: 401 without header; passes with header; `/share/…` and `/health` exempt.
  - Feature gate: disabled → 404, enabled → handler runs.
  - `share_writable: false` → 403; `created_by` recorded.
  - Audit: one line per action with correct user/path.
  - Rescan: file created / modified / deleted outside the app is reflected in the index after one pass.
  - Config defaults: absent fields reproduce current behaviour.
- **Post-deploy smoke** (host → bridge IP, setting `Remote-User` by hand): happy path (read, save, create, upload, export, share read-only); negatives — no header → 401; `/api/trash/empty` → 404; writable share → 403; `/webdav/` → 404; a throwaway container on `t2_proxy` cannot connect to `vaultreader-jll`. Test notes use the `__vr_` prefix and are cleaned up.
- **Manual end-to-end:** incognito login as `<colleague>` with 2FA; `notes.joao.date` and one other subdomain are denied; edit a note; see the `vr(<colleague>)` commit in `work/` git; confirm the main instance shows the edit (rescan) within 30 s.

### 5.5 Docs
- `docs/security.md`: threat model becomes "single operator, optionally a small trusted team per instance"; document `require_user`, network isolation, attribution-only header on the main instance.
- `docs/configuration.md`: new config fields.
- `CLAUDE.md` + Hermes skills (`vaultreader`, `vaultreader-dev`): two-container deploy line; fix stale facts (tests exist, runtime image is `dotnet/runtime-deps`, PUT returns 200 JSON, container runs as uid 1000).
- `ROADMAP.md`: mark "index rebuild on startup only" addressed; note team instance.
- `CHANGELOG.md` entry.

## Out of scope / follow-ups
- MCP / agent connector for the colleague (token-based; fits later without restructuring).
- Tightening Authelia catch-alls to `group:admins`.
- Moving `SYNCTHING_API_KEY` out of plaintext in `compose/vaultreader.yml` into SOPS.
- Authenticating `/api/shares*` in the main instance (callers on `t2_proxy` — Hermes, `lugares-api` — depend on it today).
- History panel / last-editor in UI; presence.
