# VaultReader modularization

**Status:** design
**Date:** 2026-04-29
**Author:** Joao + Claude
**Driver:** Future feature velocity for the maintainer; readability and PR-scope as side benefits.

## Goal

Break VaultReader's two monolithic files into focused modules so that adding or changing a feature touches a single small file rather than scrolling through thousands of lines of unrelated code. The change is purely organisational — runtime behaviour, deployment artifacts, and external API stay byte-identical.

**Primary success criterion:** when a contributor (including the maintainer six months from now) wants to change "the share popover" or "the search ranking," `grep` lands them in a single ≤500-line file containing every line that needs changing.

**Non-goals:**

- New features. This refactor adds none.
- Performance changes. Same memory profile, same request/response timings.
- API or schema changes. Every existing route, JSON shape, frontmatter convention, and storage format is preserved.
- Build-step adoption. No bundler, no transpiler, no codegen step.

## Constraints

- **No JS bundler.** Native ES modules only (`<script type="module">` + `import`). No esbuild / rollup / webpack / parcel / TypeScript / JSX. The Alpine + CodeMirror + Mermaid + KaTeX + Cytoscape libraries stay as bundled `<script src>` exactly as today.
- **`embed.FS` is the deployment mechanism.** Every static asset must ship inside the Go binary. Splitting JS into more files is fine; each file gets embedded.
- **Single Go binary** — `package main` is mandatory.
- **No CSS preprocessor.** `static/style.css` stays a single hand-written file.
- **No HTML templating** for `index.html`. The 1300-line markup stays a single file.

## Current state

- **`main.go`** — 3553 lines, 100 functions, one `package main` file. Already organised into ~14 logical sections via `// ─── Section ───` dividers. Each section is roughly self-contained: index, markdown rendering, search, shares, trash, attachments, graph, etc.
- **`static/index.html`** — 4699 lines: HTML markup (1356 lines) + a 70-line `__cmAPI` IIFE (CodeMirror wrapper) + a ~3270-line inline `<script>` containing the `vaultApp()` Alpine factory. The factory is already organised into ~50 thematic sections via `// ── Section ─` dividers (file commander, undo toasts, graph view, shares, autosave, paste-append, etc.).
- **`static/style.css`** — 2870 lines, organised by section comments.
- **Static asset bundle** — third-party libs (`alpine.min.js`, `mermaid.min.js`, `katex.min.js`, `cytoscape.min.js`, `cola.min.js`, `cytoscape-cola.js`, `codemirror.bundle.js`, `katex.min.css`, fonts) — already separate files, served directly by `embed.FS`.

## Approach

Two staged changes, shipped as separate PRs to keep blast radius small.

**Stage 1 — Backend split** (this stage's risk profile is essentially zero because Go's `package main` semantics treat multiple files in the same package as concatenated input to the compiler).

**Stage 2 — Frontend split** (riskier, done after Stage 1 is stable for a week or so).

The user is the only consumer; "production usage" is `notes.joao.date` and the open-source repo. There are no other deployments to coordinate with.

---

## Stage 1 — Backend split

### Target file layout

`main.go` becomes a thin entry-point; everything else moves to topic-named siblings in the same `package main`. No subdirectories — `package main` doesn't benefit from them.

```
main.go              — CLI flag parsing, server struct construction, mux setup, ListenAndServe (~80 lines)
server.go            — server struct definition, vaultPath, safePath, vault/admin helpers (~120 lines)
http.go              — gzipMiddleware, rateLimiter, jsonResponse, errResponse, gzipResponseWriter (~200 lines)
index.go             — NoteIndex struct + buildAll, updateNote, removeNote, resolve, getBacklinks (~200 lines)
markdown.go          — md var + init(), renderMarkdown, expandEmbeds, resolveEmbed, renderWikilinks, renderWikilinksPlain, renderCallouts, protectWikilinkPipes / restoreWikilinkPipes, htmlEscape, urlEscape, noteHref, resolveWikilinkTarget, parseFrontmatter, extractTitle, normalizeName, normalizeMarkdown (~600 lines)
search.go            — searchQuery, parseSearchQuery, extractTagsLower, parseModSpec, searchVault (~300 lines)
notes.go             — handleGetNote, handlePutNote, handleCreateNote, handleDeleteNote, handleMove, handleRenameFolder, handleCreateFolder, handleDeleteFolder, handleFolder, handleResolve, handleBacklinks, saveNote (~400 lines)
files.go             — handleFile, handleVaultIcon, handleUpload, sanitizeFilename, imageExtensions map, buildTree, TreeNode (~300 lines)
shares.go            — ShareEntry, ShareStore (newShareStore, load, save, create, get, revoke, revokeAll, list), handleShareCreate, handleShareList, handleShareRevoke, handleShareRevokeAll, handleShareView, handleShareAsset, handleShareFile, rewriteShareImageURLs, shareAssetAllowlist (~600 lines)
trash.go             — trashSentinel const, makeTrashName, decodeTrashName, legacyDecodeTrashName, handleTrashList, handleTrashRestore, handleTrashEmpty (~250 lines)
attachments.go       — AttachmentItem, AttachmentRef, handleAttachments, countWithRefs (~200 lines)
graph.go             — handleGraph (folder/vault/ego subgraph builders) (~200 lines)
tags.go              — handleTags (~80 lines)
admin.go             — AdminConfig, configPath, loadConfig, saveConfig, isWritable, requireAdminToken, handleAdminConfig, handleHealth, handleAdminRestart, handleWritablePaths (~250 lines)
sync.go              — SyncStatus, syncHTTPClient, handleSyncStatus, handleStats, VaultStat, StatsResponse (~150 lines)
templates.go         — handleTemplates (~80 lines)
webdav.go            — newWebDAVHandler (~30 lines)
vaults.go            — vaultOrder, handleVaults, handleTree, handleIndex (~120 lines)
```

**Total ~19 files** (counting the existing `main.go` as a much smaller file). All in repo root, all `package main`, all visible to `go build`.

### Boundary principles

Each file answers one question:
- *Where does the share-popover backend live?* → `shares.go`
- *Where is wikilink resolution?* → `markdown.go`
- *How is search ranking computed?* → `search.go`
- *What touches the trash directory?* → `trash.go`

The `server` struct stays in `server.go`. Methods on `server` (almost every handler) live in whichever file fits the domain — Go doesn't care that `(s *server) handleTrashList` is defined in `trash.go` while `(s *server) handleNote` is defined in `notes.go`.

Cross-file references work because `package main` is a single namespace — no imports, no exports, no visibility annotations needed. A function defined in `markdown.go` is callable from `shares.go` directly.

### What does NOT move

- The `var staticFiles embed.FS` declaration with its `//go:embed` directive — stays in `main.go` because it's the natural place for the deployment-bundling concern.
- Constants used by exactly one file (e.g. `wikilinkAliasPipeSentinel`) — stay with the only consumer.
- The `init()` for the goldmark renderer — stays with the markdown code.

### Risk and verification

**Risk:** approximately zero. Go's package-level scope means a multi-file `package main` is identical to the concatenated single-file version after compilation. Same binary, byte-for-byte (modulo Go build caching).

**Verification at the end of Stage 1:**

1. `go build` succeeds.
2. `docker build` succeeds.
3. Build the new image, hash the output binary, compare functionally (every endpoint smoke-tests the same): `/api/vaults`, `/api/tree`, `/api/note`, `/api/search`, `/api/shares`, `/api/graph`, share view, WebDAV, file serving, vault icons, admin endpoints with token, rate-limit, redirects.
4. The headless-browser smoke test from the project's CLAUDE.md runs against `notes.joao.date`'s deployed image. No console errors, every feature exercised.

**Stage 1 PR is one commit** that simultaneously:
- Creates the new `.go` files
- Removes the moved code from `main.go`
- Leaves no behaviour difference

This is a "move-in-place" — every line of code keeps its identity (same name, same function body); only the file it lives in changes. Reviewers can verify by checking the diff is purely "minus from `main.go`, plus elsewhere," and `go build` passing.

---

## Stage 2 — Frontend split

### Target file layout

The single inline `<script>` containing `vaultApp()` becomes a tree of ES modules under `static/js/`. The HTML shell loads a single entry module that assembles the rest.

```
static/
├── index.html                — HTML markup + 70-line __cmAPI IIFE only.
│                                Inline <script> for vaultApp() removed; replaced by:
│                                <script type="module" src="/js/app.js"></script>
└── js/
    ├── app.js                — entry point. Imports + assembles vaultApp factory; calls
    │                           window.vaultApp = function() { return Object.assign({}, ...mixins); }
    │                           (~80 lines)
    ├── core/
    │   ├── state.js          — initialState() returns the big defaults object
    │   │                       (vaults: [], activeVault: '', activePath: '', noteRaw: '',
    │   │                       searchOpen: false, ..., a few hundred fields) (~200 lines)
    │   ├── routing.js        — _routePath, _routeHash, _noteURL, popstate handler, hashchange
    │   │                       handler, _recordRecent, recent-files state (~250 lines)
    │   ├── modal.js          — modal helpers, focus trap, $watch wiring (~150 lines)
    │   └── util.js           — _wordCount, _stripFrontmatter, _countOutgoingLinks,
    │                           shareExpiry, formatBytes, etc. — pure functions (~150 lines)
    ├── features/
    │   ├── search.js         — searchOpen state, searchQuery, doSearch, highlightMatch,
    │   │                       saved searches, tag-pane integration (~300 lines)
    │   ├── graph.js          — Cytoscape + cola wrapper, openGraphSmart, scope/depth controls,
    │   │                       _graphLayoutOptions, _graphReflow (~450 lines)
    │   ├── editor.js         — toolbar (tbWrap, tbHeading, tbLinePrefix, etc.), [[ autocomplete,
    │   │                       paste-image upload, mode toggle, mermaid menu, __cmAPI bridge
    │   │                       (~450 lines)
    │   ├── shares.js         — share modal, popover, quickShare, copyShareLink, revokeShare,
    │   │                       loadActiveShares, noteShares, canWriteCurrent (~300 lines)
    │   ├── sidebar.js        — file commander (currentDirs, currentFiles, sortedFolderItems),
    │   │                       drag-drop, bulk select, context menu, sidebar resize (~400 lines)
    │   ├── settings.js       — settings pane, tabs (general/shared/trash/attachments/admin/about),
    │   │                       refresh logic, vault overview (~400 lines)
    │   ├── render.js         — decorateCodeBlocks, rerenderMermaid, rerenderMath,
    │   │                       handlePreviewClick, handlePreviewContextMenu, _copyCodeBlock
    │   │                       (~250 lines)
    │   ├── outline.js        — outlineItems, outlineOpen state, outline scroll-spy (~120 lines)
    │   ├── tags.js           — tagsList, tagsLoading, filteredTags, selectTag (~120 lines)
    │   ├── trash.js          — trashItems, restoreTrashItem, emptyTrash UI (~150 lines)
    │   ├── attachments.js    — attachments list, orphan filter, bulk-delete (~150 lines)
    │   ├── undo-toasts.js    — _toastSeq, showUndoToast, showActionToast, dismissToast,
    │   │                       undoToast, _bulk handling (~120 lines)
    │   ├── copy-paste.js     — copyNoteLink, copyNoteBody, copyNotePath, pasteAppendToNote,
    │   │                       copy state flags (~150 lines)
    │   ├── viewer.js         — _viewerKindFor, fileKind, fileIconSvg, openItem, openViewer,
    │   │                       closeViewer (~120 lines)
    │   ├── notes-io.js       — openNote, saveNow, _saveNote, isDirty, autosave, conflict modal,
    │   │                       _showConflictModal, isWritable, vaultIsWritable (~350 lines)
    │   ├── notes-ops.js      — promptCreateNote, promptRenameNote, deleteNote, deleteFolder,
    │   │                       promptCreateFolder, promptRenameFolder, move-picker (~400 lines)
    │   ├── daily.js          — openDailyNote (~80 lines)
    │   ├── templates.js      — openTemplatePicker, useTemplate, _expandTemplate (~120 lines)
    │   ├── stats.js          — refreshStats, refreshSyncStatus, statsBar state (~100 lines)
    │   ├── admin.js          — adminConfig state, fetchAdminConfig, saveAdminConfig,
    │   │                       loadWritablePaths, restartServer (~150 lines)
    │   └── breadcrumb.js     — activeBreadcrumbs, navigateToSegment, sbContext (~80 lines)
    └── (existing third-party libs untouched)
        ├── alpine.min.js
        ├── codemirror.bundle.js
        ├── mermaid.min.js
        ├── katex.min.js / .min.css
        ├── katex-auto-render.min.js
        ├── cytoscape.min.js
        ├── cola.min.js
        ├── cytoscape-cola.js
        └── fonts/
```

**Total ~24 module files** across `core/` and `features/`, plus `app.js`. Largest target ≤ 450 lines; most are 100–300.

### Mixin assembly pattern

Each feature file exports a single mixin object:

```js
// features/copy-paste.js
export const copyPasteMixin = {
  // State defaults
  copyLinkDone: false,
  copyPathDone: false,
  copyBodyDone: false,

  // Methods
  async copyNoteLink() {
    const name = this.activePath.split('/').pop().replace(/\.md$/, '')
    // ... uses this.* freely
  },
  async copyNoteBody() { /* ... */ },
  async copyNotePath() { /* ... */ },
  async pasteAppendToNote() { /* ... */ },
  _stripFrontmatter(raw) { /* ... */ },
}
```

`app.js` assembles them:

```js
// app.js
import { initialState } from './core/state.js'
import { routingMixin } from './core/routing.js'
import { modalMixin } from './core/modal.js'
import { searchMixin } from './features/search.js'
import { graphMixin } from './features/graph.js'
// ... etc
import { copyPasteMixin } from './features/copy-paste.js'

window.vaultApp = function () {
  return Object.assign(
    {},
    initialState(),
    routingMixin,
    modalMixin,
    searchMixin,
    graphMixin,
    // ...
    copyPasteMixin,
  )
}
```

`index.html` loads the assembled factory:

```html
<script type="module" src="/js/app.js"></script>
```

Because `Object.assign` happens before Alpine wraps the result in a Proxy, every `this.foo` reference inside any mixin method sees the full merged object. Reactivity is unchanged.

### Why mixins (and not factories or helpers-only)

- **Mixins** keep the existing code style intact. Methods that use `this.activePath`, `this.noteRaw`, `this.modal`, etc. don't need refactoring — the meaning of `this` inside an Alpine method stays "the merged component."
- **Factories** (each module returns a closure-bound object) would force every cross-mixin call to be rewritten and would lose Alpine's reactivity for closure-held state.
- **Helpers-only** (extract pure functions, leave the giant factory) doesn't actually shrink the factory and is the smallest velocity gain.

The existing code already calls itself with `this.` everywhere; mixins preserve that.

### Cross-mixin calls

Mixins routinely call methods on other mixins (e.g. `pasteAppendToNote` calls `_saveNote`, which lives in `notes-io.js`). This Just Works because, after `Object.assign`, all methods share the same `this`. No imports between mixin files are needed.

### State that's used by exactly one mixin

Stays in that mixin's exported defaults. State that's used by many mixins (e.g. `activePath`, `activeVault`, `cwd`, `mode`) lives in `core/state.js`'s `initialState()`.

### Per-mixin state-default conflicts

Two mixins must not declare the same state field. The build is `Object.assign({}, ...)` — later-spread wins silently. To prevent collisions:

- `core/state.js` owns the canonical defaults for state shared across mixins.
- Each feature mixin declares only state fields it owns alone.
- A linter step (`grep` or a Node script run in CI) verifies no field is declared in more than one mixin file. Stage 2 PR includes this check.

### `__cmAPI` bridge

The 70-line CodeMirror wrapper IIFE that sets `window.__cmAPI` is moved to `static/js/lib/codemirror-bridge.js` and loaded as a regular `<script>` tag (NOT a module — it sets a `window` global). This stays a non-module script because it bridges to the third-party CodeMirror bundle, which is itself non-module.

### Risk and verification

**Risk:** medium. Failure modes:
1. **Alpine reactivity break** — possible if a mixin holds state in a closure rather than as a public property. Mitigation: every `let` / `const` outside method bodies is checked during the move; module-level state (other than constants) is moved into the mixin object.
2. **`this` binding lost** — possible if mixin methods are accidentally re-declared as arrow functions with closure-captured `this`. Mitigation: all mixin methods stay as method-shorthand syntax (`foo() { ... }`).
3. **Module load order matters** — `app.js` must define `window.vaultApp` before Alpine evaluates the `x-data="vaultApp()"` binding. Native ES module scripts are implicitly deferred and execute after parsing in source-order. The fix:
   - `app.js` is loaded with `<script type="module" src="/js/app.js"></script>` placed BEFORE the Alpine `<script defer src="/alpine.min.js"></script>` in `index.html`.
   - `app.js` imports all mixins synchronously (top-level `import` is blocking within the module graph) and sets `window.vaultApp = function () { ... }` as the final synchronous statement of the module.
   - When Alpine initialises (on its own `DOMContentLoaded` listener), `window.vaultApp` is guaranteed to be defined because both `<script type="module">` and `<script defer>` execute before `DOMContentLoaded`, and module scripts in source-order complete before deferred scripts in source-order.
   - Verification: a deliberate test that `console.log(typeof window.vaultApp)` inside `app.js` returns `'function'` before Alpine's first paint.
4. **State-default collision** — caught by the linter step described above.

**Verification at the end of Stage 2:**

1. `node --check` passes for every `.js` module.
2. `grep` verification: no public state field declared in two mixins (CI-runnable script).
3. Headless-browser smoke test from CLAUDE.md exercises every feature path:
   - Open a note → preview renders with mermaid + math + callouts
   - Edit a note → save → mtime updates, no conflict
   - Search with operators → results ranked correctly
   - Open graph view → drag a node → graph reflows
   - Create a share → copy link → open in new tab → renders
   - Right-click sidebar item → context menu works
   - Bulk select + delete → undo toast appears → undo restores
   - Open a non-md file → viewer renders (image + PDF + text)
   - Toolbar copy buttons all work
4. **No console errors** at any point during the smoke test.
5. **Visual diff** of a screenshot at default state (taken before the refactor and after) — pixels match.

### Stage 2 PR is one commit per migrated mixin, OR one big commit

**Decision:** one big commit. Per-mixin commits would force interim states where some methods are in `app.js` and some in modules, complicating cross-mixin calls. The whole frontend moves together.

The diff will be large. To make it reviewable:
- The PR description lists exactly which lines moved from `index.html` to which module.
- A `tools/check-mixin-collisions.js` script ships with the PR.
- The `index.html` post-refactor diff is small (just removing the inline `<script>` and adding the `<script type="module" src="/js/app.js">` line) — easy to verify.

---

## Documentation updates

Each stage updates docs that reference file structure:

**Stage 1 docs:**
- `CLAUDE.md` "Where things live" map — replaces "main.go ~L2580" with file names.
- `docs/architecture.md` — backend section gets a file-by-file overview.
- `README.md` "Project layout" — updated.

**Stage 2 docs:**
- `CLAUDE.md` "Alpine state" — replaces "static/index.html ~L1010" with "static/js/core/state.js".
- `docs/architecture.md` — frontend section gets a module-by-module overview.

## Out of scope

Saved for future work, NOT addressed by this design:

- Tests (highest-priority targets in CLAUDE.md: `safePath`, `isWritable`, `expandEmbeds`, `handleUpload`). Tests would naturally land per file, easier after the split, but that's a separate effort.
- Search index for large vaults (Bleve / Tantivy).
- Reverse attachment-refcount index.
- The skill instructions reference "elements-of-style:writing-clearly-and-concisely" — applied during writing, not a separate output.

## Rollback

Both stages roll back via `git revert <commit>`. No state migration, no schema change, no external dependency change. A revert returns the binary and the bundle to the pre-refactor state with no side effects.

## Acceptance

This design is approved when:
- The user confirms the file layout in Stage 1 (specifically the names and groupings — moving things into different `.go` files later is cheap, but knowing the target shape upfront avoids rework).
- The user confirms the file layout in Stage 2 (same reasoning, larger blast radius if wrong).
- The user confirms that the mixin pattern is the intended JS style.

After approval, an implementation plan is written that breaks each stage into ordered, individually-verifiable steps. Each step is small enough to check in isolation; the plan covers verification commands at every checkpoint.
