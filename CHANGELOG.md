# Changelog

All notable changes to VaultReader. Versioning is loose — there are no formal releases yet, just dated entries grouped by what landed in `main`.

Most-recent first.

## 2026-04-29 — Sidebar lists non-md files; inline image/PDF/text viewer

- **`buildTree` no longer filters by `.md`** — every non-skipped file appears in the sidebar with an extension chip on the right (e.g. "PNG", "PDF", "PY"). Note rows look as before (basename without `.md`); non-note rows show the full filename + chip.
- **Inline file viewer** for non-`.md` items. Click an image/PDF/text file in the sidebar (or hit a deep URL like `/n/<vault>/path/to/img.png`) and the main pane swaps from markdown preview to a viewer panel with: file path + size header, an "Open" button to pop out, and the appropriate inline element. Supported: images (`png/jpg/gif/webp/svg/bmp/avif`), PDFs (browser-native via `<iframe>`), plaintext / source code (`txt/json/yaml/csv/log/ini/conf/toml/sh/py/js/ts/go/rs/css/html/xml/sql/env`), audio (`mp3/wav/ogg/flac/m4a/aac/opus`), video (`mp4/webm/mkv/mov/avi`).
- **Unknown extensions** open in a new tab (`/api/file?…`) so the browser decides what to do — keeps the viewer palette small without cutting off less-common formats.
- **`TreeNode` gains an `Ext` field** — empty for `.md` so existing frontend code paths keying on "no ext means note" stay correct without churn.
- **Routing** (`_routePath`) now dispatches by extension: `.md` → `openNote`, supported types → `openViewer`, fallthrough → `openNote` (which 404s gracefully). Reload, back/forward, deep links, and bookmarks all work for non-md files too.

## 2026-04-29 — Copy/paste flows for agent workflows

- **Copy button on every rendered code block** (top-right of each `<pre>`, fades in on hover). Click → copies the code text to clipboard with a brief tick. Idempotent: works after every render, plays nicely with the Mermaid replacement pass (skipped for mermaid blocks since they're swapped to SVG).
- **"Copy note body" toolbar button** — copies the active note's markdown without the YAML frontmatter to the clipboard. Different from the existing "Copy wikilink" button (which copies just `[[name]]`). For pasting note content into agent conversations cleanly.
- **"Paste-append to note" toolbar button** — reads the OS clipboard and appends it to the end of the active note (with one blank-line separator), autosaves, and shows a 6.5-second undo toast. Click Undo → reverts the file to its pre-paste state via a regular conflict-aware PUT. Disabled (greyed out) when the path isn't writable.
- **New public endpoint `GET /api/writable-paths`** — returns just the `rw_paths` array (no admin token needed). The full `/api/admin/config` is still gated. Lets the SPA gate write-related UI (paste-append, future flows) without requiring the admin token. Knowing which paths are writable isn't sensitive — the writes themselves are gated server-side.
- **Generic action-toast factory (`showActionToast`)** — undo-toast pattern was previously hardcoded for trash-restore. Now any flow can pass a custom undo callback (`_undoFn`) to `undoToast`; falls back to the trash-restore behaviour when absent. Used by the paste-append undo.

## 2026-04-29 — Copy-path button on the breadcrumb row

- **Small copy button on the breadcrumb row** (left of the frontmatter toggle, right edge of the row). Click → copies `vault/path/to/note.md` to the clipboard for pasting into agent conversations or scripts. When no note is open, copies the active vault + current folder (`vault/path/to/folder`). Tick + crimson confirmation for 2s. Doesn't touch the breadcrumb segment click handlers — those still navigate.

## 2026-04-29 — `examples/` scaffold for first-clone experience

- **`examples/vaults/demo/`** — a 9-note sample vault that ships in the repo. Includes a Welcome page, a syntax showcase covering every renderer feature, a wikilinks-in-tables demo (regression cover for the alias-pipe fix), a daily note, four cross-linked notes for the graph view, a meeting template, two SVG image assets, and a vault icon. Notes are intentionally Obsidian-compatible — drop them into Obsidian and they'll render the same.
- **`examples/appdata/`** — seed `config.json` (empty admin token + `rw_paths: ["demo"]`), empty `shares.json`, and `icons/demo.svg`. Lets a fresh clone boot to a working install with a single `docker run` (bind-mount `examples/vaults` and `examples/appdata`).
- **`examples/README.md`** — what's there, how to use it, how to pivot to your own vaults. Top-level README's Quick Start now points at the demo seed.
- **Compatibility fix:** `renderWikilinks` and `renderWikilinksPlain` now strip a trailing `\` from the wikilink name. Obsidian's manual `\|` escape inside table cells (used by some users to work around the alias-pipe collision) was leaving `name\` as the lookup key, breaking resolution. The escape is no longer necessary (the `protectWikilinkPipes` pre-pass handles it) but is now backwards-compatible.
- Synced docs: `docs/features.md` (toolbar share button + popover entry), `docs/syntax.md` (wikilinks-in-tables explainer), `docs/configuration.md` + `docs/api.md` (corrected `/api/vault-icon` query param: `?vault=` not `?name=`), README.md (Quick Start, share-button bullet, callouts bullet).

## 2026-04-29 — Share-from-toolbar + wikilink/table fix

- **Share button on the main toolbar** (next to copy-wikilink) with a per-note popover. When the active note has any active share, the button turns crimson and shows a small badge with the count; the popover lists each link with one-click Copy / Open / Revoke. When unshared, the popover offers three quick-share options (read-only · no expiry / read-only · 7 days / editable · no expiry) plus a "More options…" link to the full create-share modal. No more digging through Settings → Shared to find out which open note is shared.
- **Fix: wikilink with alias inside a markdown table** (`[[name|alias]]` in a table cell) — previously the `|` was eaten by goldmark's table parser as a column boundary, splitting the link across two cells (rendering as raw `[[name`). Added a pre-pass (`protectWikilinkPipes`) that swaps the alias pipe for a sentinel before goldmark sees it, restored after rendering. Applies to both internal preview and shared notes.

## 2026-04-29 — Callouts + parity polish

- **Obsidian callouts (`> [!type] Title`) now render as styled blocks** in both the internal preview and shared notes. Eight type families with Obsidian-compatible color codes (info/tip/success/warning/failure/question/quote + generic), light + dark palettes, fold-marker (`-` suffix) accepted. Unknown types render as a generic `.callout`. Implemented as a regex post-pass over the goldmark blockquote output (`renderCallouts`) — no goldmark extension dependency.
- **Wikilinks now render in shared notes** as plain styled spans (`renderWikilinksPlain`) — visible as the alias text, no clickable navigation off the share. Previously wikilinks rendered as raw `[[name|alias]]`.
- Synced docs: `docs/syntax.md` now documents callouts (was previously listed under "not handled").

## 2026-04-29 — Shared-note image rendering

- **Fix: images broken in shared notes accessed via `/notas/<token>` proxy alias.** The rewriter was emitting absolute `/share/<token>/file?…` URLs; on the public `joao.date` host the `/share/*` path has no Traefik route, so every embed 404'd. Switched to path-relative URLs (`file?path=…` + `asset?name=…`) anchored to a `<base href="<token>/">` tag. Works under both `/share/<token>` and `/notas/<token>` without the server needing to know the proxy prefix.
- **Plain markdown images now resolved against the note directory.** Previously only Obsidian wikilink embeds (`![[img.png]]`) reached the share-file route — `![alt](sub/img.png)` syntax leaked the literal `sub/img.png` href, which the browser tried against `/notas/...` and 404'd. Now both forms route through `file?path=…`. Fixes embeds in notes that use vanilla markdown image syntax (e.g. converted-from-PDF reports).

## 2026-04-29/30 — Polish wave (Tiers 1–4)

A second wave of work driven by daily-use feedback. Twenty-plus features and fixes across a single user session, mostly small and ergonomic. Rolled out in four "tiers" (the planning rationale is captured in commit messages).

### Graph view — visual + interaction overhaul
- **Switched layout from `cose` (compute-then-place) to `cola` (live force simulation)** via `cytoscape-cola`. The graph now visibly settles from chaos over a few seconds; dragging a node ripples through the rest of the graph in real time. Bundle: +78KB cola + 22KB cytoscape-cola.
- **Live reflow on drag** — `grab`/`free` events restart cola for 1.5s, so the graph keeps wobbling for a couple of seconds after you let go. Inertia, basically.
- **Moebio-inspired visual polish**: smaller nodes (5–14px) with text-opacity hidden by default, curved bezier edges that are visible against dark backgrounds, hover-to-light-up-neighborhood with rest-fade-to-0.12, center node rendered as an outlined accent ring instead of a solid red blob.
- **Three scope modes** with a clickable scope-breadcrumb: all-vaults, single-vault, **folder-scoped** (`?folder=…`), **ego graph** (`?center=vault:path&depth=N`, 1–5 hops via outbound + inbound). Toolbar graph icon picks the smallest meaningful scope based on what's open.
- **Per-graph depth ± controls** when in ego mode. Shift-click any node to re-center the graph there; Cmd/Ctrl-click opens the note in a new tab.
- **Zoom-aware label visibility** — labels hidden until rendered font ≥ ~14px so default-zoom views are geometry-only, hover/lit nodes always show labels.
- **Smaller fonts (8/11) + longer simulation** (7–9s initial, 3.5s post-drag) for a weighty feel.

### Editor UX (Tier 1)
- **Search ranking** — title-match (×20) > filename-match (×5) > body-occurrence (×1, capped 5), plus 0–3 recency boost over 30 days. Top 20 by score per vault. Old behavior kept the first 20 in filesystem order; recently touched notes used to sink under stale archives.
- **Alt+← / Alt+→** for back/forward through visited notes (wraps `window.history.back/forward`). Skipped in text inputs / editor.
- **Paste/drop image in preview mode** — uploads + appends `![[…]]` at end of note + saves with conflict detection + re-renders. No edit-mode switch needed. Honors `isWritable`.
- **Rename warning** — new `/api/backlinks` endpoint; `promptRenameNote` checks before showing the rename input. If any notes link to it, surface a danger modal listing up to 5 affected titles + "Rename anyway".

### Editor UX (Tier 2)
- **Right-click on `[[wikilink]]`** in preview → context menu: Open · Open in new tab · Reveal in sidebar · Copy wikilink · Copy URL. Missing-link spans get "Create" + "Copy as text" instead.
- **Reveal in sidebar** via `Ctrl+Shift+L` or context menu. Switches to the note's vault, navigates `cwd` to its parent folder, smooth-scrolls the row into view.
- **Daily note opens in edit mode** with cursor at EOF when freshly created (existing dailies open in preview as before).
- **Whitespace normalization on save** — backend `saveNote` strips trailing space/tab per line and ensures exactly one trailing newline. Reduces git-diff noise when the same vault is edited from multiple tools.
- **Drag-and-drop sidebar items** to move into folders. File rows + folder rows are draggable; folder rows + the `..↑` row are drop targets with a dashed-accent outline on hover.

### Editor UX (Tier 3)
- **Undo toast for delete** — bottom-right toast for 6.5s after soft-delete. Click Undo → restores from `.trash/`. Bulk-aware (one combined toast for the whole batch).
- **Search across attachment names** — `/api/search` includes image filenames as `kind: "image"` results, scored lower than note matches. UI shows 🖼 prefix; click opens in a new tab.
- **Bulk select + bulk ops** — Cmd/Ctrl-click toggles, Shift-click range-selects, Esc clears. Bulk Move (reuses tree picker) + Bulk Delete (one combined toast). Selection clears on folder change.
- **Note templates** — `<vault>/templates/*.md` enumerated via new `/api/templates`. "+ New" → "From template…" picker. Placeholders `{{date}}`, `{{date:FMT}}` (custom YYYY/MM/DD/HH/mm/ss), `{{time}}`, `{{title}}` expand on creation. Drops user into edit mode at end of file.

### Editor UX (Tier 4)
- **Search operators**: `tag:foo` / `path:foo` / `title:foo` / `modified:>7d` / `modified:<2026-01-01`. AND together. Plain text after operators acts as the body substring filter as before. Operator-only queries (e.g. `tag:work modified:>7d`) return everything matching, sorted by recency.
- **Mermaid + KaTeX in shared notes** — both were previously not rendered at all (handleShareView didn't load the libs, AND Authelia would have blocked `/static/` even if it had). Fixed via new `/share/<token>/asset?name=…` route under the existing share-bypass; conditional script-tag injection (only if the page actually contains `language-mermaid` or math delimiters); KaTeX CSS font URLs rewritten to absolute share-asset URLs. Strict allowlist + extension check prevents leaked tokens from fetching arbitrary static files.
- **Skipped**: auto-link suggestions while typing (needs a clearer spec); WebDAV write-mode (Syncthing/Obsidian Sync handle this better); folder reorder (alphabetical only).

### Layout / look
- **Two-row toolbar** — buttons on row 1, full breadcrumb on row 2. Long paths now legible (segment max-width 240px, note-title max-width 480px).
- **Frontmatter toggle moved to right edge of breadcrumb row** with the count badge. Stats strip moved into the collapsible content. The dedicated stats row is gone.
- **Sidebar breadcrumb wraps over multiple lines** with a smaller, denser font (11px) and the last segment bolded — current location reads at a glance even on deep paths.
- **Properties strip back-references** — for non-orphan attachments, the Attachments tab shows up to 8 referencing notes as clickable chips (with H1-extracted titles).
- **Subtle CSS animations** — pop-in scale on modal/overlay/settings boxes (140ms), max-height transition on frontmatter expand (220ms), gentle 250ms fade on save-status indicator, CSS spinner replacing "Loading…" text in async tabs (attachments, graph, tags).

### Settings
- **Drop "Read-write paths" admin UI**, replace with a Vaults overview list. Each row shows icon, name, note count, and a "writable" tag if any rw_paths cover it. The hint at the bottom points at `appdata/config.json` for power users. `isWritable` and config gating remain unchanged at the backend.
- **Attachments tab gains a folder/name filter** — substring match, client-side.
- **Properties strip is now collapsible** — hidden by default behind the fm-toggle icon. Frees up vertical reading space.
- **Compact props row** — toggle is an icon with a count badge instead of "frontmatter (8 fields)" eating a whole row.

### Bugfixes
- **Trash naming overhaul (`VRTRASH_<base64>_<unix>` scheme)** — legacy `__→/` round-trip corrupted any file whose original basename contained `__` or started with `_`. New scheme encodes the full vault-relative path in base64-url, eliminating the entire class of round-trip bugs. Legacy entries keep working via `legacyDecodeTrashName` fallback.
- **Modal z-index 500 → 1100**, was rendering behind settings (`800`), making the "Revoke all share links?" confirm invisible.
- **`revokeAllShares` had `confirmOnly: false`** so the input box was visible on a pure-confirm danger modal — tab-to-confirm enforced "Name cannot be empty". Set to `confirmOnly: true`.
- **Bulk revoke** was using `POST` against a `DELETE`-only handler; replaced with batch endpoint `DELETE /api/shares/revoke-all` (no per-token rate-limit hits either).
- **Misleading "all vaults are read-only"** copy when `rw_paths` was empty — replaced with a hint that suggests what to add.
- **Goldmark HTML-escapes `&` to `&amp;`** in attribute values, breaking the regex that rewrote `/api/file?…` URLs to `/share/…/file?…` for share images. Now decodes the entity before parsing.
- **Tag-operator strict matching** — `tag:london` initially didn't catch `london-2026`; relaxed to substring match (still respects exact + hierarchical descendant rules first).
- **Outline class binding** rendered `[object Object]` because Alpine's `:class="[a, {b: c}]"` array+object form silently degrades in this Alpine version. Switched to string concat.
- **Frontmatter chip click race** — clicking a chip set `searchQuery` then opened the overlay, but the `$watch('searchOpen', …)` reset `searchQuery = ''` on open. Reordered: open first, set query in `$nextTick`.
- **Wikilink popup label visibility** in the cola layout regressed because `min-zoomed-font-size` was unreliable post-cola; switched to a JS zoom-event handler.

### Documentation
This entry. Plus refresh of README, ROADMAP, and the existing `docs/`.

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
