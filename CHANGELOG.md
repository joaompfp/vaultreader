# Changelog

All notable changes to VaultReader. Versioning is loose — there are no formal releases yet, just dated entries grouped by what landed in `main`.

Most-recent first.

## 2026-04-29 — Big feature wave

A two-day burst that took VaultReader from "polished reader with basic edits" to "Obsidian-flavored web client". Sequenced one feature at a time with browser smoke-tests after each commit.

### Added
- **Frontmatter chips.** Array values (`tags: [foo, bar]`) and known tag-like scalar keys (`tags`, `aliases`, `category`, `status`, `topic`, `project`, etc.) render as clickable rounded chips. Click a chip → opens search overlay pre-populated with that value.
- **Settings pane redesign with tabs**: General · Shared · Trash · Attachments · Admin · About. Each tab lazy-loads its data on activation. Live count badges on Shared and Trash tabs. The trash UI works for the first time — its `openTrash()` entry point was a dangling reference for ~5 days.
- **Outline / table-of-contents** right rail with scroll-spy. Toggleable per-browser. Smooth-scroll to clicked heading. Mobile becomes an overlay; auto-closes on click.
- **Note properties strip** — size, modified time (relative + absolute on hover), word count (strips frontmatter + code blocks), outgoing wikilink count, backlink count.
- **Pinned notes** via `pinned: true` frontmatter. Float to top of recents with 📌. Backed by a separate per-vault `vr-pinned-<vault>` localStorage key, so they survive recents-list churn.
- **Daily-note shortcut.** `Ctrl/⌘+D` opens or creates `daily/YYYY-MM-DD.md` with a minimal template. Idempotent.
- **KaTeX math.** `$$…$$` block, `\(…\)` inline. Bare `$…$` deliberately not consumed (currency conflicts: `$5 and $10` would false-match). KaTeX 0.16.11 bundled (~1.5MB total: JS + CSS + 60 fonts).
- **Conflict-aware writes.** PUT `/api/note?ifMTime=<unix>` returns 409 with the disk version's content if the file changed since the client's last GET. Resolution modal: Cancel · Take theirs · Keep mine (overwrite).
- **Attachment manager tab.** Walks every image in a vault and counts references via path-suffix scan over all `.md` files. Filter All / Orphans only. Per-image and bulk "Delete all orphans" (move to trash).
- **Graph view via Cytoscape 3.30.** Three scopes:
  - All vaults / single vault — whole-graph view.
  - Folder-scoped — pass `?folder=<vault-relative-path>`.
  - Ego-graph — pass `?center=<vault:path>&depth=N` (1-5 hops via outbound + inbound).
  Toolbar icon is context-aware: opens ego-graph rooted at the current note if any, folder-scoped otherwise. Header has a clickable scope breadcrumb and depth ± controls. Click a node = open the note; shift-click = re-center the graph; Cmd/Ctrl-click = open in a new tab. Center node renders larger and bolder.
- **Tag pane.** Toolbar tag-icon → 600px overlay with filter input + tag cloud. Aggregates frontmatter `tags`/`tag` across all vaults (463 unique in pessoal). Click any tag → search.
- **Saved searches.** `★ Save` button in search overlay → modal asks for a name. Up to 30 saved (LIFO; deduped). Empty-state of search shows the saved list with click-to-run.
- **WebDAV-out (read-only).** Mounted at `/webdav/`. Method allowlist: GET / HEAD / OPTIONS / PROPFIND. All mutating verbs return 405. Backed by `golang.org/x/net/webdav`.

### Changed
- **Mermaid v10.9 → v11.14.0.** Required for `block` diagram support (used in the new editor toolbar's mermaid-starter dropdown). Backwards-compatible with the existing flowchart / sequence / gantt / pie diagrams.
- **Modal overlay z-index 500 → 1100.** Was rendering behind the settings pane (z-index 800), making confirms invisible until you closed settings.
- **Toolbar graph icon now context-aware** (was always "all vaults"). Opens the smallest meaningful scope based on what's currently visible.
- **`PUT /api/note`** now returns `200 OK` with `{"mtime": <new>}` instead of `204 No Content`. Old clients ignoring the body still work.
- **Settings tabs** unified: previously separate `#admin-panel` and `#trash-overlay` are gone; both are now tabs. The toolbar gear icon opens settings on the Admin tab.
- **Editor toolbar** added 14 buttons (B, I, ~~, H, list, numbered, task, quote, inline code, code block, table, link, wikilink, mermaid-dropdown). Hidden under 700px.
- **Wikilink autocomplete** in the editor: type `[[` → popup with up to 8 results from `/api/search`; arrow keys + Enter to insert. Hand-rolled popup since the bundled CodeMirror lacks `@codemirror/autocomplete`.
- **Paste/drop image upload** to `<note-dir>/attachments/<note-base>-<unix>.<ext>` via new `POST /api/upload`. Reuses `isWritable` + `safePath` from note PUT.
- **CodeMirror toolbar shortcuts**: `Ctrl/⌘+B / I / E / K / L` for bold / italic / inline code / link / wikilink (when focus is in the editor).

### Fixed
- **Trash UI was completely non-functional** since 2026-04-24. The settings pane called `openTrash()` which was never defined; trash methods (`restoreTrashItem`, `permanentlyDeleteTrashItem`, `emptyTrash`) were called from the HTML but missing from the Alpine state. Reimplemented and wired up under the new Trash tab. Fixed `/api/trash/empty` endpoint mismatch (frontend was calling `DELETE /api/trash` which returns 405; now correctly hits `/api/trash/empty`).
- **Rate limiter shared one bucket** behind Traefik because every request had the same source IP (the Docker bridge). Now reads `X-Real-IP` / `X-Forwarded-For` (left-most hop) for real per-user buckets. Bumped baseline 120 → 240/min now that buckets are per-user.
- **Outline class binding** rendered `[object Object]` literally because Alpine's `:class="[a, {b: c}]"` array+object form silently degraded in this Alpine version. Replaced with string concat.
- **Frontmatter chip click race** — clicking a chip set `searchQuery` then opened the overlay, but the `$watch('searchOpen', …)` reset `searchQuery = ''` on open. Reordered: open first, set query in `$nextTick`.
- **Attachment refcount** caught only basename + vault-relative paths, missing Obsidian's note-relative `![[subdir/foo.png]]` form. Yielded 72/73 false orphans on real data. Now matches every path suffix; correctly down to 1/73.
- **Math currency false-match.** `$E=mc^2$ … costs $5 and pays $10` previously rendered `5 and pays $10` as math. Disabled bare `$…$` delimiter.
- **`revokeAllShares` modal** showed a name input ("Name cannot be empty") on a pure-confirm danger modal because `confirmOnly: false`. Set to `true`.
- **"No RW paths — all vaults are read-only"** copy was misleading (vaults are typically writable in deployment). Replaced with a hint of what to add.
- **Sidebar resize** — the right edge is now a draggable handle (180–600px range). Width persists per-browser; double-click resets. Hidden on mobile.
- **Multiple stale `council/*` worktrees** from a 2026-04-01 multi-agent design council session removed. `.worktrees/` was already in `.gitignore`.

### Docs
- `README.md` rewritten — full feature list, doc index, repository structure.
- `docs/features.md` — every feature in detail.
- `docs/configuration.md` — flags, env vars, admin token, RW paths, reverse-proxy, custom share URL prefix.
- `docs/architecture.md` — backend/frontend tour, persistence model, perf notes, "where to look for X".
- `docs/api.md` — every endpoint with request/response shapes.
- `docs/syntax.md` — wikilinks, embeds, frontmatter, mermaid, math, what's NOT supported (callouts, footnotes, inline `#tag`).
- `docs/security.md` — threat model, auth surfaces, what's deliberately weak.
- `docs/authelia.md` — Traefik + Authelia full example with `/share/` path exemption.
- `docs/theming.md` — CSS variables, vault icons, dark mode, custom CodeMirror themes.
- `docs/specs/2026-04-28-codemirror-editor-features.md` — design doc for the editor toolbar/autocomplete/upload work.
- This `CHANGELOG.md`.

## 2026-04-27

- **Focus trap** for all modals + overlays (a11y).
- **`isWritable` correctness fix** — earlier "robustness" pass had introduced double-join and broken `EvalSymlinks` paths; reverted to clean form.

## 2026-04-24

- **Security pass:** admin token bypass closed, `subtle.ConstantTimeCompare` for token comparison, body-size limits on admin POST.
- **Trash UI** — *added but never wired up*; this was the bug that the 2026-04-29 settings refactor finally fixed.
- **Per-vault sort, shortcuts modal, recents** improvements.
- **`/health` endpoint, rate limiter, atomic config write.**
- **Error feedback on delete** — 403 writable errors used to silently fail; now surface a modal.

## 2026-04-20

- **Mermaid v10** + URL hash navigation persistence.
- **Wider note text area** (860px), LR org chart redesign.
- **Sidebar collapsed state** survives page reload.
- **Image embeds** rendered as inline images.
- **Clean URLs** + working wikilinks with context-aware resolution.

## 2026-04-19

- **Mermaid v9** initial integration.

## 2026-04-06

- **Admin panel** (RW paths + restart container).
- **Move to…** in context menu + folder move support.
- **Visual tree picker modal** for moves.
- **Shareable notes** — read-only public links with expiration, share modal, active-shares in admin panel, `/share/TOKEN` page, share URL prefix `notas/`.

## 2026-04-02

- Initial public-ish state. Multiple vaults, basic CRUD, search, backlinks, sync indicator, dark mode, mobile-friendly layout.

---

Earlier history is in `git log` but not summarized here.
