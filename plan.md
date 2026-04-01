# VaultReader Planning Round — Feature Backlog & Architecture

**Council:** ⚙ Coder, ◎ Designer, ⚔ Contrarian

---

## Current State Analysis

### What Works
- **Read-only vault browsing:** File tree, folder navigation, wikilink resolution ✓
- **Note viewing:** Markdown rendering, HTML output, metadata extraction ✓
- **Inline editing:** CodeMirror 6 integration, PUT /api/note persistence ✓
- **Discovery:** Search, backlinks, wikilink resolution ✓
- **Status:** Syncthing sync awareness, word/char counts ✓

### Critical Gap: No Create/Delete
**You cannot add a note or remove one.** This is a show-stopper for daily Obsidian replacement. The UI is "read-mostly" with editing baked in, but **creation flows are completely missing**:
- No "new note" button
- No "new folder" action
- No delete/trash
- No rename/move
- No frontmatter templates
- No bulk operations

---

## Prioritized Feature Backlog

### P0 — Must Have (Basic CRUD + UX)

#### 1. Create Note
**Problem:** Can't capture new thoughts; users must use desktop Obsidian or edit filesystem directly.

**UX:**
- Context menu on folder → "New note"
- Toolbar button → "New note" (creates in current folder or root)
- After creation, auto-focus name entry, then open in editor
- Sensible defaults: blank body, optional frontmatter template

**API:**
```
POST /api/note?vault=X&path=Y&template=default
  body: { raw: "# Title\n..." }
  returns: { path, created: true, mtime }
  409 Conflict if note exists
```

**Implementation Complexity:** S
- Go: fs.WriteFile + index rebuild  
- JS: form modal + focus mgmt  
- Path validation critical (prevent `../../` escapes)

**Why P0:** Foundational. Can't be a daily driver without it.

---

#### 2. Delete Note
**Problem:** No way to remove notes; they accumulate forever.

**UX:**
- Right-click context menu on file → "Delete" with confirmation modal
- Toolbar delete button (when note open)
- Undo/trash consideration: **out of scope for P0** (just rm file)

**API:**
```
DELETE /api/note?vault=X&path=Y
  returns: { deleted: true, path }
  404 if not found
```

**Implementation Complexity:** S
- Go: os.Remove + index rebuild  
- JS: confirm modal + optimistic UI update  
- Risk: accidental deletion → consider soft-delete + purge cron later

**Why P0:** Users need cleanup ability. Prevents vault bloat.

---

#### 3. Rename / Move Note
**Problem:** Can't reorganize; notes are locked to creation path.

**UX:**
- Right-click file → "Rename" (inline edit in sidebar)
- Drag-drop file between folders (future P1)
- Show "moving..." spinner while index updates

**API:**
```
POST /api/move?vault=X
  body: { from: "path/old.md", to: "path/new.md" }
  returns: { moved: true, from, to, backlinksUpdated: N }
  409 Conflict if target exists
```

**Implementation Complexity:** M
- Go: os.Rename + backlinks reindex (critical)
- Update all wikilinks that reference old path (or use vault-scoped links)
- JS: rename modal + validation (no slashes, no .md dupes)

**Why P0:** Core organizational primitive.

---

#### 4. Create Folder
**Problem:** Can't organize; forced flat structure.

**UX:**
- Context menu on folder → "New folder"
- Simple text input modal
- Creates immediately in current directory

**API:**
```
POST /api/folder?vault=X&path=Y
  returns: { path, created: true }
  409 Conflict if exists
```

**Implementation Complexity:** S
- Go: os.MkdirAll  
- JS: modal + path validation

**Why P0:** Enables hierarchical org; users expect this.

---

#### 5. Delete Folder
**Problem:** Can't remove empty or obsolete folders.

**UX:**
- Right-click folder → "Delete" (only if empty, or confirm recursion)
- Show warning if folder has files

**API:**
```
DELETE /api/folder?vault=X&path=Y
  returns: { deleted: true, path }
  409 Conflict if not empty
```

**Implementation Complexity:** S
- Go: os.RemoveAll (or check empty + os.Remove)
- JS: warn if non-empty

**Why P0:** Hygiene & org.

---

### P1 — High Value (Quality of Life + Advanced Features)

#### 6. Note Templates
**Problem:** Users repeat boilerplate (daily log, project, fleeting note).

**UX:**
- "New note" modal with template selector
- Builtin templates: blank, daily, project, fleeting
- Admin UI to define vault-specific templates
- Insert template at creation OR at cursor in editor

**API:**
```
GET /api/templates?vault=X
  returns: { templates: [{ name, body, frontmatter }] }

POST /api/note?template=daily
  auto-fills frontmatter + boilerplate
```

**Implementation Complexity:** M
- Go: template dir, YAML parsing  
- JS: dropdown + preview  
- Risk: template bloat if not carefully scoped

**Why P1:** Speeds common workflows; not strictly required for MVP.

---

#### 7. Wikilink Auto-Update on Rename
**Problem:** Rename "FooBar" → "FooBaz", but 10 notes still reference [[FooBar]].

**UX:**
- After rename, show "Update X backlinks?" dialog
- Auto-update all references (vault-scoped or global)
- Show list of changed files

**API:**
```
POST /api/move (enhanced)
  body: { from: "...", to: "...", updateBacklinks: true }
  returns: { moved: true, backlinksUpdated: [list of files] }
```

**Implementation Complexity:** M
- Go: tree-walk all notes, regex replace [[oldname]] → [[newname]]  
- Handle conflicts (e.g., renamed to name that already exists elsewhere)
- JS: progress UI during bulk update

**Why P1:** Prevents link rot; expected in note apps.

---

#### 8. Bulk Move
**Problem:** Can't move entire folder subtree to new location.

**UX:**
- Multi-select files in sidebar (Ctrl+Click or Cmd+Click)
- Drag selected files to target folder
- "Move to..." context menu

**API:**
```
POST /api/bulk-move?vault=X
  body: { from: ["a.md", "b.md"], to: "dest/" }
  returns: { moved: N, backlinksUpdated: M }
```

**Implementation Complexity:** M
- Go: batch fs operations  
- JS: multi-select state mgmt in Alpine

**Why P1:** Power users need this; not essential for daily use.

---

#### 9. Trash / Soft Delete
**Problem:** `rm` is permanent; users fear accidental deletion.

**UX:**
- Delete note → moves to `.trash` subfolder with timestamp
- Restore button / Purge after 30 days
- Trash size warnings in status bar

**API:**
```
POST /api/trash?vault=X&path=Y
  soft-deletes to vault/.trash/timestamp_path

GET /api/trash?vault=X
  returns: { items: [{ path, deletedAt }] }

POST /api/restore?vault=X&path=Y
  moves back to original location (or prompt if exists)
```

**Implementation Complexity:** M
- Go: mkdir .trash, mv instead of rm, metadata file  
- Cron/GC to purge old items  
- JS: trash view modal

**Why P1:** Safety net; expected UX pattern. Can defer if time-constrained.

---

#### 10. Frontmatter Editor
**Problem:** Frontmatter hidden in raw markdown; no structured YAML editing.

**UX:**
- Toggle "Edit Metadata" in toolbar
- Rendered form view with fields (title, tags, date, custom)
- Auto-sync with raw content

**API:**
- No new endpoints; uses existing PUT /api/note

**Implementation Complexity:** M
- JS: CodeMirror plugin or separate form pane  
- Parse/stringify YAML ↔ form fields  
- Validate YAML syntax before save

**Why P1:** Improves discoverability of metadata; quality-of-life.

---

### P2 — Nice-to-Have (Advanced Features)

#### 11. Vault Settings / Config
**Problem:** No per-vault customization (note extension, sorting, sync behavior).

**UX:**
- Gear icon per vault → settings modal
- Configure: note extension (.md vs .markdown), sort order, exclude patterns
- Store in `.vaultconfig.yaml` at vault root

**API:**
```
GET /api/vault/X/config
PUT /api/vault/X/config
  body: { noteExt: ".md", sort: "mtime", excludePatterns: [...] }
```

**Implementation Complexity:** M

**Why P2:** Customization is secondary to core CRUD.

---

#### 12. Quick Capture (Global Hotkey)
**Problem:** Users want to jot notes without switching windows.

**UX:**
- Hotkey (e.g., Cmd+Shift+N) to open "New note" dialog
- Captures to daily note or inbox
- Auto-return focus to previous window

**Implementation Complexity:** XL (requires native app or browser extension)

**Why P2:** Out of scope for web app; defer to future native build.

---

#### 13. Sync Conflict Resolution
**Problem:** If Syncthing has conflicts, no UI to resolve.

**UX:**
- Status bar alert → "Conflicts detected"
- Modal showing conflicting files, merge options (keep remote, keep local, manual merge)

**API:**
```
GET /api/sync-conflicts?vault=X
  returns: { conflicts: [{ file, synced, local, remote }] }

POST /api/resolve-conflict
  body: { file, resolution: "remote|local|merge" }
```

**Implementation Complexity:** L
- Requires diff UI, merge logic
- Touch Syncthing state outside Go

**Why P2:** Only needed if Syncthing is primary sync; may not happen often.

---

#### 14. Search Filters & Refinements
**Problem:** Search is basic keyword; no advanced queries.

**UX:**
- Search: `vault:work tag:urgent from:2025`  
- Facets: vault, tag, date range, type
- Saved searches

**Implementation Complexity:** L

**Why P2:** Requires search index overhaul; nice-to-have.

---

#### 15. Export Note / Vault
**Problem:** Users locked into web viewer; want to export.

**UX:**
- Right-click note → Export as .md / PDF / HTML
- Right-click vault → Export as .zip

**API:**
```
GET /api/note/X/Y/export?format=pdf
  returns: binary PDF

GET /api/vault/X/export?format=zip
  returns: binary ZIP
```

**Implementation Complexity:** L
- PDF rendering (need headless browser or Go lib)
- ZIP all files

**Why P2:** Escape hatch; not required if vault is primary source of truth.

---

## Architecture & Implementation Notes

### Go Backend Changes (Priority Order)

1. **Path validation utility**  
   Prevent `../`, absolute paths, double slashes, null bytes.  
   Use for all note/folder operations.

2. **Create / Delete / Move Note handlers**  
   ```go
   func handlePostNote(w http.ResponseWriter, r *http.Request)
   func handleDeleteNote(w http.ResponseWriter, r *http.Request)
   func handleMoveNote(w http.ResponseWriter, r *http.Request)
   ```
   All must rebuild index after modification.

3. **Index rebuild on mutation**  
   Currently only done at startup. Need incremental rebuild:
   - On file create: add to allNotes, scan for wikilinks
   - On file delete: remove from allNotes, update inbound/outbound
   - On file move: update paths in index

4. **Backlink update on rename**  
   After rename, tree-walk all notes in vault, regex-replace old wikilink refs.  
   Batch write changes, rebuild index once.

5. **Folder CRUD**  
   `POST /api/folder` → os.MkdirAll  
   `DELETE /api/folder` → os.RemoveAll (with safety check)

### Frontend (Alpine.js) Changes

1. **Context menus**  
   Implement right-click handlers on file/folder items.  
   Show: New, Rename, Delete, Move options.

2. **Modals**  
   - "New note" (name input, template selector)
   - "Rename" (inline or modal)
   - "Delete confirm" (with warning)
   - "New folder" (simple name input)

3. **Multi-select (future)**  
   Ctrl+Click or Cmd+Click to select files.  
   Show "Move N items" or "Delete N items" in toolbar.

4. **Loading states**  
   Spinner during create/delete/move to show async activity.

5. **Error handling**  
   Display 409 Conflict, 400 Bad Path, 500 errors in toasts.

### Data Flow: Create Note Example
```
User clicks "New note" in folder
  → Modal opens (name input, template dropdown)
  → User enters "My research"
  → POST /api/note?vault=work&path=projects/my-research.md
  → Backend:
      1. Validate path (no ../, etc.)
      2. WriteFile to disk
      3. Rebuild index (add to allNotes, scan for links)
      4. Return { path, created: true, mtime }
  → Frontend:
      1. Update tree (add new item to currentFiles())
      2. Open note in editor
      3. Set focus to content pane
```

---

## Risk & Mitigation

| Risk | Mitigation |
|------|-----------|
| Path traversal attacks | Strict path validation: no `../`, absolute paths, null bytes |
| Accidental deletion | P1: Trash/soft-delete; P0: confirm modal |
| Backlink rot after rename | P1: auto-update + list of changed files |
| Index out-of-sync after mutation | Rebuild index in every write handler; use mu.Lock() |
| Wikilink resolution breaks | Test with complex vault structures; handle case-insensitive lookups |
| Syncthing conflicts | P2: conflict resolution UI; for now, warn in status bar |

---

## What We're **NOT** Doing (Scope Cutouts)

- **Collaborative editing:** Too complex for MVP; single-user assumption
- **Native mobile apps:** Web-only for now
- **Git history / version control:** Defer to future; Syncthing is primary sync
- **Encrypted vaults:** Security model TBD
- **AI features (auto-tagging, etc.):** Out of scope; focus on core UX
- **Custom themes / CSS editing:** Stock theme only
- **Tabs / split pane editing:** Single note at a time

---

## Sprint Estimate

### P0 (Must have) — ~3–4 days
- Create note: 4 hrs
- Delete note: 2 hrs
- Rename/move note: 6 hrs
- Create/delete folder: 2 hrs
- Integration testing: 4 hrs

### P1 (High value) — ~2–3 days (if time allows)
- Wikilink auto-update: 4 hrs
- Soft delete/trash: 4 hrs
- Templates: 4 hrs

### P2 (Nice-to-have) — Later sprints

---

## Decision Log

**Why no Obsidian plugin API?**  
- Too much scope; focus on core CRUD first.

**Why no native app yet?**  
- Web app proven & Syncthing-synced; native is later optimization.

**Why soft-delete instead of permanent rm?**  
- UX best practice; but P1, not P0. P0 is "quick delete with confirm".

**Why not REST resource model (e.g., /api/notes/{path})?**  
- Query string format is simpler for existing codebase; avoid breaking changes.

---

## Next Steps (Round 2)

1. ⚙ **Coder:** Implement P0 CRUD endpoints + path validation
2. ◎ **Designer:** Build modal UX components (new, rename, delete, folder)
3. ⚔ **Contrarian:** Challenge complexity, review PR for over-engineering

