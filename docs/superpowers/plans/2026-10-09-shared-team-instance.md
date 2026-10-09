# Shared Team Instance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run a second, physically isolated VaultReader container at `team.joao.date` that serves only `~/vaults/work/jll` to the Authelia user `<colleague>` (and `joao`), with per-instance feature flags, user attribution, per-author git history, and pickup of changes made by the other instance.

**Architecture:** Same binary and image for both instances; all differences live in each instance's `appdata/config.json`. The app learns exactly one thing about users (the `Remote-User` name from Authelia) and uses it for attribution only. Isolation comes from the mount (only `jll` is visible), a dedicated Docker bridge (only host-mode Traefik can reach it), and `require_user` (fail-closed). Versioning is done by a host-side script that turns the app's `audit.jsonl` into per-author commits in the `work/` git repo.

**Tech Stack:** Go 1.24 stdlib (`net/http`, `httptest`), goldmark (existing), Alpine.js SPA (no build step), Docker Compose + Traefik v2 (host network) + Authelia 4.39, Python 3.12 stdlib (`unittest`) for host scripts, systemd user timers.

**Spec:** `docs/superpowers/specs/2026-10-09-shared-team-instance-design.md` — read it before starting any task.

## Global Constraints

- Domain is exactly `team.joao.date`; share base URL there is `https://team.joao.date/share/`.
- Team instance mounts `/home/joao/vaults/work/jll` → `/vaults/jll` and nothing else from `~/vaults`.
- Every new config field is optional and its absence reproduces today's behaviour exactly (main instance `notes.joao.date` must not change).
- Feature names (exact strings): `shares`, `share_writable`, `webdav`, `sync_status`, `trash_empty`. Missing key = enabled.
- Team instance config: `require_user: true`, `rw_paths: ["jll"]`, features `shares: true, share_writable: false, webdav: false, sync_status: false, trash_empty: false`, empty `admin_token`.
- Disabled feature route → **404**. Missing `Remote-User` with `require_user` → **401** (except `/share/…` and `/health`). Writable share when `share_writable` is off → **403**.
- `Remote-User` is used for attribution only, never authorization. Absent → recorded as `unknown`.
- Audit-log write failure never fails the request (log and continue).
- Go dependencies stay at three (`goldmark`, `golang.org/x/net`, `gopkg.in/yaml.v3`). No `fsnotify`, no `go-git`.
- No JS build step. Every edit to `static/index.html` must pass the `node --check` snippet in `CLAUDE.md` → "Verification before committing".
- Tests never mock the filesystem: use `t.TempDir()` (Go) / `tempfile.TemporaryDirectory()` (Python).
- Infra rules (from `~/docker/CLAUDE.md` + user memory): deploy only via `dc-*` aliases run through `bash -lic`; never raw `docker run`; one compose file per service under `stacks/office/compose/`; secrets only in SOPS; mount `${DOCKER_DIR}/resolv.conf`, never the host's, if DNS is needed.
- Both containers always run the same image: `vaultreader-jll` has **no `build:`**; the only deploy command is `./deploy.sh` (Task 4), which builds once and recreates both.
- Data is private, the system is not: never rely on obscurity; never let a note outside `work/jll` or a secret reach the team instance.
- Commit trailer on every commit: `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.

## Review Focus

1. **Trash-restore path traversal** — `POST /api/trash/restore?path=../../appdata/shares.json` must be rejected (400), not move a file out of `appdata`. → Task 2 tests.
2. **Typo in a feature name** (`"share": false` instead of `"shares"`) silently leaves the feature on. Expect startup to fail loudly. → Task 3 `validateConfig` test.
3. **Header edge cases** — `Remote-User: "   "` counts as missing (401); `/sharefoo` and `/share` (no slash) are *not* exempt from `require_user`; `/share/<token>/asset?…` is exempt. → Task 4 tests.
4. **Trashed and dot-dir notes leak into the index** (confirmed today: `/api/search?q=VRTRASH&vault=work` returns `.trash/…` notes) — after Task 7 neither `buildAll` nor the rescanner indexes anything under a dot-directory. → Task 7 tests.
5. **Committer edge cases** — a truncated last line in `audit.jsonl` (crash mid-write), a folder move (both old and new paths must be staged), and a git failure (offset must not advance). → Task 10 tests.

---

## File Structure

**VaultReader repo (`~/docker/stacks/office/images/vaultreader`)**

| File | Status | Responsibility |
|---|---|---|
| `routes.go` | Create | `(*server).routes()` mux table + `(*server).handler()` middleware chain (extracted from `main.go`) |
| `main.go` | Modify | Flags, startup, index build, rescanner start, `http.Server`, shutdown — no route table |
| `config.go` | Create | Feature names, `enabled()`, `validateConfig()`, resolved config accessors |
| `admin.go` | Modify | `AdminConfig` gains new fields; `loadConfig` validates |
| `identity.go` | Create | `userFrom(r)`, `requireUser` middleware |
| `instance.go` | Create | `version` var, `GET /api/instance`, `/health` with version |
| `audit.go` | Create | `auditLog` append-only JSONL writer + `(*server).audit(...)` |
| `rescan.go` | Create | `walkVaultFiles` shared walker, `rescanner` periodic stat pass |
| `index.go` | Modify | `buildAll` uses `walkVaultFiles` (skips dot-dirs) |
| `trash.go` | Modify | Restore hardened (`safePath`, must be under `.trash/`, `isWritable`) |
| `shares.go` | Modify | `CreatedBy` field; `share_writable` enforcement on create and share-PUT |
| `notes.go` | Modify | Audit calls at each successful write; `meta=1` on GET |
| `attachments.go` | Modify | Audit call on attachment delete |
| `testutil_test.go` | Create | `newTestServer`, `writeFile`, `do` helpers |
| `*_test.go` | Create | One test file per new Go file + `trash_test.go` |
| `static/index.html` | Modify | `/api/instance` consumption, share URL, default note, footer, freshness check |
| `Dockerfile` | Modify | `go test` in builder; `ARG GIT_SHA` → ldflags |
| `deploy.sh` | Create | The single deploy command (build once, recreate both, pass SHA) |
| `docs/*.md`, `CLAUDE.md`, `ROADMAP.md`, `CHANGELOG.md` | Modify | Task 13 |

**Docker repo (`~/docker`)**

| File | Status | Responsibility |
|---|---|---|
| `stacks/office/compose/vaultreader-jll.yml` | Create | Team instance service + `vr_jll_net` network |
| `stacks/office/compose/vaultreader.yml` | Modify | `build.args.GIT_SHA` |
| `stacks/office/compose.yml` | Modify | `include:` the new file |
| `stacks/office/appdata/vaultreader-jll/config.json` | Create (not committed: appdata) | Team instance config |
| `stacks/core/appdata/authelia/{users_database,configuration}.yml` | Modify | User, groups, rules |
| `scripts/vaults/vault_commit_authors.py` | Create | Host committer |
| `scripts/vaults/test_vault_commit_authors.py` | Create | Its tests |
| `scripts/vaults/vault-commit-authors.example.json` | Create | Config template (real config lives in `~/.config/`) |

**Host**

| File | Status | Responsibility |
|---|---|---|
| `~/.config/systemd/user/vault-commit-authors.{service,timer}` | Create | 5-minute timer |
| `~/.config/vault-commit-authors.json` | Create | Real committer config (author emails) |
| `~/.hermes/scripts/f3nix_context_gatherers/vaultreader.py` | Create | Parity + committer-freshness gatherer |
| `~/.hermes/scripts/f3nix-context-refresh.py` | Modify | Register gatherer |
| `~/.hermes/skills/.../vaultreader*` SKILL.md | Modify | Deploy line, stale facts |

---

### Task 0: Toolchain, branch, tests in the image build

**Files:**
- Modify: `Dockerfile`

**Interfaces:**
- Produces: a working local `go test ./...` loop (`~/.local/go/bin/go`), and a Docker build that refuses to produce an image when tests fail.

- [ ] **Step 1: Branch**

```bash
cd ~/docker/stacks/office/images/vaultreader
git checkout design/shared-team-instance
git checkout -b feat/shared-team-instance
git status --short   # expect: " M static/index.html" (the default-note hardcode; Task 8 replaces it — leave it alone until then)
```

- [ ] **Step 2: Install Go 1.24 locally (no Go on host today)**

```bash
V=$(curl -s 'https://go.dev/dl/?mode=json&include=all' | python3 -c "import json,sys;print(next(r['version'] for r in json.load(sys.stdin) if r['version'].startswith('go1.24.')))")
curl -fsSL "https://go.dev/dl/${V}.linux-amd64.tar.gz" -o /tmp/claude-1000/go.tgz
rm -rf ~/.local/go && tar -C ~/.local -xzf /tmp/claude-1000/go.tgz && rm /tmp/claude-1000/go.tgz
~/.local/go/bin/go version
```
Expected: `go version go1.24.x linux/amd64`. All later steps use `GO=~/.local/go/bin/go`.

- [ ] **Step 3: Run the existing tests**

Run: `cd ~/docker/stacks/office/images/vaultreader && ~/.local/go/bin/go test ./... 2>&1 | tail -5`
Expected: `ok  	vaultreader` (or the module name). If anything fails, stop and report — do not start feature work on a red baseline.

- [ ] **Step 4: Make the image build run tests**

In `Dockerfile`, stage 1, replace:
```dockerfile
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" -trimpath -o vaultreader .
```
with:
```dockerfile
RUN go mod tidy
# Fail the image build (and therefore the deploy) on any failing test.
RUN CGO_ENABLED=0 go test ./...
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" -trimpath -o vaultreader .
```

- [ ] **Step 5: Commit**

```bash
git add Dockerfile
git commit -m "build: run go test in the image builder stage

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 1: Extract the route table (refactor, no behaviour change)

AGENTS.md hard rule: refactor to the principle first, then change behaviour. Tests in later tasks need to drive the real mux + middleware chain without starting a server.

**Files:**
- Create: `routes.go`
- Create: `testutil_test.go`
- Create: `routes_test.go`
- Modify: `main.go`

**Interfaces:**
- Produces:
  - `func (s *server) routes() *http.ServeMux` — every `mux.HandleFunc` line currently in `main()`, same order, same handlers.
  - `func (s *server) handler() http.Handler` — `gzipMiddleware(originGuard(newRateLimiter(s.routes(), 240, time.Minute)))`.
  - Test helpers: `newTestServer(t *testing.T, cfg AdminConfig) *server` (creates `<tmp>/vaults/jll` and `<tmp>/appdata`), `writeFile(t *testing.T, path, content string)`, `do(t *testing.T, h http.Handler, method, target string, body string, headers map[string]string) *httptest.ResponseRecorder`.

- [ ] **Step 1: Write the failing test**

`testutil_test.go`:
```go
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestServer builds a server over real temp dirs with one vault "jll".
func newTestServer(t *testing.T, cfg AdminConfig) *server {
	t.Helper()
	root := t.TempDir()
	vaults := filepath.Join(root, "vaults")
	appdata := filepath.Join(root, "appdata")
	for _, d := range []string{filepath.Join(vaults, "jll"), appdata} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	idx := newIndex()
	idx.buildAll(vaults)
	return &server{
		vaultsDir:  vaults,
		appdataDir: appdata,
		idx:        idx,
		cfg:        cfg,
		shares:     newShareStore(appdata),
		shutdown:   make(chan struct{}),
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// do sends one request through h and returns the recorder.
func do(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
```

`routes_test.go`:
```go
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesServeExistingEndpoints(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	writeFile(t, filepath.Join(s.vaultsDir, "jll", "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	h := s.handler()

	if rec := do(t, h, "GET", "/health", "", nil); rec.Code != 200 {
		t.Fatalf("/health = %d", rec.Code)
	}
	rec := do(t, h, "GET", "/api/vaults", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "jll") {
		t.Fatalf("/api/vaults = %d %q", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "GET", "/api/note?vault=jll&path=a.md", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"raw":"# A\n"`) {
		t.Fatalf("/api/note = %d %q", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `~/.local/go/bin/go test -run TestRoutesServeExistingEndpoints ./...`
Expected: FAIL — `s.handler undefined`.

- [ ] **Step 3: Move the route table into `routes.go`**

`routes.go`:
```go
package main

import (
	"io/fs"
	"log"
	"net/http"
	"time"
)

// routes is the single place where URL paths meet handlers.
func (s *server) routes() *http.ServeMux {
	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("static sub: %v", err)
	}
	staticHandler := http.FileServer(http.FS(subFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			s.handleIndex(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})
	// …paste every mux.HandleFunc / mux.Handle line from main() here,
	// unchanged and in the same order, replacing `srv.` with `s.`…
	return mux
}

// handler wraps the mux in the middleware chain used in production.
func (s *server) handler() http.Handler {
	return gzipMiddleware(originGuard(newRateLimiter(s.routes(), 240, time.Minute)))
}
```
The "paste" instruction is literal: cut lines `mux.HandleFunc("/api/vaults", …)` through `mux.Handle("/webdav/", srv.newWebDAVHandler())` from `main.go` (currently ~L72–L110) and paste them in place of the comment, renaming `srv.` → `s.`. Do not add, drop or reorder routes.

In `main.go`: delete the `subFS`/`staticHandler`/`mux` block and set `Handler: srv.handler(),` in the `http.Server` literal. Remove the now-unused `io/fs` import from `main.go`.

- [ ] **Step 4: Run all tests**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add routes.go main.go testutil_test.go routes_test.go
git commit -m "refactor: extract route table and middleware chain from main()

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Harden trash restore (security fix)

**Files:**
- Modify: `trash.go` (`handleTrashRestore`, ~L114–L156)
- Test: `trash_test.go`

**Interfaces:**
- Consumes: `newTestServer`, `writeFile`, `do` (Task 1).
- Produces: `handleTrashRestore` that accepts only `path` values of the form `.trash/<name>` (single segment under `.trash`), validated by `safePath`, with the restore destination checked by `isWritable`.

- [ ] **Step 1: Write the failing tests**

`trash_test.go`:
```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrashRestoreRejectsTraversal(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	secret := filepath.Join(s.appdataDir, "shares.json")
	writeFile(t, secret, "[]")
	h := s.handler()

	for _, p := range []string{
		"../../appdata/shares.json",
		".trash/../../../appdata/shares.json",
		"notes/a.md",          // not under .trash
		".trash/sub/x.md",     // nested — only direct children are restorable
		"/etc/passwd",
	} {
		rec := do(t, h, "POST", "/api/trash/restore?vault=jll&path="+p, "", nil)
		if rec.Code != 400 {
			t.Errorf("path %q: code %d, want 400", p, rec.Code)
		}
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("appdata file was moved: %v", err)
	}
}

func TestTrashRestoreRoundTrip(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	writeFile(t, filepath.Join(s.vaultsDir, "jll", "x", "n.md"), "# N\n")
	h := s.handler()

	rec := do(t, h, "DELETE", "/api/note?vault=jll&path=x/n.md", "", nil)
	if rec.Code != 200 {
		t.Fatalf("delete = %d", rec.Code)
	}
	entries, _ := os.ReadDir(filepath.Join(s.vaultsDir, "jll", ".trash"))
	if len(entries) != 1 {
		t.Fatalf("trash entries = %d", len(entries))
	}
	rec = do(t, h, "POST", "/api/trash/restore?vault=jll&path=.trash/"+entries[0].Name(), "", nil)
	if rec.Code != 204 {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(s.vaultsDir, "jll", "x", "n.md")); err != nil {
		t.Fatalf("not restored: %v", err)
	}
}

func TestTrashRestoreRequiresWritable(t *testing.T) {
	s := newTestServer(t, AdminConfig{}) // nothing writable
	writeFile(t, filepath.Join(s.vaultsDir, "jll", ".trash", makeTrashName("n.md", 1)), "# N\n")
	rec := do(t, s.handler(), "POST", "/api/trash/restore?vault=jll&path=.trash/"+makeTrashName("n.md", 1), "", nil)
	if rec.Code != 403 {
		t.Fatalf("code %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `~/.local/go/bin/go test -run TestTrashRestore ./...`
Expected: FAIL — traversal cases return non-400 and/or `shares.json` gets moved; writable case returns 204.

- [ ] **Step 3: Implement**

In `handleTrashRestore`, replace the block from `// path is relative to vault root` through `dest := filepath.Join(vp, originalBase)` with:
```go
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	// Only direct children of <vault>/.trash are restorable.
	dir, name := filepath.Split(filepath.ToSlash(path))
	if dir != ".trash/" || name == "" || name == "." || name == ".." {
		errResponse(w, 400, "path must be .trash/<name>")
		return
	}
	trashFull, ok := s.safePath(vp, ".trash/"+name)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	originalBase, ok := decodeTrashName(name)
	if !ok {
		originalBase = legacyDecodeTrashName(name)
	}
	dest, ok := s.safePath(vp, originalBase)
	if !ok {
		errResponse(w, 400, "invalid restore target")
		return
	}
	if !s.isWritable(vault, originalBase) {
		errResponse(w, 403, "path is not writable")
		return
	}
```
(Keep the rest — `MkdirAll`, `Rename`, reindex, `204` — unchanged.)

- [ ] **Step 4: Sweep for the same class of bug**

Run: `grep -n 'URL.Query().Get("path")\|URL.Query().Get("from")\|URL.Query().Get("to")\|FormValue("notePath")' *.go`
For every handler listed, confirm the value reaches `safePath` before any filesystem call. Expected: all others already do (`notes.go`, `files.go`, `attachments.go`, `export.go`, `trash.go` empty). If one doesn't, add a failing traversal test for it in `trash_test.go` style and fix it in this task.

- [ ] **Step 5: Run all tests, commit**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...` → PASS.
```bash
git add trash.go trash_test.go
git commit -m "fix(security): trash restore only accepts .trash/<name>, validates target + writability

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Per-instance config + feature gate

**Files:**
- Create: `config.go`, `config_test.go`
- Modify: `admin.go` (`AdminConfig`, `loadConfig`), `routes.go`, `shares.go` (`handleShareCreate`, share-view PUT branch)

**Interfaces:**
- Consumes: `newTestServer`, `do` (Task 1).
- Produces:
  - `AdminConfig` fields: `RequireUser bool \`json:"require_user,omitempty"\``, `ShareBaseURL string \`json:"share_base_url,omitempty"\``, `DefaultNote string \`json:"default_note,omitempty"\``, `Features map[string]bool \`json:"features,omitempty"\``.
  - `var knownFeatures = []string{"shares", "share_writable", "webdav", "sync_status", "trash_empty"}`
  - `func (s *server) enabled(name string) bool` — missing key ⇒ `true`.
  - `func (s *server) resolvedFeatures() map[string]bool` — every known feature → bool.
  - `func (s *server) shareBaseURL() string` — default `"https://joao.date/notas/"`.
  - `func validateConfig(c AdminConfig) error` — error on unknown feature key, on `default_note` without a `/`, on `share_base_url` not ending in `/`.
  - `func (s *server) feature(name string, h http.HandlerFunc) http.HandlerFunc` — 404 when disabled.

- [ ] **Step 1: Write the failing tests**

`config_test.go`:
```go
package main

import (
	"testing"
)

func TestEnabledDefaultsToTrue(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	for _, f := range knownFeatures {
		if !s.enabled(f) {
			t.Errorf("%s should default to enabled", f)
		}
	}
	s.cfg.Features = map[string]bool{"webdav": false}
	if s.enabled("webdav") || !s.enabled("shares") {
		t.Fatal("explicit false must disable only that feature")
	}
}

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  AdminConfig
		ok   bool
	}{
		{"empty", AdminConfig{}, true},
		{"known features", AdminConfig{Features: map[string]bool{"shares": true, "trash_empty": false}}, true},
		{"typo feature", AdminConfig{Features: map[string]bool{"share": false}}, false},
		{"default note ok", AdminConfig{DefaultNote: "pessoal/inbox/tarefas.md"}, true},
		{"default note no vault", AdminConfig{DefaultNote: "tarefas.md"}, false},
		{"base url ok", AdminConfig{ShareBaseURL: "https://team.joao.date/share/"}, true},
		{"base url no slash", AdminConfig{ShareBaseURL: "https://team.joao.date/share"}, false},
	}
	for _, c := range cases {
		if err := validateConfig(c.cfg); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestDisabledFeatureRoutes404(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}, Features: map[string]bool{
		"shares": false, "webdav": false, "sync_status": false, "trash_empty": false,
	}})
	h := s.handler()
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/shares"},
		{"POST", "/api/shares/create"},
		{"DELETE", "/api/shares/revoke?token=x"},
		{"DELETE", "/api/shares/revoke-all"},
		{"GET", "/share/abc"},
		{"PROPFIND", "/webdav/"},
		{"GET", "/api/sync-status"},
		{"DELETE", "/api/trash/empty?vault=jll"},
	} {
		if rec := do(t, h, c.method, c.path, "", nil); rec.Code != 404 {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
	// Enabled neighbours still work.
	if rec := do(t, h, "GET", "/api/trash?vault=jll", "", nil); rec.Code != 200 {
		t.Errorf("/api/trash = %d, want 200", rec.Code)
	}
}

func TestShareWritableDisabled(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}, Features: map[string]bool{"share_writable": false}})
	h := s.handler()
	rec := do(t, h, "POST", "/api/shares/create", `{"vault":"jll","path":"a.md","writable":true}`, nil)
	if rec.Code != 403 {
		t.Fatalf("writable create = %d, want 403", rec.Code)
	}
	rec = do(t, h, "POST", "/api/shares/create", `{"vault":"jll","path":"a.md","writable":false}`, nil)
	if rec.Code != 200 {
		t.Fatalf("read-only create = %d, want 200", rec.Code)
	}
	// A pre-existing writable share must not accept writes either.
	e := s.shares.create("jll", "a.md", true, 0, "legacy")
	rec = do(t, h, "PUT", "/share/"+e.Token, `{"raw":"x"}`, nil)
	if rec.Code != 403 {
		t.Fatalf("share PUT = %d, want 403", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `~/.local/go/bin/go test -run 'TestEnabled|TestValidateConfig|TestDisabledFeature|TestShareWritable' ./...`
Expected: FAIL — undefined `knownFeatures`, `enabled`, `validateConfig`; `Features` unknown field.

- [ ] **Step 3: Implement**

`admin.go` — extend the struct (keep existing fields and tags):
```go
type AdminConfig struct {
	AdminToken   string          `json:"admin_token,omitempty"`
	RWPaths      []string        `json:"rw_paths"`
	RequireUser  bool            `json:"require_user,omitempty"`   // 401 without Remote-User (team instance)
	ShareBaseURL string          `json:"share_base_url,omitempty"` // public prefix for share links; default joao.date/notas/
	DefaultNote  string          `json:"default_note,omitempty"`   // "vault/path.md" opened when nothing is restored
	Features     map[string]bool `json:"features,omitempty"`       // missing key = enabled; see knownFeatures
}
```
`admin.go` — `loadConfig` fails fast on an invalid config:
```go
func (s *server) loadConfig() {
	data, err := os.ReadFile(s.configPath())
	if err != nil {
		return // no config yet → empty (all writes blocked by default)
	}
	var cfg AdminConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("config: %s: %v", s.configPath(), err)
	}
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("config: %s: %v", s.configPath(), err)
	}
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}
```

`config.go`:
```go
package main

import (
	"fmt"
	"net/http"
	"strings"
)

// knownFeatures lists every toggle an instance may switch off in
// config.json → "features". A missing key means enabled, so an empty
// config reproduces the original single-user behaviour.
var knownFeatures = []string{"shares", "share_writable", "webdav", "sync_status", "trash_empty"}

const defaultShareBaseURL = "https://joao.date/notas/"

func validateConfig(c AdminConfig) error {
	for k := range c.Features {
		known := false
		for _, f := range knownFeatures {
			if k == f {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown feature %q (known: %s)", k, strings.Join(knownFeatures, ", "))
		}
	}
	if c.DefaultNote != "" && !strings.Contains(c.DefaultNote, "/") {
		return fmt.Errorf("default_note %q must be \"vault/path.md\"", c.DefaultNote)
	}
	if c.ShareBaseURL != "" && !strings.HasSuffix(c.ShareBaseURL, "/") {
		return fmt.Errorf("share_base_url %q must end with /", c.ShareBaseURL)
	}
	return nil
}

func (s *server) enabled(name string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	v, ok := s.cfg.Features[name]
	return !ok || v
}

func (s *server) resolvedFeatures() map[string]bool {
	out := make(map[string]bool, len(knownFeatures))
	for _, f := range knownFeatures {
		out[f] = s.enabled(f)
	}
	return out
}

func (s *server) shareBaseURL() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfg.ShareBaseURL == "" {
		return defaultShareBaseURL
	}
	return s.cfg.ShareBaseURL
}

// feature gates a route: a disabled feature answers 404, as if the
// endpoint did not exist.
func (s *server) feature(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled(name) {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}
}
```

`routes.go` — wrap exactly these registrations:
```go
	mux.HandleFunc("/api/sync-status", s.feature("sync_status", s.handleSyncStatus))
	mux.HandleFunc("/api/shares", s.feature("shares", s.handleShareList))
	mux.HandleFunc("/api/shares/create", s.feature("shares", s.handleShareCreate))
	mux.HandleFunc("/api/shares/revoke", s.feature("shares", s.handleShareRevoke))
	mux.HandleFunc("/api/shares/revoke-all", s.feature("shares", s.handleShareRevokeAll))
	mux.HandleFunc("/share/", s.feature("shares", s.handleShareView))
	mux.HandleFunc("/api/trash/empty", s.feature("trash_empty", s.handleTrashEmpty))
	mux.HandleFunc("/webdav/", s.feature("webdav", s.newWebDAVHandler().ServeHTTP))
```
(`mux.Handle("/webdav/", …)` becomes the `HandleFunc` line above.)

`shares.go` — in `handleShareCreate`, after the `vault and path required` check:
```go
	if req.Writable && !s.enabled("share_writable") {
		errResponse(w, 403, "editable shares are disabled on this instance")
		return
	}
```
`shares.go` — in `handleShareView`, change the PUT guard to:
```go
		if !e.Writable || !s.enabled("share_writable") { errResponse(w, 403, "read-only share"); return }
```

- [ ] **Step 4: Run all tests**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config.go config_test.go admin.go routes.go shares.go
git commit -m "feat(config): per-instance feature flags gated at route registration

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Identity — `userFrom` + `require_user`

**Files:**
- Create: `identity.go`, `identity_test.go`
- Modify: `routes.go` (`handler()`)

**Interfaces:**
- Consumes: `AdminConfig.RequireUser` (Task 3), test helpers (Task 1).
- Produces:
  - `const remoteUserHeader = "Remote-User"`
  - `func userFrom(r *http.Request) string` — trimmed header value, or `"unknown"`.
  - `func (s *server) requireUser(next http.Handler) http.Handler`
  - `handler()` becomes `gzipMiddleware(originGuard(s.requireUser(newRateLimiter(s.routes(), 240, time.Minute))))`.

- [ ] **Step 1: Write the failing tests**

`identity_test.go`:
```go
package main

import (
	"net/http/httptest"
	"testing"
)

func TestUserFrom(t *testing.T) {
	for _, c := range []struct{ hdr, want string }{
		{"alice", "alice"},
		{"  joao  ", "joao"},
		{"", "unknown"},
		{"   ", "unknown"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.hdr != "" {
			r.Header.Set("Remote-User", c.hdr)
		}
		if got := userFrom(r); got != c.want {
			t.Errorf("userFrom(%q) = %q, want %q", c.hdr, got, c.want)
		}
	}
}

func TestRequireUser(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}, RequireUser: true})
	h := s.handler()
	user := map[string]string{"Remote-User": "alice"}
	blank := map[string]string{"Remote-User": "   "}

	cases := []struct {
		path    string
		headers map[string]string
		want    int
	}{
		{"/api/vaults", nil, 401},
		{"/api/vaults", blank, 401},
		{"/api/vaults", user, 200},
		{"/", nil, 401},
		{"/health", nil, 200},
		{"/share/doesnotexist", nil, 404},       // exempt → reaches the share handler
		{"/share/tok/asset?name=katex.min.css", nil, 404}, // exempt (token unknown → 404, not 401)
		{"/share", nil, 401},                    // not the /share/ prefix
		{"/sharefoo", nil, 401},
	}
	for _, c := range cases {
		if rec := do(t, h, "GET", c.path, "", c.headers); rec.Code != c.want {
			t.Errorf("GET %s %v = %d, want %d", c.path, c.headers, rec.Code, c.want)
		}
	}
}

func TestRequireUserOffByDefault(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	if rec := do(t, s.handler(), "GET", "/api/vaults", "", nil); rec.Code != 200 {
		t.Fatalf("default instance must not require a user, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `~/.local/go/bin/go test -run 'TestUserFrom|TestRequireUser' ./...`
Expected: FAIL — `userFrom` undefined.

- [ ] **Step 3: Implement**

`identity.go`:
```go
package main

import (
	"net/http"
	"strings"
)

// remoteUserHeader is set by Authelia via Traefik forwardAuth
// (authResponseHeaders), which overwrites any client-supplied value.
const remoteUserHeader = "Remote-User"

// userFrom returns who made the request, for attribution only — never
// for authorization. Missing or blank → "unknown".
func userFrom(r *http.Request) string {
	if u := strings.TrimSpace(r.Header.Get(remoteUserHeader)); u != "" {
		return u
	}
	return "unknown"
}

// requireUser makes the instance fail closed: with require_user on, a
// request that did not come through Authelia (no Remote-User) gets 401.
// Share links authenticate by token and /health is for probes.
func (s *server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.cfgMu.RLock()
		required := s.cfg.RequireUser
		s.cfgMu.RUnlock()
		if !required || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/share/") {
			next.ServeHTTP(w, r)
			return
		}
		if userFrom(r) == "unknown" {
			errResponse(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```
`routes.go`:
```go
func (s *server) handler() http.Handler {
	return gzipMiddleware(originGuard(s.requireUser(newRateLimiter(s.routes(), 240, time.Minute))))
}
```

- [ ] **Step 4: Run all tests**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add identity.go identity_test.go routes.go
git commit -m "feat(identity): Remote-User attribution + fail-closed require_user

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Version, `/api/instance`, single deploy command

**Files:**
- Create: `instance.go`, `instance_test.go`, `deploy.sh`
- Modify: `admin.go` (`handleHealth` → moved to `instance.go`), `routes.go`, `Dockerfile`, `~/docker/stacks/office/compose/vaultreader.yml`

**Interfaces:**
- Consumes: `userFrom` (Task 4), `resolvedFeatures`, `shareBaseURL` (Task 3).
- Produces:
  - `var version = "dev"` (set by `-ldflags "-X main.version=<sha>"`).
  - `type InstanceInfo struct { Version string; User string; Features map[string]bool; RWPaths []string; ShareBaseURL string; DefaultNote string }` with JSON tags `version, user, features, rw_paths, share_base_url, default_note`.
  - `GET /api/instance` → `InstanceInfo`; `GET /health` → `{"status":"ok","version":"<sha>"}`.
  - `deploy.sh` — the only supported deploy entry point.

- [ ] **Step 1: Write the failing tests**

`instance_test.go`:
```go
package main

import (
	"encoding/json"
	"testing"
)

func TestInstanceInfo(t *testing.T) {
	s := newTestServer(t, AdminConfig{
		RWPaths:      []string{"jll"},
		ShareBaseURL: "https://team.joao.date/share/",
		Features:     map[string]bool{"webdav": false},
	})
	rec := do(t, s.handler(), "GET", "/api/instance", "", map[string]string{"Remote-User": "alice"})
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
	var got InstanceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.User != "alice" || got.Version != version || got.ShareBaseURL != "https://team.joao.date/share/" {
		t.Fatalf("unexpected %+v", got)
	}
	if got.Features["webdav"] || !got.Features["shares"] || len(got.Features) != len(knownFeatures) {
		t.Fatalf("features %+v", got.Features)
	}
}

func TestHealthReportsVersion(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	rec := do(t, s.handler(), "GET", "/health", "", nil)
	var got map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["status"] != "ok" || got["version"] != version {
		t.Fatalf("health %v", got)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `~/.local/go/bin/go test -run 'TestInstanceInfo|TestHealthReportsVersion' ./...`
Expected: FAIL — `InstanceInfo` undefined.

- [ ] **Step 3: Implement**

`instance.go`:
```go
package main

import "net/http"

// version is stamped at build time: -ldflags "-X main.version=<git sha>".
var version = "dev"

// InstanceInfo is everything the SPA needs to adapt to this instance.
type InstanceInfo struct {
	Version      string          `json:"version"`
	User         string          `json:"user"`
	Features     map[string]bool `json:"features"`
	RWPaths      []string        `json:"rw_paths"`
	ShareBaseURL string          `json:"share_base_url"`
	DefaultNote  string          `json:"default_note"`
}

func (s *server) handleInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	s.cfgMu.RLock()
	rw := append([]string{}, s.cfg.RWPaths...)
	defaultNote := s.cfg.DefaultNote
	s.cfgMu.RUnlock()
	jsonResponse(w, InstanceInfo{
		Version:      version,
		User:         userFrom(r),
		Features:     s.resolvedFeatures(),
		RWPaths:      rw,
		ShareBaseURL: s.shareBaseURL(),
		DefaultNote:  defaultNote,
	})
}

// handleHealth is the liveness probe; version lets ops detect drift
// between instances that should run the same image.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"status": "ok", "version": version})
}
```
Delete the old `handleHealth` from `admin.go`. In `routes.go` add `mux.HandleFunc("/api/instance", s.handleInstance)` next to `/api/writable-paths`.

`Dockerfile` stage 1 — add before the `go build` line and change the build:
```dockerfile
ARG GIT_SHA=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w -X main.version=${GIT_SHA}" -trimpath -o vaultreader .
```

`~/docker/stacks/office/compose/vaultreader.yml` — under `build:` add:
```yaml
      args:
        GIT_SHA: ${VR_GIT_SHA:-dev}
```

`deploy.sh`:
```bash
#!/usr/bin/env bash
# The ONLY supported way to deploy VaultReader. Builds the image once and
# recreates every container that runs it, so all instances stay on the
# same code. Stamps the git SHA (suffix -dirty for uncommitted changes).
set -euo pipefail
cd "$(dirname "$0")"
sha=$(git rev-parse --short HEAD)
git diff --quiet HEAD -- . || sha="${sha}-dirty"
services="vaultreader"
if [ -f "$HOME/docker/stacks/office/compose/vaultreader-jll.yml" ]; then
  services="vaultreader vaultreader-jll"
fi
VR_GIT_SHA="$sha" bash -lic "cd ~/docker && dc-office-up -d --build $services"
echo "deployed $sha → $services"
```
`chmod +x deploy.sh`.

- [ ] **Step 4: Run tests, deploy the main instance, verify**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...` → PASS.
Commit first (so the SHA is clean), then:
```bash
./deploy.sh
IP=$(docker inspect vaultreader --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}')
curl -s http://$IP:8080/health
curl -s http://$IP:8080/api/instance
```
Expected: `{"status":"ok","version":"<short sha>"}`; instance JSON with all features `true`, `share_base_url` `https://joao.date/notas/`, user `unknown`. Then open `notes.joao.date` in a browser and confirm nothing changed visually.

- [ ] **Step 5: Commit**

```bash
git add instance.go instance_test.go admin.go routes.go Dockerfile deploy.sh
git commit -m "feat: /api/instance, versioned /health, single deploy.sh

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
cd ~/docker && git add stacks/office/compose/vaultreader.yml && git commit -m "office: pass VR_GIT_SHA build arg to vaultreader

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```
(`~/docker` contains the submodule pointer; do not commit the pointer until Task 11.)

---

### Task 6: Attribution — audit log + share `created_by`

**Files:**
- Create: `audit.go`, `audit_test.go`
- Modify: `admin.go` (`server` struct), `main.go` (construct), `testutil_test.go` (construct), `notes.go`, `trash.go`, `attachments.go`, `shares.go`

**Interfaces:**
- Consumes: `userFrom` (Task 4).
- Produces:
  - `type AuditEntry struct { TS int64 "ts"; User string "user"; Action string "action"; Vault string "vault"; Path string "path"; To string "to,omitempty"; Bytes int64 "bytes,omitempty" }`
  - `func newAuditLog(appdataDir string) *auditLog`; `func (a *auditLog) append(e AuditEntry) error`
  - `func (s *server) audit(r *http.Request, action, vault, path, to string, bytes int64)` — never returns an error.
  - Actions (exact): `save`, `create`, `upload`, `move`, `delete`, `restore`, `share`, `revoke`.
  - `ShareEntry.CreatedBy string \`json:"created_by,omitempty"\``.
  - File: `<appdata>/audit.jsonl`, one JSON object per line. **The committer (Task 10) depends on this exact shape.**

- [ ] **Step 1: Write the failing tests**

`audit_test.go`:
```go
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readAudit(t *testing.T, s *server) []AuditEntry {
	t.Helper()
	f, err := os.Open(filepath.Join(s.appdataDir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []AuditEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func TestAuditRecordsEachWrite(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	h := s.handler()
	u := map[string]string{"Remote-User": "alice"}

	steps := []struct{ method, target, body, action string }{
		{"POST", "/api/note?vault=jll&path=a.md", `{"raw":"# A"}`, "create"},
		{"PUT", "/api/note?vault=jll&path=a.md", "# A2", "save"},
		{"POST", "/api/move?vault=jll&from=a.md&to=sub/b.md", "", "move"},
		{"POST", "/api/folder?vault=jll&path=newdir", "", "create"},
		{"DELETE", "/api/note?vault=jll&path=sub/b.md", "", "delete"},
		{"POST", "/api/shares/create", `{"vault":"jll","path":"x.md"}`, "share"},
	}
	for _, st := range steps {
		if rec := do(t, h, st.method, st.target, st.body, u); rec.Code >= 300 {
			t.Fatalf("%s %s = %d %s", st.method, st.target, rec.Code, rec.Body.String())
		}
	}
	got := readAudit(t, s)
	if len(got) != len(steps) {
		t.Fatalf("audit lines = %d, want %d: %+v", len(got), len(steps), got)
	}
	for i, st := range steps {
		if got[i].Action != st.action || got[i].User != "alice" || got[i].Vault != "jll" || got[i].TS == 0 {
			t.Errorf("line %d = %+v, want action %s by alice", i, got[i], st.action)
		}
	}
	if got[2].Path != "a.md" || got[2].To != "sub/b.md" {
		t.Errorf("move line = %+v", got[2])
	}
	if got[1].Bytes != int64(len("# A2\n")) {
		t.Errorf("save bytes = %d", got[1].Bytes)
	}
}

func TestAuditFailureDoesNotFailRequest(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	// Make audit.jsonl a directory so the append fails.
	if err := os.MkdirAll(filepath.Join(s.appdataDir, "audit.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := do(t, s.handler(), "POST", "/api/note?vault=jll&path=a.md", `{"raw":"x"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("create = %d, want 201 despite audit failure", rec.Code)
	}
}

func TestShareCreatedBy(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	rec := do(t, s.handler(), "POST", "/api/shares/create", `{"vault":"jll","path":"x.md"}`,
		map[string]string{"Remote-User": "alice"})
	var e ShareEntry
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.CreatedBy != "alice" {
		t.Fatalf("created_by = %q", e.CreatedBy)
	}
}
```
(Note: `saveNote` normalizes to exactly one trailing newline, so `bytes` for `"# A2"` is 5.)

- [ ] **Step 2: Run to verify they fail**

Run: `~/.local/go/bin/go test -run 'TestAudit|TestShareCreatedBy' ./...`
Expected: FAIL — `AuditEntry` undefined.

- [ ] **Step 3: Implement the log**

`audit.go`:
```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry is one line of appdata/audit.jsonl. The host-side committer
// (docker repo: scripts/vaults/vault_commit_authors.py) parses this exact
// shape to attribute git commits — change both together.
type AuditEntry struct {
	TS     int64  `json:"ts"`
	User   string `json:"user"`
	Action string `json:"action"` // save|create|upload|move|delete|restore|share|revoke
	Vault  string `json:"vault"`
	Path   string `json:"path"`
	To     string `json:"to,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type auditLog struct {
	mu   sync.Mutex
	path string
}

func newAuditLog(appdataDir string) *auditLog {
	return &auditLog{path: filepath.Join(appdataDir, "audit.jsonl")}
}

func (a *auditLog) append(e AuditEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// audit records who changed what. Attribution is not security: a failed
// append is logged and the request carries on.
func (s *server) audit(r *http.Request, action, vault, path, to string, bytes int64) {
	if s.auditLog == nil {
		return
	}
	e := AuditEntry{TS: time.Now().Unix(), User: userFrom(r), Action: action,
		Vault: vault, Path: path, To: to, Bytes: bytes}
	if err := s.auditLog.append(e); err != nil {
		log.Printf("audit: %v", err)
	}
}
```
`admin.go` — add field to `server`: `auditLog *auditLog`.
`main.go` — in the `srv := &server{…}` literal add `auditLog: newAuditLog(*appdataDir),`.
`testutil_test.go` — in `newTestServer` add `auditLog: newAuditLog(appdata),`.

- [ ] **Step 4: Add the audit calls (one per successful write)**

Insert immediately **before** each success response:

| File / handler | Line before | Insert |
|---|---|---|
| `notes.go` `handleCreateNote` | `w.WriteHeader(http.StatusCreated)` (~L124) | `s.audit(r, "create", vault, path, "", int64(len(normalizeMarkdown(body.Raw))))` |
| `notes.go` `handleDeleteNote` | `jsonResponse(… "deleted" …)` (~L168) | `s.audit(r, "delete", vault, path, "", 0)` |
| `notes.go` `handleRenameFolder` | `jsonResponse(… "renamed" …)` (~L254) | `s.audit(r, "move", vault, from, to, 0)` |
| `notes.go` `handleCreateFolder` | `w.WriteHeader(http.StatusCreated)` (~L287) | `s.audit(r, "create", vault, path, "", 0)` |
| `notes.go` `handleDeleteFolder` | `jsonResponse(… "deleted" …)` (~L345) | `s.audit(r, "delete", vault, path, "", 0)` |
| `notes.go` `handleMove` | `jsonResponse(… "moved" …)` (~L418) | `s.audit(r, "move", vault, from, to, 0)` |
| `notes.go` `handlePutNote` | `w.Header().Set("Content-Type", …)` after `newMTime` (~L540) | `s.audit(r, "save", vault, path, "", int64(len(normalizeMarkdown(content))))` |
| `notes.go` `handleUpload` | final `jsonResponse` (~L648) | `s.audit(r, "upload", vault, relFilePath, "", hdr.Size)` |
| `trash.go` `handleTrashRestore` | `w.WriteHeader(http.StatusNoContent)` | `s.audit(r, "restore", vault, originalBase, "", 0)` |
| `attachments.go` DELETE branch | `w.WriteHeader(http.StatusNoContent)` (~L69) | `s.audit(r, "delete", vault, path, "", 0)` |
| `shares.go` `handleShareCreate` | `jsonResponse(…create…)` | see below |
| `shares.go` `handleShareRevoke` | `s.shares.revoke(token)` | `s.audit(r, "revoke", "", token, "", 0)` |
| `shares.go` `handleShareRevokeAll` | `count := …` | `s.audit(r, "revoke", "", "*", "", 0)` |
| `shares.go` share-view PUT | `jsonResponse(… "saved" …)` | `s.audit(r, "save", e.Vault, e.Path, "", int64(len(normalizeMarkdown(body.Raw))))` — `userFrom` gives `unknown` there; acceptable (shares never carry Remote-User) |

Verify the variable names above against the current code before inserting (e.g. `handleRenameFolder` uses `from`/`to`; `handleCreateFolder` uses `path`). If `normalizeMarkdown` is not idempotent with what `saveNote` writes, use `os.Stat(full).Size()` after the save instead.

`shares.go` — `ShareEntry` gains `CreatedBy string \`json:"created_by,omitempty"\``; `ShareStore.create` gains a `createdBy string` last parameter assigned to `CreatedBy`. In `handleShareCreate`:
```go
	e := s.shares.create(req.Vault, req.Path, req.Writable, req.TTL, req.Label, userFrom(r))
	s.audit(r, "share", req.Vault, req.Path, "", 0)
	jsonResponse(w, e)
```
Update the other `s.shares.create(` call site in `config_test.go` (`TestShareWritableDisabled`) to pass `"test"` as the last argument.

- [ ] **Step 5: Run all tests, commit**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...` → PASS.
```bash
git add audit.go audit_test.go admin.go main.go testutil_test.go notes.go trash.go attachments.go shares.go config_test.go
git commit -m "feat(audit): append-only audit.jsonl per instance + share created_by

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Index hygiene + periodic rescan + `meta=1`

**Files:**
- Create: `rescan.go`, `rescan_test.go`
- Modify: `index.go` (`buildAll`), `main.go` (start rescanner), `notes.go` (`handleGetNote`)

**Interfaces:**
- Consumes: `NoteIndex.updateNote`, `NoteIndex.removeNote` (existing, `index.go`), `shouldSkip` (`files.go`).
- Produces:
  - `func walkVaultFiles(vaultsDir string, fn func(vault, rel string, info os.FileInfo))` — visits every regular file under every vault, **skipping any directory or file whose name `shouldSkip`** (so `.trash`, `.git`, `.obsidian`, `.vr` are never descended).
  - `type rescanner struct` + `func newRescanner(vaultsDir string, idx *NoteIndex) *rescanner` (primes its stamp map without touching the index) + `func (rs *rescanner) pass() (changed, removed int)` + `func (rs *rescanner) run(stop <-chan struct{}, every time.Duration)`.
  - `GET /api/note?…&meta=1` → `{"mtime":<unix>,"size":<bytes>}`; 404 if missing.

- [ ] **Step 1: Write the failing tests**

`rescan_test.go`:
```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildAllSkipsDotDirs(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "jll")
	writeFile(t, filepath.Join(v, "keep.md"), "# Keep\n")
	writeFile(t, filepath.Join(v, ".trash", makeTrashName("gone.md", 1)), "# Gone\n")
	writeFile(t, filepath.Join(v, ".obsidian", "x.md"), "# X\n")
	s.idx.buildAll(s.vaultsDir)

	rec := do(t, s.handler(), "GET", "/api/search?q=VRTRASH&vault=jll", "", nil)
	if strings.Contains(rec.Body.String(), ".trash") {
		t.Fatalf("trashed note is searchable: %s", rec.Body.String())
	}
	if _, _, ok := s.idx.resolve("x", "jll", ""); ok {
		t.Fatal(".obsidian note was indexed")
	}
	if _, _, ok := s.idx.resolve("keep", "jll", ""); !ok {
		t.Fatal("regular note missing from index")
	}
}

func TestRescanPicksUpExternalChanges(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	v := filepath.Join(s.vaultsDir, "jll")
	writeFile(t, filepath.Join(v, "a.md"), "# A\n")
	s.idx.buildAll(s.vaultsDir)
	rs := newRescanner(s.vaultsDir, s.idx)

	if c, r := rs.pass(); c != 0 || r != 0 {
		t.Fatalf("idle pass changed=%d removed=%d", c, r)
	}

	// Created outside the app.
	writeFile(t, filepath.Join(v, "b.md"), "# B\n[[a]]\n")
	// Modified outside the app (force a different mtime).
	writeFile(t, filepath.Join(v, "a.md"), "# A changed\n")
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(v, "a.md"), future, future)

	if c, _ := rs.pass(); c != 2 {
		t.Fatalf("changed = %d, want 2", c)
	}
	if bl := s.idx.getBacklinks("jll", "a.md"); len(bl) != 1 {
		t.Fatalf("backlinks to a.md = %d, want 1", len(bl))
	}

	// Deleted outside the app.
	_ = os.Remove(filepath.Join(v, "b.md"))
	if _, r := rs.pass(); r != 1 {
		t.Fatalf("removed = %d, want 1", r)
	}
	if _, _, ok := s.idx.resolve("b", "jll", ""); ok {
		t.Fatal("deleted note still resolvable")
	}
}

func TestNoteMeta(t *testing.T) {
	s := newTestServer(t, AdminConfig{})
	writeFile(t, filepath.Join(s.vaultsDir, "jll", "a.md"), "# A\n")
	h := s.handler()
	rec := do(t, h, "GET", "/api/note?vault=jll&path=a.md&meta=1", "", nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"mtime"`) || strings.Contains(body, `"html"`) {
		t.Fatalf("meta = %d %s", rec.Code, body)
	}
	if rec := do(t, h, "GET", "/api/note?vault=jll&path=nope.md&meta=1", "", nil); rec.Code != 404 {
		t.Fatalf("missing meta = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `~/.local/go/bin/go test -run 'TestBuildAllSkipsDotDirs|TestRescan|TestNoteMeta' ./...`
Expected: FAIL — `.trash` note searchable; `newRescanner` undefined.

- [ ] **Step 3: Implement the shared walker and use it in `buildAll`**

`rescan.go`:
```go
package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// walkVaultFiles visits every regular file in every vault, never
// descending into skipped directories (.trash, .git, .obsidian, .vr, …).
// buildAll and the rescanner share it so they always agree on what
// belongs in the index.
func walkVaultFiles(vaultsDir string, fn func(vault, rel string, info os.FileInfo)) {
	entries, err := os.ReadDir(vaultsDir)
	if err != nil {
		log.Printf("walk: cannot read vaults dir %s: %v", vaultsDir, err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		vault := e.Name()
		root := filepath.Join(vaultsDir, vault)
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if p != root && shouldSkip(info.Name()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			fn(vault, rel, info)
			return nil
		})
	}
}

type fileStamp struct {
	mtime int64
	size  int64
}

// rescanner keeps the in-memory index in step with edits made outside
// this process (the other instance, Obsidian, Syncthing) by comparing
// stat() results between passes. Only changed .md files are read.
type rescanner struct {
	vaultsDir string
	idx       *NoteIndex
	seen      map[string]fileStamp // vault + "\x00" + rel
}

func newRescanner(vaultsDir string, idx *NoteIndex) *rescanner {
	rs := &rescanner{vaultsDir: vaultsDir, idx: idx}
	rs.seen = rs.snapshot()
	return rs
}

func (rs *rescanner) snapshot() map[string]fileStamp {
	out := map[string]fileStamp{}
	walkVaultFiles(rs.vaultsDir, func(vault, rel string, info os.FileInfo) {
		if strings.HasSuffix(rel, ".md") {
			out[vault+"\x00"+rel] = fileStamp{info.ModTime().UnixNano(), info.Size()}
		}
	})
	return out
}

func (rs *rescanner) pass() (changed, removed int) {
	now := rs.snapshot()
	for key, st := range now {
		if old, ok := rs.seen[key]; ok && old == st {
			continue
		}
		vault, rel, _ := strings.Cut(key, "\x00")
		data, err := os.ReadFile(filepath.Join(rs.vaultsDir, vault, rel))
		if err != nil {
			log.Printf("rescan: %s/%s: %v", vault, rel, err)
			delete(now, key) // retry next pass
			continue
		}
		rs.idx.updateNote(vault, rel, string(data))
		changed++
	}
	for key := range rs.seen {
		if _, ok := now[key]; !ok {
			vault, rel, _ := strings.Cut(key, "\x00")
			rs.idx.removeNote(vault, rel)
			removed++
		}
	}
	rs.seen = now
	return changed, removed
}

func (rs *rescanner) run(stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if c, r := rs.pass(); c+r > 0 {
				log.Printf("rescan: %d changed, %d removed", c, r)
			}
		}
	}
}
```
`index.go` — in `buildAll`, replace the `entries, err := os.ReadDir(vaultsPath)` … `for _, e := range entries { … filepath.Walk(…) … }` block with a single call that keeps the existing per-file body:
```go
	walkVaultFiles(vaultsPath, func(vaultName, rel string, info os.FileInfo) {
		if !strings.HasSuffix(info.Name(), ".md") {
			k := strings.ToLower(filepath.Base(rel))
			idx.byAttachment[k] = append(idx.byAttachment[k], NoteRef{Vault: vaultName, Path: rel})
			return
		}
		data, err := os.ReadFile(filepath.Join(vaultsPath, vaultName, rel))
		if err != nil {
			return
		}
		// …existing body from `content := string(data)` to the end of the
		// per-file closure, unchanged (return nil → return)…
	})
```
Keep the map initialisations at the top of `buildAll` unchanged. Remove imports that become unused.

`main.go` — after `srv.loadConfig()`:
```go
	go newRescanner(*vaultsDir, idx).run(srv.shutdown, 30*time.Second)
```

`notes.go` — in `handleGetNote`, directly after the `safePath` check:
```go
	if r.URL.Query().Get("meta") == "1" {
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			errResponse(w, 404, "note not found")
			return
		}
		jsonResponse(w, map[string]int64{"mtime": info.ModTime().Unix(), "size": info.Size()})
		return
	}
```

- [ ] **Step 4: Run all tests**

Run: `~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...` → PASS (including `TestRewriteNoteImageURLs` and the math tests — `buildAll` behaviour for normal notes must be unchanged).

- [ ] **Step 5: Commit**

```bash
git add rescan.go rescan_test.go index.go main.go notes.go
git commit -m "feat(index): 30s stat rescan for external edits; never index dot-dirs (.trash leak); note meta=1

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Frontend adapts to the instance

**Files:**
- Modify: `static/index.html`

**Interfaces:**
- Consumes: `GET /api/instance` (Task 5) → `{version, user, features, rw_paths, share_base_url, default_note}`.
- Produces: Alpine state `instance: { version: '', user: '', features: {}, share_base_url: 'https://joao.date/notas/', default_note: '' }`, helpers `feat(name)` and `shareURL(token)`.

- [ ] **Step 1: Drop the uncommitted hardcode**

```bash
git checkout -- static/index.html   # removes the pessoal/inbox/tarefas.md diff; replaced by default_note below
```
Then add `"default_note": "pessoal/inbox/tarefas.md"` to the **main** instance's `appdata/config.json` (gitignored; keep existing keys):
```bash
python3 - <<'EOF'
import json; p='appdata/config.json'; c=json.load(open(p)); c['default_note']='pessoal/inbox/tarefas.md'; json.dump(c,open(p,'w'),indent=2)
EOF
```

- [ ] **Step 2: State + loader**

In `vaultApp()` state (near `noteMTime: 0,` ~L1624) add:
```js
    // Per-instance capabilities from /api/instance. Defaults reproduce
    // the original single-user behaviour if the fetch fails.
    instance: { version: '', user: '', features: {}, share_base_url: 'https://joao.date/notas/', default_note: '' },
```
Add methods next to `loadWritablePaths()` (~L4900):
```js
    async loadInstance() {
      try {
        const r = await fetch('/api/instance')
        if (r.ok) this.instance = await r.json()
      } catch(e) { console.error('instance fetch:', e) }
    },
    // Missing feature key = enabled, same rule as the server.
    feat(name) { return this.instance.features[name] !== false },
    shareURL(token) { return this.instance.share_base_url + token },
```

- [ ] **Step 3: Wire it into `init()`**

In `init()` (~L1945), before `this.loadActiveShares()`:
```js
      await this.loadInstance()
```
Change `this.loadActiveShares()` to `if (this.feat('shares')) this.loadActiveShares()` and the sync interval to:
```js
      if (this.feat('sync_status')) setInterval(() => this.refreshSyncStatus(), 10000)
```
After `if (!matched) this._routeHash()` add:
```js
      // Nothing restored from the URL → open the instance's default note.
      if (!this.activePath && this.instance.default_note) {
        const i = this.instance.default_note.indexOf('/')
        const v = this.instance.default_note.slice(0, i)
        if (this.vaults.includes(v)) await this.openNote(v, this.instance.default_note.slice(i + 1))
      }
```

- [ ] **Step 4: Replace hardcoded share URLs and gate the UI**

Replace each `'https://joao.date/notas/' + X` / `'https://joao.date/notas/'+X` with `this.shareURL(X)` in JS, and `shareURL(X)` inside Alpine attributes. Known sites: L297 (`:href`), L1210 (clipboard), L4768, L4843, L4876. Verify none remain: `grep -n "joao.date/notas" static/index.html` → no output.

Gates (add to the existing attributes):
- Share button wrapper `<template x-if="activePath">` at ~L256 → `x-if="activePath && feat('shares')"`.
- "Editable · no expiry" quick-share button (~L318): `x-show="canWriteCurrent() && !viewerKind && feat('share_writable')"`.
- Share modal writable toggle (~L1089): wrap its row with `x-show="feat('share_writable')"`.
- Settings "Shared" tab button and body: add `x-show="feat('shares')"` to the tab button (find with `grep -n "settingsTab === 'shares'\|settingsTab = 'shares'" static/index.html`).
- "Empty all" trash button (~L1228) and any per-item permanent-delete button in the trash list: `x-show="feat('trash_empty')"`.
- Sync indicator (~L1381) already `x-show="syncAvailable"` → `x-show="syncAvailable && feat('sync_status')"`.

- [ ] **Step 5: Footer user + version**

In the status bar, after the `<span class="sb-sync" …>` element (~L1384):
```html
    <span class="sb-sep" x-show="instance.version">·</span>
    <span class="sb-instance" x-show="instance.version"
          x-text="(instance.user && instance.user !== 'unknown' ? instance.user + ' · ' : '') + instance.version"
          title="Signed-in user · build"></span>
```
In `static/style.css` (status bar section):
```css
.sb-instance { color: var(--text3); font-variant-numeric: tabular-nums; }
```

- [ ] **Step 6: Verify**

Run the `node --check` snippet from `CLAUDE.md` → no output. Then `./deploy.sh`, and with Playwright against `http://$IP:8080/` (main instance, no header): footer shows `<sha>`; share button visible; editable option visible; `pessoal/inbox/tarefas.md` opens on a bare `/`; console has no errors. `grep -n "joao.date/notas" static/index.html` → nothing.

- [ ] **Step 7: Commit**

```bash
git add static/index.html static/style.css
git commit -m "feat(ui): adapt to /api/instance — share base URL, default note, feature-gated UI, user+version footer

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Frontend — pick up changes to the open note

**Files:**
- Modify: `static/index.html`

**Interfaces:**
- Consumes: `GET /api/note?…&meta=1` (Task 7); existing `openNote`, `_showConflictModal(diskRaw, diskMtime)`, `takeTheirs()`, state `mode`, `isDirty`, `noteMTime`, `viewerKind`.
- Produces: `checkOpenNoteFreshness()`; state `_freshWarnedMtime: 0`.

- [ ] **Step 1: Add the check**

State (next to `instance:`): `_freshWarnedMtime: 0,`
Method (next to `takeTheirs()`):
```js
    // Another instance / Obsidian / Syncthing may change the open note.
    // Preview follows the disk; edit mode never loses typed text — a dirty
    // buffer gets the existing conflict modal, once per disk version.
    async checkOpenNoteFreshness() {
      if (!this.activeVault || !this.activePath || this.viewerKind) return
      const q = '/api/note?vault=' + encodeURIComponent(this.activeVault) +
                '&path=' + encodeURIComponent(this.activePath)
      let r
      try { r = await fetch(q + '&meta=1') } catch(_) { return }
      if (r.status === 404) {
        if (this.mode === 'edit' && this.isDirty) return // keep the buffer; the save will recreate it
        await this.openNote(this.activeVault, this.activePath) // shows "This note no longer exists"
        return
      }
      if (!r.ok) return
      const meta = await r.json()
      if (!meta.mtime || meta.mtime <= this.noteMTime + 1) return
      if (this.mode !== 'edit') {
        const el = document.getElementById('preview')
        const top = el ? el.scrollTop : 0
        await this.openNote(this.activeVault, this.activePath)
        this.$nextTick(() => { const p = document.getElementById('preview'); if (p) p.scrollTop = top })
        return
      }
      if (this._freshWarnedMtime === meta.mtime) return
      this._freshWarnedMtime = meta.mtime
      const full = await (await fetch(q)).json()
      this._conflictDiskRaw = full.raw || ''
      this._conflictDiskMtime = full.mtime || 0
      if (this.isDirty) this._showConflictModal(full.raw || '', full.mtime || 0)
      else this.takeTheirs() // clean buffer: just follow the disk
    },
```

- [ ] **Step 2: Schedule it**

In `init()`, after the sync interval line:
```js
      setInterval(() => this.checkOpenNoteFreshness(), 60000)
      document.addEventListener('visibilitychange', () => {
        if (document.visibilityState === 'visible') this.checkOpenNoteFreshness()
      })
```

- [ ] **Step 3: Verify**

`node --check` snippet → clean. `./deploy.sh`. Playwright against the main instance bridge IP, using a test note `pessoal/agents/claude/__vr_fresh.md` (create via API, delete at the end):
1. Open it in preview; from the shell append a line (`echo "line2" >> ~/vaults/pessoal/agents/claude/__vr_fresh.md`); call `Alpine.$data(document.querySelector('#app')).checkOpenNoteFreshness()` via `browser_evaluate`; expect the preview to contain `line2`.
2. Switch to edit, type text (dirty), append another line from the shell, call the check → conflict modal titled "Note changed on disk" appears; calling it again does not open a second modal.
3. Edit mode, clean buffer, external change → editor content updates, no modal.
4. Delete the file from the shell in preview → "This note no longer exists."
Clean up: `rm -f ~/vaults/pessoal/agents/claude/__vr_fresh.md`.

- [ ] **Step 4: Commit**

```bash
git add static/index.html
git commit -m "feat(ui): follow on-disk changes to the open note (preview reloads, edit warns)

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Host committer — per-author commits from `audit.jsonl`

**Files (docker repo `~/docker`):**
- Create: `scripts/vaults/vault_commit_authors.py`, `scripts/vaults/test_vault_commit_authors.py`, `scripts/vaults/vault-commit-authors.example.json`
- Create (host, not in a repo): `~/.config/vault-commit-authors.json`, `~/.config/systemd/user/vault-commit-authors.service`, `~/.config/systemd/user/vault-commit-authors.timer`

**Interfaces:**
- Consumes: `audit.jsonl` lines exactly as `AuditEntry` (Task 6): `{"ts","user","action","vault","path","to"?,"bytes"?}`.
- Produces:
  - `read_new_entries(audit_path: str, offset: int) -> tuple[list[dict], int]` — returns parsed complete lines from `offset` and the byte offset just after the last **complete** line (a trailing partial line is left for next time; malformed complete lines are skipped).
  - `to_repo_paths(entry: dict, source: dict) -> list[str]` — repo-relative paths (`path` and `to`), with `source["prefix"]` prepended; `[]` for entries whose vault ≠ `source["vault"]`, for `share`/`revoke`.
  - `group_by_author(entries: list[tuple[dict, list[str]]]) -> dict[str, set[str]]` — **user contribution**, see Step 3.
  - `commit_window(repo, groups, authors, fallback, run=subprocess.run) -> list[str]` — per-author commits, then a fallback commit for leftovers; returns created commit subjects.
  - `main(config_path, state_path) -> int`.
  - State file `~/.local/state/vault-commit-authors/state.json`: `{"offsets": {"<audit path>": <int>}, "last_success": <unix>}`.

- [ ] **Step 1: Baseline snapshot of the work vault**

The repo has ~466 uncommitted changes since the last manual snapshot (2026-10-06). Commit them with the existing convention so the first automated commit doesn't swallow them:
```bash
cd ~/vaults/work && git add -A && git commit -q -m "snapshot $(date -Iseconds): sync vault state" && git log --oneline -1
```

- [ ] **Step 2: Write the failing tests**

`scripts/vaults/test_vault_commit_authors.py`:
```python
import json
import os
import subprocess
import tempfile
import unittest

import vault_commit_authors as vca

SRC_JLL = {"audit": "", "vault": "jll", "prefix": "jll/"}
SRC_MAIN = {"audit": "", "vault": "work", "prefix": ""}


class ReadEntries(unittest.TestCase):
    def test_partial_and_malformed_lines(self):
        with tempfile.TemporaryDirectory() as d:
            p = os.path.join(d, "audit.jsonl")
            good = json.dumps({"ts": 1, "user": "alice", "action": "save", "vault": "jll", "path": "a.md"})
            with open(p, "w") as f:
                f.write(good + "\n" + "{not json}\n" + '{"ts": 2, "user": "jo')  # last line truncated
            entries, off = vca.read_new_entries(p, 0)
            self.assertEqual([e["path"] for e in entries], ["a.md"])
            self.assertEqual(off, len(good) + 1 + len("{not json}") + 1)
            # Completing the line later makes it readable from the stored offset.
            with open(p, "a") as f:
                f.write('ao", "action": "save", "vault": "jll", "path": "b.md"}\n')
            entries, _ = vca.read_new_entries(p, off)
            self.assertEqual([e["path"] for e in entries], ["b.md"])

    def test_missing_file_is_empty(self):
        self.assertEqual(vca.read_new_entries("/nonexistent/audit.jsonl", 0), ([], 0))


class RepoPaths(unittest.TestCase):
    def test_prefix_move_and_filters(self):
        mv = {"user": "alice", "action": "move", "vault": "jll", "path": "a", "to": "b/a"}
        self.assertEqual(vca.to_repo_paths(mv, SRC_JLL), ["jll/a", "jll/b/a"])
        other = {"user": "joao", "action": "save", "vault": "pessoal", "path": "x.md"}
        self.assertEqual(vca.to_repo_paths(other, SRC_MAIN), [])
        share = {"user": "joao", "action": "share", "vault": "work", "path": "x.md"}
        self.assertEqual(vca.to_repo_paths(share, SRC_MAIN), [])


class Grouping(unittest.TestCase):
    def test_last_author_wins_per_path(self):
        items = [
            ({"ts": 1, "user": "joao"}, ["jll/a.md"]),
            ({"ts": 2, "user": "alice"}, ["jll/a.md", "jll/b.md"]),
            ({"ts": 3, "user": "joao"}, ["jll/c.md"]),
        ]
        self.assertEqual(vca.group_by_author(items), {"alice": {"jll/a.md", "jll/b.md"}, "joao": {"jll/c.md"}})

    def test_unknown_user_kept_as_is(self):
        self.assertEqual(vca.group_by_author([({"ts": 1, "user": "unknown"}, ["x"])]), {"unknown": {"x"}})


class Commit(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = self.tmp.name
        run = lambda *a: subprocess.run(["git", "-C", self.repo, *a], check=True, capture_output=True)
        run("init", "-q")
        run("config", "user.name", "joaompfp")
        run("config", "user.email", "joao@example.com")
        os.makedirs(os.path.join(self.repo, "jll", "dir"))
        for p in ("jll/a.md", "jll/dir/x.md", "other.md"):
            open(os.path.join(self.repo, p), "w").write("v1\n")
        run("add", "-A")
        run("commit", "-q", "-m", "init")
        self.git = run

    def tearDown(self):
        self.tmp.cleanup()

    def log(self):
        out = subprocess.run(["git", "-C", self.repo, "log", "--format=%an|%s", "--name-status"],
                             capture_output=True, text=True, check=True).stdout
        return out

    def test_per_author_then_fallback(self):
        open(os.path.join(self.repo, "jll/a.md"), "w").write("v2\n")           # alice
        os.rename(os.path.join(self.repo, "jll/dir"), os.path.join(self.repo, "jll/moved"))  # alice folder move
        open(os.path.join(self.repo, "other.md"), "w").write("v2\n")           # Obsidian, not in audit
        groups = {"alice": {"jll/a.md", "jll/dir", "jll/moved"}}
        authors = {"alice": "Alice <alice@example.com>"}
        subjects = vca.commit_window(self.repo, groups, authors, "joaompfp <joao@example.com>")
        self.assertEqual(subjects, ["vr(alice): 3 changes", "vr(sync): 1 changes"])
        log = self.log()
        self.assertIn("Alice|vr(alice): 3 changes", log)
        self.assertIn("joaompfp|vr(sync): 1 changes", log)
        status = subprocess.run(["git", "-C", self.repo, "status", "--porcelain"],
                                capture_output=True, text=True).stdout
        self.assertEqual(status, "")

    def test_no_changes_no_commits(self):
        self.assertEqual(vca.commit_window(self.repo, {"alice": {"jll/a.md"}}, {}, "joaompfp <joao@example.com>"), [])


class MainOffsets(unittest.TestCase):
    def test_offset_not_advanced_on_git_failure(self):
        with tempfile.TemporaryDirectory() as d:
            audit = os.path.join(d, "audit.jsonl")
            open(audit, "w").write(json.dumps({"ts": 1, "user": "alice", "action": "save", "vault": "jll", "path": "a.md"}) + "\n")
            cfg = os.path.join(d, "cfg.json")
            json.dump({"repo": os.path.join(d, "not-a-repo"), "sources": [{"audit": audit, "vault": "jll", "prefix": "jll/"}],
                       "authors": {}, "fallback_author": "x <x@x>"}, open(cfg, "w"))
            state = os.path.join(d, "state.json")
            self.assertNotEqual(vca.main(cfg, state), 0)
            self.assertFalse(os.path.exists(state))


if __name__ == "__main__":
    unittest.main()
```

Run: `cd ~/docker/scripts/vaults && python3 -m unittest -v test_vault_commit_authors 2>&1 | tail -3`
Expected: FAIL — `ModuleNotFoundError: vault_commit_authors`.

- [ ] **Step 3: Implement — with the grouping function written by the user**

`scripts/vaults/vault_commit_authors.py`:
```python
#!/usr/bin/env python3
"""Turn VaultReader audit logs into per-author git commits in a vault repo.

VaultReader instances append one JSON line per write to appdata/audit.jsonl
(see vaultreader/audit.go → AuditEntry). This script, run by a systemd user
timer every 5 minutes, reads the new lines, commits the files each user
touched with that user as git author, then commits anything else (Obsidian,
Syncthing, edits outside VaultReader) as the fallback author.
"""
import json
import os
import subprocess
import sys
import tempfile
import time

IGNORED_ACTIONS = {"share", "revoke"}


def read_new_entries(audit_path, offset):
    try:
        with open(audit_path, "rb") as f:
            f.seek(offset)
            data = f.read()
    except FileNotFoundError:
        return [], offset
    end = data.rfind(b"\n") + 1  # only complete lines; a partial tail waits
    entries = []
    for raw in data[:end].splitlines():
        try:
            entries.append(json.loads(raw))
        except json.JSONDecodeError:
            print(f"skip malformed audit line: {raw[:80]!r}", file=sys.stderr)
    return entries, offset + end


def to_repo_paths(entry, source):
    if entry.get("vault") != source["vault"] or entry.get("action") in IGNORED_ACTIONS:
        return []
    return [source["prefix"] + p for p in (entry.get("path"), entry.get("to")) if p]


def group_by_author(entries):
    """Map each author to the repo paths they last touched in this window.

    entries: list of (audit_entry, repo_paths) in log order.
    A path touched by several users is attributed to the LAST one only
    (spec §3.2 known limit); the audit log keeps the full sequence.
    """
    # TODO(joao): implement — see the plan's Step 3 guidance.
    raise NotImplementedError


def _git(repo, *args, run=subprocess.run):
    return run(["git", "-C", repo, *args], check=True, capture_output=True, text=True)


def _has_staged(repo, run):
    return run(["git", "-C", repo, "diff", "--cached", "--quiet"]).returncode == 1


def commit_window(repo, groups, authors, fallback, run=subprocess.run):
    subjects = []
    for user in sorted(groups):
        paths = sorted(groups[user])
        _git(repo, "add", "-A", "--", *paths, run=run)
        if not _has_staged(repo, run):
            continue
        n = len(_git(repo, "diff", "--cached", "--name-only", "--no-renames", run=run).stdout.split())
        subject = f"vr({user}): {n} changes"
        _git(repo, "commit", "-q", "--author", authors.get(user, fallback),
             "-m", subject, "-m", "\n".join(paths), run=run)
        subjects.append(subject)
    _git(repo, "add", "-A", run=run)
    if _has_staged(repo, run):
        n = len(_git(repo, "diff", "--cached", "--name-only", "--no-renames", run=run).stdout.split())
        subject = f"vr(sync): {n} changes"
        _git(repo, "commit", "-q", "--author", fallback, "-m", subject, run=run)
        subjects.append(subject)
    return subjects


def _load_json(path, default):
    try:
        with open(path) as f:
            return json.load(f)
    except FileNotFoundError:
        return default


def _save_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path))
    with os.fdopen(fd, "w") as f:
        json.dump(data, f, indent=2)
    os.replace(tmp, path)


def main(config_path, state_path):
    cfg = _load_json(config_path, None)
    if cfg is None:
        print(f"missing config {config_path}", file=sys.stderr)
        return 2
    state = _load_json(state_path, {"offsets": {}, "last_success": 0})
    items, new_offsets = [], {}
    for src in cfg["sources"]:
        entries, off = read_new_entries(src["audit"], state["offsets"].get(src["audit"], 0))
        new_offsets[src["audit"]] = off
        items += [(e, p) for e in entries if (p := to_repo_paths(e, src))]
    items.sort(key=lambda it: it[0].get("ts", 0))
    try:
        subjects = commit_window(cfg["repo"], group_by_author(items), cfg["authors"], cfg["fallback_author"])
    except (subprocess.CalledProcessError, FileNotFoundError, NotADirectoryError) as e:
        print(f"git failed, offsets kept: {e}", file=sys.stderr)
        return 1
    state["offsets"].update(new_offsets)
    state["last_success"] = int(time.time())
    _save_json(state_path, state)
    for s in subjects:
        print(s)
    return 0


if __name__ == "__main__":
    home = os.path.expanduser("~")
    raise SystemExit(main(
        os.environ.get("VCA_CONFIG", f"{home}/.config/vault-commit-authors.json"),
        os.environ.get("VCA_STATE", f"{home}/.local/state/vault-commit-authors/state.json"),
    ))
```

**USER CONTRIBUTION — `group_by_author` (5–8 lines).** Pause here and ask the user to write the body. Context to give them: `items` arrive sorted by `ts`; each item is `(entry, repo_paths)`; the result maps `entry["user"]` → set of paths; when two users touch the same path in one window only the last one keeps it (the tests in `Grouping` pin this, including that a user whose every path was taken over disappears from the result). Trade-off worth mentioning: "last author wins" vs. "co-author trailer for everyone who touched it" — the spec chose the former for simplicity. If the user prefers that the agent writes it, use:
```python
    owner = {}
    for entry, paths in entries:
        for p in paths:
            owner[p] = entry.get("user") or "unknown"
    groups = {}
    for p, user in owner.items():
        groups.setdefault(user, set()).add(p)
    return groups
```

- [ ] **Step 4: Run tests**

Run: `cd ~/docker/scripts/vaults && python3 -m unittest -v test_vault_commit_authors 2>&1 | tail -3`
Expected: `OK`.

- [ ] **Step 5: Config, example, timer**

Input needed from the user before this step: **the colleague's full name and email** (git author + Authelia). Ask once; reuse in Task 11.

`scripts/vaults/vault-commit-authors.example.json`:
```json
{
  "repo": "/home/joao/vaults/work",
  "sources": [
    {"audit": "/home/joao/docker/stacks/office/images/vaultreader/appdata/audit.jsonl", "vault": "work", "prefix": ""},
    {"audit": "/home/joao/docker/stacks/office/appdata/vaultreader-jll/audit.jsonl", "vault": "jll", "prefix": "jll/"}
  ],
  "authors": {
    "joao": "joaompfp <joao.mpfp@gmail.com>",
    "alice": "Alice Example <alice@example.com>"
  },
  "fallback_author": "joaompfp <joao.mpfp@gmail.com>"
}
```
Copy to `~/.config/vault-commit-authors.json` and put the colleague's real name/email there (not in the repo).

`~/.config/systemd/user/vault-commit-authors.service`:
```ini
[Unit]
Description=Per-author git commits for the work vault from VaultReader audit logs

[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /home/joao/docker/scripts/vaults/vault_commit_authors.py
```
`~/.config/systemd/user/vault-commit-authors.timer`:
```ini
[Unit]
Description=Run vault-commit-authors every 5 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
Persistent=true

[Install]
WantedBy=timers.target
```
```bash
systemctl --user daemon-reload
systemctl --user start vault-commit-authors.service && journalctl --user -u vault-commit-authors -n 5 --no-pager
systemctl --user enable --now vault-commit-authors.timer
systemctl --user list-timers vault-commit-authors.timer --no-pager
```
Expected: service exits 0 (nothing to commit, or a `vr(sync)` commit); timer listed.

- [ ] **Step 6: Commit**

```bash
cd ~/docker
git add scripts/vaults/
git commit -m "scripts: vault-commit-authors — per-author commits from VaultReader audit logs

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: The team instance — compose, network, Authelia, deploy, smoke

**Files (docker repo):**
- Create: `stacks/office/compose/vaultreader-jll.yml`
- Modify: `stacks/office/compose.yml`, `stacks/core/appdata/authelia/users_database.yml`, `stacks/core/appdata/authelia/configuration.yml`
- Create (gitignored appdata): `stacks/office/appdata/vaultreader-jll/config.json`, `stacks/office/appdata/vaultreader-jll/icons/jll.svg`

**Interfaces:**
- Consumes: everything above; `deploy.sh` (Task 5) picks up the new service automatically once the compose file exists.

- [ ] **Step 1: Pick a free subnet and confirm the DNS-mount convention**

```bash
docker network inspect $(docker network ls -q) --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}}{{end}}' | sort -k2
ls -l ~/docker/resolv.conf
```
Choose an unused `/24` (proposal `172.21.0.0/24`; use the next free one if taken). The container needs no outbound DNS, so the resolv.conf mount is only for convention; skip it if `~/docker/resolv.conf` doesn't exist and say so in the commit message.

- [ ] **Step 2: Appdata**

```bash
mkdir -p ~/docker/stacks/office/appdata/vaultreader-jll/icons
cat > ~/docker/stacks/office/appdata/vaultreader-jll/config.json <<'EOF'
{
  "require_user": true,
  "rw_paths": ["jll"],
  "share_base_url": "https://team.joao.date/share/",
  "features": {
    "shares": true,
    "share_writable": false,
    "webdav": false,
    "sync_status": false,
    "trash_empty": false
  }
}
EOF
cp ~/docker/stacks/office/images/vaultreader/appdata/icons/work.svg ~/docker/stacks/office/appdata/vaultreader-jll/icons/jll.svg 2>/dev/null || true
```
Confirm `stacks/office/appdata/` is gitignored in `~/docker` (`git check-ignore -v stacks/office/appdata/vaultreader-jll/config.json`); if it isn't, stop and ask.

- [ ] **Step 3: Compose file**

`stacks/office/compose/vaultreader-jll.yml` (replace `172.21.0.0/24` with the subnet from Step 1):
```yaml
# Team instance of VaultReader: serves ONLY ~/vaults/work/jll to the
# Authelia group jll-team at team.joao.date. Same image as `vaultreader`
# (no build: here — deploy both with images/vaultreader/deploy.sh).
# Own bridge network: other t2_proxy containers cannot reach it; Traefik
# (host network) can.
networks:
  vr_jll_net:
    name: vr_jll_net
    driver: bridge
    ipam:
      config:
        - subnet: 172.21.0.0/24

services:
  vaultreader-jll:
    image: vaultreader:local
    container_name: vaultreader-jll
    user: "1000:1000"
    depends_on:
      - vaultreader
    volumes:
      - /home/joao/vaults/work/jll:/vaults/jll:rw
      - ${DOCKER_DIR}/stacks/office/appdata/vaultreader-jll:/appdata:rw
      - ${DOCKER_DIR}/resolv.conf:/etc/resolv.conf:ro
    networks:
      - vr_jll_net
    restart: unless-stopped
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=vr_jll_net"
      # App — Authelia (group jll-team, two_factor)
      - "traefik.http.routers.vaultreader-jll-rtr.rule=Host(`team.${DOMAINNAME}`)"
      - "traefik.http.routers.vaultreader-jll-rtr.entrypoints=websecure"
      - "traefik.http.routers.vaultreader-jll-rtr.tls=true"
      - "traefik.http.routers.vaultreader-jll-rtr.tls.certresolver=dns-cloudflare"
      - "traefik.http.routers.vaultreader-jll-rtr.middlewares=chain-authelia@file"
      - "traefik.http.routers.vaultreader-jll-rtr.service=vaultreader-jll-svc"
      - "traefik.http.routers.vaultreader-jll-rtr.priority=10"
      # Public read-only share links — token is the credential
      - "traefik.http.routers.vaultreader-jll-share-rtr.rule=Host(`team.${DOMAINNAME}`) && PathPrefix(`/share/`)"
      - "traefik.http.routers.vaultreader-jll-share-rtr.entrypoints=websecure"
      - "traefik.http.routers.vaultreader-jll-share-rtr.tls=true"
      - "traefik.http.routers.vaultreader-jll-share-rtr.tls.certresolver=dns-cloudflare"
      - "traefik.http.routers.vaultreader-jll-share-rtr.service=vaultreader-jll-svc"
      - "traefik.http.routers.vaultreader-jll-share-rtr.priority=20"
      - "traefik.http.services.vaultreader-jll-svc.loadbalancer.server.port=8080"
```
Add `- compose/vaultreader-jll.yml` to `include:` in `stacks/office/compose.yml`, right after `compose/vaultreader.yml`.

- [ ] **Step 4: Authelia — user, groups, rules**

Generate a password and hash (password goes to the user to hand to the colleague out-of-band; never written to disk):
```bash
PW=$(openssl rand -base64 18); echo "The colleague's initial password: $PW"
docker exec authelia authelia crypto hash generate argon2 --password "$PW" | sed -n 's/^Digest: //p'
```
In `users_database.yml` add (same field layout as `jarbas`/`joao`; name/email from the Task 10 input):
```yaml
    <colleague>:
        password: <digest from the command above>
        displayname: <colleague full name>
        email: <colleague email>
        groups:
            - jll-team
            - external
        disabled: false
```
Add `- jll-team` to `joao`'s groups.

In `configuration.yml`, `access_control.rules`, insert as the **first three** rules (before `authelia.joao.date` bypass):
```yaml
    # Team VaultReader (work/jll) — see vaultreader spec 2026-10-09
    - domain: "team.joao.date"
      resources:
        - '^/share/.*$'
      policy: bypass
    - domain: "team.joao.date"
      subject: "group:jll-team"
      policy: two_factor
    # Wall: external collaborators get nothing else on joao.date
    - domain:
        - "*.joao.date"
        - "joao.date"
      subject: "group:external"
      policy: deny
```
Apply: `bash -lic 'dc-core restart authelia'`; then `docker logs authelia --since 1m 2>&1 | tail -5` → no errors, "Startup complete".

- [ ] **Step 5: Deploy both instances together**

```bash
cd ~/docker/stacks/office/images/vaultreader && ./deploy.sh
docker inspect -f '{{.Name}} {{.Image}}' vaultreader vaultreader-jll
```
Expected: both containers report the **same** image ID. If not, stop: the parity guarantee is broken.

- [ ] **Step 6: Smoke tests (host → team instance bridge IP)**

```bash
JIP=$(docker inspect vaultreader-jll --format '{{range $k,$v := .NetworkSettings.Networks}}{{$v.IPAddress}}{{end}}'); echo $JIP
U='-H Remote-User:<colleague>'
c() { curl -s -o /dev/null -w "%{http_code} $*\n" "$@"; }
c http://$JIP:8080/api/vaults                         # 401
c $U http://$JIP:8080/api/vaults                      # 200 — body must be ["jll"]
curl -s $U http://$JIP:8080/api/vaults; echo
c $U http://$JIP:8080/api/instance                    # 200
c $U -X DELETE "http://$JIP:8080/api/trash/empty?vault=jll"   # 404
c $U -X PROPFIND http://$JIP:8080/webdav/             # 404
c $U http://$JIP:8080/api/sync-status                 # 404
c $U -X POST http://$JIP:8080/api/shares/create -d '{"vault":"jll","path":"CLAUDE.md","writable":true}'   # 403
c $U http://$JIP:8080/health                          # 200
c $U "http://$JIP:8080/api/note?vault=pessoal&path=inbox/tarefas.md"  # 400 (vault does not exist here)
c $U -X POST "http://$JIP:8080/api/trash/restore?vault=jll&path=../../appdata/config.json"  # 400
```
Happy path (test note, cleaned up after):
```bash
c $U -X POST "http://$JIP:8080/api/note?vault=jll&path=__vr_smoke.md" -d '{"raw":"# smoke"}'     # 201
c $U -X PUT  "http://$JIP:8080/api/note?vault=jll&path=__vr_smoke.md" --data-binary '# smoke 2' # 200
curl -s $U -X POST http://$JIP:8080/api/shares/create -d '{"vault":"jll","path":"__vr_smoke.md"}'; echo   # token, created_by <colleague>
c $U "http://$JIP:8080/api/note/export?vault=jll&path=__vr_smoke.md&format=docx"   # 200 (GET, docx; uses jll/.vr templates)
tail -3 ~/docker/stacks/office/appdata/vaultreader-jll/audit.jsonl
```
Within 30 s the main instance must see the new note: `curl -s "http://$IP:8080/api/search?q=title:smoke&vault=work"` contains `jll/__vr_smoke.md` (IP = main instance bridge IP).

Network isolation — from an existing `t2_proxy` container that has Python (check with `docker ps --filter network=t2_proxy`; `lugares-api` is the usual candidate):
```bash
docker exec lugares-api python3 -c "import urllib.request;urllib.request.urlopen('http://$JIP:8080/health',timeout=3)" && echo REACHABLE || echo ISOLATED
```
Expected: `ISOLATED` (timeout / connection error). If `REACHABLE`, stop: section 1.3 of the spec is not holding.

Cleanup: `c $U -X DELETE "http://$JIP:8080/api/note?vault=jll&path=__vr_smoke.md"`, revoke the smoke share (`-X DELETE ".../api/shares/revoke?token=<token>"`), and `rm -f ~/vaults/work/jll/.trash/VRTRASH_*` entries whose decoded name is `__vr_smoke.md` (`ls ~/vaults/work/jll/.trash`).

- [ ] **Step 7: Through Traefik + Authelia (manual, user present)**

1. Incognito → `https://team.joao.date` → Authelia login as `<colleague>` → TOTP enrolment email (Gmail notifier) → logged in; sidebar shows only `jll`; footer shows `<colleague> · <sha>`; no share "Editable" option; no "Empty all" in trash.
2. Same session → `https://notes.joao.date` and `https://dash.joao.date` → both **denied**.
3. Logged out → `https://team.joao.date/share/<token>` of a read-only share → renders without login.
4. As `<colleague>`: edit a note; within 5 min `git -C ~/vaults/work log -1 --format='%an %s'` shows `<Colleague name>… vr(<colleague>): …`.

- [ ] **Step 8: Commit**

```bash
cd ~/docker
git add stacks/office/compose/vaultreader-jll.yml stacks/office/compose.yml stacks/core/appdata/authelia/configuration.yml
git status --short stacks/core/appdata/authelia/users_database.yml   # commit only if this file is already tracked; it holds hashes, not plaintext
git commit -m "office: team.joao.date VaultReader instance (work/jll) + Authelia jll-team/external rules

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Parity + committer freshness in f3nix-health

**Files:**
- Create: `~/.hermes/scripts/f3nix_context_gatherers/vaultreader.py`, `~/.hermes/scripts/tests/test_vaultreader_gatherer.py`
- Modify: `~/.hermes/scripts/f3nix-context-refresh.py`, the `f3nix-health` Hermes skill (`find ~/.hermes/skills -path '*f3nix-health*' -name SKILL.md`)

**Interfaces:**
- Produces: `gather() -> dict` with keys `in_sync: bool | None`, `images: dict[str,str]`, `committer_last_success: int`, `committer_stale: bool` (`> 3600` s or never).

- [ ] **Step 1: Write the failing test**

`~/.hermes/scripts/tests/test_vaultreader_gatherer.py`:
```python
import json
import os
import tempfile
import time
import unittest
from unittest import mock

from f3nix_context_gatherers import vaultreader as g


def fake_run(images):
    def run(cmd, **kw):
        name = cmd[-1]
        m = mock.Mock()
        m.returncode = 0 if name in images else 1
        m.stdout = images.get(name, "") + "\n"
        return m
    return run


class VaultreaderGatherer(unittest.TestCase):
    def state(self, last):
        d = tempfile.mkdtemp()
        p = os.path.join(d, "state.json")
        json.dump({"offsets": {}, "last_success": last}, open(p, "w"))
        return p

    def test_in_sync_and_fresh(self):
        with mock.patch("subprocess.run", fake_run({"vaultreader": "sha256:a", "vaultreader-jll": "sha256:a"})):
            out = g.gather(state_path=self.state(int(time.time())))
        self.assertTrue(out["in_sync"])
        self.assertFalse(out["committer_stale"])

    def test_drift_and_stale(self):
        with mock.patch("subprocess.run", fake_run({"vaultreader": "sha256:a", "vaultreader-jll": "sha256:b"})):
            out = g.gather(state_path=self.state(int(time.time()) - 7200))
        self.assertFalse(out["in_sync"])
        self.assertTrue(out["committer_stale"])

    def test_team_instance_absent(self):
        with mock.patch("subprocess.run", fake_run({"vaultreader": "sha256:a"})):
            out = g.gather(state_path="/nonexistent")
        self.assertIsNone(out["in_sync"])
        self.assertTrue(out["committer_stale"])


if __name__ == "__main__":
    unittest.main()
```
Run: `cd ~/.hermes/scripts && python3 -m unittest -v tests.test_vaultreader_gatherer 2>&1 | tail -3` → FAIL (module missing).

- [ ] **Step 2: Implement**

`~/.hermes/scripts/f3nix_context_gatherers/vaultreader.py`:
```python
"""VaultReader instance parity + per-author committer freshness.

Both vaultreader containers must run the same image (deploy with
images/vaultreader/deploy.sh). in_sync=False means one was not recreated.
committer_stale=True means vault-commit-authors has not succeeded for >1h.
"""
import json
import os
import subprocess
import time

CONTAINERS = ("vaultreader", "vaultreader-jll")
STATE = os.path.expanduser("~/.local/state/vault-commit-authors/state.json")


def _image(name):
    r = subprocess.run(["docker", "inspect", "-f", "{{.Image}}", name],
                       capture_output=True, text=True, timeout=10)
    return r.stdout.strip() if r.returncode == 0 else ""


def gather(state_path=STATE):
    images = {n: _image(n) for n in CONTAINERS}
    present = [v for v in images.values() if v]
    in_sync = None if len(present) < len(CONTAINERS) else len(set(present)) == 1
    try:
        with open(state_path) as f:
            last = int(json.load(f).get("last_success", 0))
    except (OSError, ValueError):
        last = 0
    return {
        "in_sync": in_sync,
        "images": images,
        "committer_last_success": last,
        "committer_stale": time.time() - last > 3600,
    }


if __name__ == "__main__":
    print(json.dumps(gather(), indent=2))
```
`f3nix-context-refresh.py`: add `vaultreader` to the `from f3nix_context_gatherers import (…)` list and `"vaultreader": vaultreader.gather(),` to `data`.

f3nix-health `SKILL.md`: add under its checks list:
```markdown
- **vaultreader**: if `vaultreader.in_sync` is `false`, the two VaultReader containers run different images — fix by running `~/docker/stacks/office/images/vaultreader/deploy.sh` (never `dc-office-up` for one service). If `committer_stale` is `true`, check `journalctl --user -u vault-commit-authors -n 20`; do not edit the work vault's git history.
```

- [ ] **Step 3: Run tests + live check**

```bash
cd ~/.hermes/scripts && python3 -m unittest -v tests.test_vaultreader_gatherer 2>&1 | tail -3   # OK
python3 f3nix_context_gatherers/vaultreader.py   # in_sync: true, committer_stale: false
python3 f3nix-context-refresh.py && python3 -c "import json;print(json.load(open('$HOME/.hermes/state/f3nix-setup.json'))['vaultreader'])"
```

- [ ] **Step 4: Commit** (only if `~/.hermes/scripts` is a git repo: `git -C ~/.hermes/scripts rev-parse 2>/dev/null`; otherwise record the change in the CHANGELOG entry of Task 13)

```bash
cd ~/.hermes/scripts && git add f3nix_context_gatherers/vaultreader.py tests/test_vaultreader_gatherer.py f3nix-context-refresh.py && git commit -m "f3nix: vaultreader parity + committer freshness gatherer

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: Documentation, skills, release

**Files:**
- Modify (vaultreader repo): `docs/security.md`, `docs/configuration.md`, `docs/api.md`, `docs/authelia.md`, `CLAUDE.md`, `ROADMAP.md`, `CHANGELOG.md`
- Modify (Hermes skills): `vaultreader`, `vaultreader-dev`, `vaultreader-share-links`, `hugo-vaultreader-write-bridge` SKILL.md files under `~/.hermes/skills/software-development/`
- Modify (docker repo): `CLAUDE.md` (office stack line), submodule pointer

- [ ] **Step 1: VaultReader docs**

- `docs/security.md`: replace "It is **not** designed for: Multi-tenant deployments…" with the new model — *single operator, optionally one small trusted team per instance; isolation by mount, dedicated network and `require_user`; `Remote-User` is attribution-only on instances without `require_user`; data private, system not (no security by obscurity)*. Add the trash-restore fix under "Data plane". Replace "No audit log" in "What's deliberately weak" with a description of `audit.jsonl`. Note that `/api/shares*` are unauthenticated inside the app and rely on the proxy (open follow-up).
- `docs/configuration.md`: table of `require_user`, `share_base_url`, `default_note`, `features.*` with defaults (copy from spec §2.2); "unknown feature key → startup fails".
- `docs/api.md`: `GET /api/instance`, `GET /api/note?…&meta=1`, `/health` now returns `version`, `ShareEntry.created_by`, 404 for disabled features.
- `docs/authelia.md`: section "Team instance" with the three rules from Task 11 Step 4 and why the `external` wall exists.
- `CLAUDE.md`: deploy line → `./deploy.sh` (never `dc-office-up … vaultreader` alone); remove "No tests yet" (list what exists); "Distroless/scratch" → `dotnet/runtime-deps` runtime with officecli, runs as uid 1000; route table lives in `routes.go`; new files table rows (`config.go`, `identity.go`, `instance.go`, `audit.go`, `rescan.go`); "Index rebuild on startup" → "plus 30 s stat rescan".
- `ROADMAP.md`: move "Multi-user permissions" note to say a team-instance model shipped (separate containers, not ACLs); remove "index rebuild on startup only" from needs-change.
- `CHANGELOG.md`: dated entry `2026-10-XX — Team instance (team.joao.date)` summarising Tasks 2–12, including the `.trash` search leak fix and the trash-restore traversal fix.

- [ ] **Step 2: Hermes skills**

In each listed SKILL.md: deploy = `~/docker/stacks/office/images/vaultreader/deploy.sh`; `PUT /api/note` returns 200 + `{"mtime"}` (not 204); container IP must be looked up (`docker inspect`), never hardcoded `172.10.0.18`; the team instance is not on `t2_proxy` and requires `Remote-User` — agents must not call it. Remove the `docker run --rm --network t2_proxy python…` recipe from `vaultreader-share-links` (violates the no-raw-docker-run rule) and point to curl against the bridge IP from the host.

- [ ] **Step 3: Final verification**

```bash
cd ~/docker/stacks/office/images/vaultreader
~/.local/go/bin/go vet ./... && ~/.local/go/bin/go test ./...
node -e "const fs=require('fs');const h=fs.readFileSync('static/index.html','utf8');fs.writeFileSync('/tmp/claude-1000/vr-check.js',h.substring(h.lastIndexOf('<script>')+8,h.lastIndexOf('</script>')))" && node --check /tmp/claude-1000/vr-check.js
grep -rn "joao.date/notas" static/ || echo "no hardcoded share prefix"
```

- [ ] **Step 4: Commit, push, update submodule pointer**

```bash
git add docs CLAUDE.md ROADMAP.md CHANGELOG.md
git commit -m "docs: team instance — security model, config, API, deploy

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```
Then use superpowers:finishing-a-development-branch to merge `feat/shared-team-instance` into `main`, push to `github.com/joaompfp/vaultreader` (ask before pushing), run `./deploy.sh` from `main`, and commit the submodule pointer + `CLAUDE.md` office line in `~/docker`.

- [ ] **Step 5: Hand-off to the user**

Give the user: the colleague's initial password (from Task 11 Step 4, if not already handed over), the URL `https://team.joao.date`, a 3-line "first login" note (password → TOTP email → app), and the follow-ups list from the spec (catch-all tightening, Syncthing key to SOPS, `/api/shares*` auth in the main instance, MCP connector).
