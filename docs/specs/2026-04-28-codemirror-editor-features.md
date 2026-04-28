# CodeMirror editor features — toolbar, wikilink autocomplete, paste-image upload

**Status:** approved 2026-04-28
**Scope:** edit-mode UX in the existing CodeMirror 6 instance bundled at `static/codemirror.bundle.js`.

## Goal

Make edit mode actually pleasant. Today, opening a note in edit mode drops you into a bare CodeMirror buffer. You type raw markdown. There's no visual scaffolding for common syntax, no help inserting wikilinks (the most-used syntax in this app), and no way to get an image into a note short of typing the full markdown by hand against an attachment that doesn't exist yet.

This spec adds three independent features that share the editor surface:

1. **Toolbar** — a thin button strip pinned above the editor, showing only when `mode === 'edit'`. 14 buttons that wrap the current selection with the corresponding markdown syntax. One of them (📊 Mermaid) is a popover that inserts a starter for one of five diagram types.
2. **Wikilink autocomplete** — typing `[[` opens a CodeMirror autocomplete popup populated by `/api/search` against the active vault. Selecting a result inserts `[[path]]` (vault-relative path, no `.md` suffix, matching what the renderer already resolves).
3. **Paste-image upload** — pasting/dropping an image into the editor uploads it via a new `POST /api/upload` endpoint, which writes it to `<note-dir>/attachments/<basename>-<timestamp>.<ext>` (creating `attachments/` if needed), then inserts `![[attachments/<filename>]]` at the cursor. The new endpoint reuses `isWritable` and `safePath` exactly like `handlePutNote` does.

Each is independent and can ship separately, but they're specified together because they all touch the same edit-mode surface.

## Non-goals

- WYSIWYG / live preview (we keep the explicit preview/edit toggle).
- Obsidian-flavored syntax that the renderer doesn't already support: callouts, `==highlight==`, `%% comments %%`, `$math$`, footnotes, dataview. The toolbar will not expose any syntax that doesn't render.
- Mobile toolbar. Hidden under `width <= 700px` to keep the mobile editor full-width.
- Multi-file paste. One image per paste.
- Server-side image processing (resize, format conversion). Bytes are stored as-is.
- Editing the autocomplete trigger character or fuzzy-matching scoring — we use whatever `/api/search` returns, in order.

## Prerequisite: Mermaid v10.9 → v11 upgrade

The bundled `static/mermaid.min.js` is v10.9. Block diagrams require v11+. To include the **Block** Mermaid starter, we upgrade first.

**Steps** (per the existing skill doc procedure):

1. Download `https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js` → save to `static/mermaid.min.js`.
2. Smoke-test in a browser tab pointing at `notes.joao.date`: open the canonical sample diagram (the LIS10 PEP org chart referenced in the skill doc) and confirm it still renders. Also open one note containing a `flowchart`, one `gantt`, one `sequenceDiagram` — pick from the live vaults — and confirm none regressed.
3. If a regression is found, revert the file (one-file change), document the v10/v11 syntax delta in the skill doc, and reduce the starter set to v10-compatible ones (i.e. drop Block).
4. Commit as a separate commit before the toolbar work.

Risk: low. Mermaid v10 → v11 release notes flag breaking changes only in `mindmap` and `quadrantChart` (neither used in this app). All five starter diagrams (flowchart, sequence, gantt, pie, block) are v11-stable.

## Feature 1: Toolbar

### UI

A 32px-tall row above `#cm-editor`, visible only when `activePath && mode === 'edit'`, hidden under 700px viewport. Buttons are 28×28 with a 1px separator between groups. Tooltips on every button (title attribute).

Layout left-to-right, with `|` denoting a visual separator:

```
B  I  ~~  |  H  •  1.  ☐  > |  </>  ⌘  ⊞ |  🔗  [[  📊
```

| # | Button | Title tooltip | Inserts (selection-aware where it makes sense) |
|---|---|---|---|
| 1 | **B** | Bold (Ctrl+B) | wrap selection in `**…**`; if no selection, insert `****` and place cursor between |
| 2 | *I* | Italic (Ctrl+I) | wrap in `*…*` |
| 3 | ~~ | Strikethrough | wrap in `~~…~~` |
| 4 | H | Heading — cycles H1→H2→H3→none on the current line | toggle leading `# `, `## `, `### `, none on current line |
| 5 | • | Bullet list | prefix every selected line (or current line) with `- ` |
| 6 | 1. | Numbered list | prefix selected lines with `1. ` (CodeMirror won't auto-renumber — leaving raw `1.` on every line is what GFM accepts) |
| 7 | ☐ | Task | prefix with `- [ ] ` |
| 8 | `>` | Quote | prefix with `> ` |
| 9 | `</>` | Inline code (Ctrl+E) | wrap in `` `…` `` |
| 10 | ⌘ | Code block | insert `` ```\n<selection or empty>\n``` `` (asks for language via small inline prompt? No — keep it simple, user types language after the opening fence) |
| 11 | ⊞ | Table | insert a 3×3 GFM table starter at cursor on its own line |
| 12 | 🔗 | Link (Ctrl+K) | wrap selection in `[selection](url)` with cursor positioned on `url` |
| 13 | `[[` | Wikilink (Ctrl+L) | insert `[[]]`, cursor between brackets, immediately triggers autocomplete (Feature 2) |
| 14 | 📊 | Mermaid | opens popover with five starter options |

**Heading cycle behavior**: detect leading `#`s on current line, increment count; at 4 (`####`) wrap back to 0. Standard markdown editor pattern.

**Mermaid popover**: a small flyout (anchored to the 📊 button), 5 vertical items, each click inserts a fenced `mermaid` block on its own line at cursor:

| Starter | Source |
|---|---|
| Flowchart | `flowchart TD\n  A[Start] --> B{Decision}\n  B -->\|Yes\| C[Process A]\n  B -->\|No\| D[Process B]\n  C --> E[End]\n  D --> E` |
| Sequence | `sequenceDiagram\n  participant Alice\n  participant Bob\n  Alice->>Bob: Hello\n  Bob-->>Alice: Hi back\n  Note over Bob: Thinking…` |
| Gantt | `gantt\n  dateFormat YYYY-MM-DD\n  section Phase 1\n  Task A :a1, 2026-05-01, 7d\n  Task B :after a1, 5d\n  section Phase 2\n  Release :crit, milestone, after a1, 1d` |
| Pie | `pie title Distribution\n  "A" : 40\n  "B" : 35\n  "C" : 25` |
| Block | `block\n  columns 3\n  A B C\n  D E F\n  A --> D\n  B --> E\n  C --> F` |

All five wrapped in `` ```mermaid `` … `` ``` `` fences with cursor placed on the line after the closing fence.

### Implementation

- New CodeMirror plugin file? **No.** Toolbar is plain HTML in `index.html` controlled by Alpine, calling helper methods that mutate CodeMirror via the editor's `view` reference. The existing builder at `static/index.html:786-810` already constructs an `EditorView` and stores it on the wrapper; we expose a `vaultEditor.getView()` accessor so Alpine methods can dispatch transactions. All toolbar handlers are Alpine methods, e.g. `toolbarBold()`, `toolbarHeading()`, `insertMermaid(kind)`.
- Selection wrapping uses CodeMirror `view.dispatch({ changes: { from, to, insert: '**' + text + '**' } })` and a `EditorSelection` to set the cursor.
- Keyboard shortcuts (`Ctrl+B`, `Ctrl+I`, `Ctrl+E`, `Ctrl+K`, `Ctrl+L`) registered via the existing global `keydown` listener at `static/index.html:948`, gated on `document.activeElement?.closest('.cm-editor')`.
- CSS lives in `static/style.css` under a new section `/* ── Editor toolbar ─────────── */` — buttons reuse `.btn-icon` styling.

## Feature 2: Wikilink autocomplete

### Trigger and dismissal

- User types `[`, then a second `[`. After the second `[`, fire `/api/search?vault=<active>&q=` (initially empty — show recent / all titles).
- As the user keeps typing characters that aren't `]` or whitespace, debounce 150ms and re-query `/api/search?vault=<active>&q=<typed>`.
- Popup is a CodeMirror autocomplete tooltip (the bundle's `@codemirror/autocomplete` package — confirm it's present; if not, build a lightweight popup positioned via `view.coordsAtPos`).
- Arrow keys navigate, Enter inserts, Esc dismisses.
- Inserting a result replaces the typed query with the path (vault-relative, no `.md`), e.g. `[[notes/foo]]` if the user clicked the result for `notes/foo.md`. Closes both brackets if the user hadn't yet typed them.
- `]` typed by the user dismisses the popup (lets them keep going past the wikilink).

### What goes in the popup

Each row shows: bold note basename (without `.md`), then dim path. Limit 8 results — if `/api/search` returns more, the user keeps typing.

### Implementation

- New Alpine method `_wikiCompleteCandidates(query)` — wraps the existing `/api/search` call. Reuses `searchResults` shape.
- The bundled `static/codemirror.bundle.js` does NOT export `@codemirror/autocomplete` (only `EditorState`, `EditorView`, `defaultKeymap`, `history`, `historyKeymap`, `indentWithTab`, `keymap`, `lineNumbers`, `markdown`, `oneDark`, `syntaxHighlighting`, `defaultHighlightStyle`, `highlightActiveLine`, `drawSelection`). Rather than rebuild the bundle, hand-roll a small popup: a CodeMirror `updateListener` watches for cursor preceded by `[[` (no `]` between the brackets and cursor; skip if cursor is inside a fenced code block — simple heuristic: count ` ``` ` lines above). Popup is a `<div class="cm-wiki-popup">` positioned via `view.coordsAtPos(cursor)`. Up/down arrows + Enter handled in a CodeMirror keymap with `precedence.high`. Esc/`]`/click-outside dismiss.
- The current editor builder is at `static/index.html` near line 800; the new extension is appended to the existing `extensions` array.

## Feature 3: Paste/drop image upload

### Backend

New handler in `main.go`:

```go
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request)
```

Registered as `mux.HandleFunc("/api/upload", srv.handleUpload)` next to the other note routes around line 2050.

**Behavior:**
- POST only.
- multipart/form-data with fields: `vault`, `notePath` (the *containing note*, not the image), `file`.
- Validates `vault` and `notePath` via `safePath` exactly as `handlePutNote` does.
- Validates `s.isWritable(vault, notePath)` — same guard as note edits. 403 if not writable.
- Validates Content-Type starts with `image/`. Rejects others 400.
- Caps body size with `http.MaxBytesReader(w, r.Body, 10<<20)` (10MB), mirroring the per-handler pattern at `main.go:833` in `handleAdminConfig`. Note: there is no global body-size middleware; each handler that needs a cap sets one explicitly.
- Computes target dir: `filepath.Join(<note-dir>, "attachments")`. Creates with 0755 if missing.
- Computes filename: `<note-basename>-<unix-ts>.<ext>` where `<ext>` comes from the Content-Type (`image/png` → `png`, etc.). If extension can't be inferred, reject 400.
- Writes file with `os.WriteFile` 0644.
- Returns JSON `{ "path": "attachments/<filename>" }` (relative to the note's directory, exactly the form the client needs to embed).

**Security checks reused from existing handlers:**
- `safePath` resolves and confirms the path is within the vault root (existing function at `main.go:1130`).
- `isWritable` confirms the path matches an admin-configured RW prefix (existing at `main.go:787`).
- File names are server-generated — user-supplied filenames are ignored to prevent path injection.

### Frontend

- Editor extension that listens for `paste` and `drop` events on the CodeMirror DOM.
- For each event, walk `clipboardData.items` / `dataTransfer.files`, find first `image/*`.
- Build a `FormData`, POST to `/api/upload`. Show an inline "uploading…" decoration at the cursor position (small spinner span) — replace it with `![[attachments/<filename>]]` once the response lands. On error, replace with a red error toast (existing `modal` pattern can be reused with `confirmOnly: true`).
- If multiple images are pasted at once, take only the first and show a toast: "One image at a time."

### Edge case: unwritable note

If `isWritable` returns false on the server, the 403 response lands in the frontend handler, which surfaces a modal: "This note's path isn't in the writable list. Add it from the admin panel." (Reuses existing 403 handling pattern in note saves.)

## Data flow

```
Toolbar:        Alpine click → editorView.dispatch(changes) → CodeMirror updates buffer → autosave fires per existing scheduleSave()

Autocomplete:   Keystroke → CodeMirror update listener → debounce → /api/search?vault=&q= → popup → Enter → editorView.dispatch(replace range)

Paste/drop:     paste event → FormData → POST /api/upload → handleUpload writes file → 200 { path } → editorView.dispatch(insert "![[attachments/X]]")
```

## Files touched

| File | Change |
|---|---|
| `main.go` | + `handleUpload`, + route registration, + body-size cap for upload route |
| `static/mermaid.min.js` | replaced (v10.9 → v11) |
| `static/index.html` | + toolbar HTML block, + Alpine state for toolbar+autocomplete+upload, + helper methods, + CodeMirror extensions |
| `static/style.css` | + toolbar styles, + autocomplete tooltip styles, + upload spinner, + responsive hide under 700px |
| `docs/specs/2026-04-28-codemirror-editor-features.md` | this file |

## Testing plan

- Manual: each toolbar button on selection / no-selection / multi-line cases.
- Manual: each Mermaid starter renders cleanly in preview after insertion.
- Manual: `[[` autocomplete shows results, arrow + Enter inserts, Esc dismisses, typing `]` dismisses without inserting.
- Manual: paste a screenshot, verify file lands in `attachments/`, embed renders in preview.
- Manual: paste into an unwritable note (e.g. one of the read-only vaults) — expect 403 modal.
- Backend: curl `POST /api/upload` with a mock PNG to confirm path traversal is blocked (`notePath=../etc/passwd`).
- Backend: curl with non-image Content-Type → 400.
- Backend: curl with no admin-token + writable path → still works (uploads are user-action, not admin-action; same trust model as `PUT /api/note`).

## Rollout

One commit per feature, in this order:

1. Mermaid v11 upgrade (separate, isolated, easy to revert).
2. Toolbar (Feature 1) — pure frontend.
3. Backend `/api/upload` + paste/drop wiring (Feature 3) — backend + frontend together; couples cleanly.
4. Wikilink autocomplete (Feature 2) — pure frontend.

After each commit: `git push`, rebuild via `dc-office-up -d --build vaultreader`, smoke-test the live site, then move on.
