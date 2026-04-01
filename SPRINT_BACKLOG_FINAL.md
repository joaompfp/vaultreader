# VaultReader Sprint Backlog — Contrarian's Final Review

**Status:** Round 2 Planning Complete  
**Author:** ⚔ Contrarian (scope governance)  
**Consensus:** Reviewed both Coder and Designer backlogs. Filtering ruthlessly.

---

## Executive Summary

VaultReader is **read-mostly** with inline editing baked in. The critical gap: **no create/delete/rename**. This sprint adds those three operations — **and nothing else P0**.

**Scope:** 5 API endpoints + 5 Alpine.js modals.  
**Timeline:** 16–20 hours implementation + dogfood.  
**Goal:** Make VaultReader a usable daily driver for vault editing.

---

## Final Prioritization

### P0 — MUST HAVE (16–20 hours)

These are blocking. Without them, VaultReader is still read-mostly.

#### 1. Create Note
**Problem:** No way to capture new ideas; must use desktop Obsidian or raw filesystem.

**UX:**
- Toolbar button: "New note"
- Modal: filename input + folder selection (defaults to current)
- After creation, auto-open in edit mode
- Input validation: no leading/trailing spaces, require .md extension

**API:**
```
POST /api/note?vault=X&path=Y
  body: {raw: ""}
  returns: {path, created: true, mtime}
  409 Conflict if note exists
```

**Implementation:** 3 hours
- Go: validate path, WriteFile, update index
- JS: modal + focus management
- Risk: path traversal (use existing safePath utility)

---

#### 2. Delete Note
**Problem:** No way to clean up; notes accumulate forever.

**UX:**
- Right-click file → "Delete" with confirmation modal
- Show filename + parent path in confirmation
- After delete, update sidebar immediately (optimistic UI)

**API:**
```
DELETE /api/note?vault=X&path=Y
  returns: {deleted: true, path}
  404 if not found
```

**Implementation:** 2 hours
- Go: os.Remove, update index (remove from allNotes + backlinks)
- JS: confirm modal + tree refresh
- Risk: accidental deletion → use confirmation modal (no undo in P0)

---

#### 3. Rename / Move Note
**Problem:** Can't fix typos or reorganize; notes are locked to creation path.

**UX:**
- Right-click file → "Rename" 
- Modal: show current path + new path input
- Validate: no duplicate .md files in target folder
- After move, show spinner while backlinks are updated (can take time)

**API:**
```
POST /api/move?vault=X
  body: {from: "path/old.md", to: "path/new.md"}
  returns: {moved: true, backlinksUpdated: N}
  409 Conflict if target exists
```

**Implementation:** 6 hours
- Go: os.Rename, then tree-walk vault to update wikilinks
- Index update: rebuild paths + backlinks
- JS: rename modal + "Updating links..." spinner
- Risk: slow on large vaults (1000+ files) → mitigate with async + progress feedback

**Backlink Update Strategy:**
- Simple regex-replace: old basename → new basename
- Example: `[[OldNote]]` → `[[NewNote]]` (vault-scoped)
- Only affects wikilinks; regular markdown links unchanged
- Batch write + single index rebuild after all changes

---

#### 4. Create Folder
**Problem:** Can't organize; forced flat structure.

**UX:**
- Context menu on folder → "New folder"
- Modal: folder name input
- Validation: no `/` or special chars in name

**API:**
```
POST /api/folder?vault=X&path=Y
  returns: {path, created: true}
  409 Conflict if exists
```

**Implementation:** 1.5 hours
- Go: os.MkdirAll (ensures parent dirs exist)
- JS: simple modal, tree refresh
- Risk: user creates `.hidden` folders → document that `.X` folders are hidden

---

#### 5. Delete Folder
**Problem:** Can't remove empty or obsolete folders; sidebar bloat.

**UX:**
- Context menu on folder → "Delete"
- If folder has items: show count + ask "Delete folder + X items?" with confirmation
- If empty: simple confirm modal

**API:**
```
DELETE /api/folder?vault=X&path=Y
  query param: ?recursive=true (if non-empty)
  returns: {deleted: true, path, itemsAffected: N}
  409 Conflict if non-empty and !recursive
```

**Implementation:** 2 hours
- Go: check empty, then os.Remove or os.RemoveAll
- JS: confirm modal with item count
- Risk: user accidentally deletes non-empty folder → require explicit checkbox "Delete all items"

---

## Context Menu (Shared UX)

All CRUD operations use a **right-click context menu** on files and folders:

**File context menu:**
- Rename
- Delete
- Copy wikilink (existing feature)

**Folder context menu:**
- New note
- New folder
- Rename
- Delete

**Implementation:** 4 hours
- JS: contextmenu event + position calculation
- Alpine x-show binding for menu
- Click outside to close
- Keyboard accessible (Tab + Enter)

---

## Frontend Changes (Alpine.js)

**New x-data properties:**
```javascript
{
  // Modal state
  createNoteOpen: false,
  createNoteName: '',
  deleteConfirmOpen: false,
  deleteConfirmPath: '',
  deleteConfirmIsFolder: false,
  renameOpen: false,
  renamePath: '',
  newPath: '',
  createFolderOpen: false,
  folderName: '',
  
  // Context menu
  contextMenu: { x: 0, y: 0, open: false, target: null },
  
  // Loading states
  isSaving: false,
  saveError: '',
}
```

**New Alpine methods:**
```javascript
async createNote()            // POST /api/note
async deleteNote()            // DELETE /api/note
async renameNote()            // POST /api/move
async createFolder()          // POST /api/folder
async deleteFolder()          // DELETE /api/folder (with ?recursive=true)
showContextMenu(e, target)    // Right-click handler
closeAllModals()              // Cleanup
validatePath(path)            // Client-side path validation
refreshTree()                 // After mutations
handleApiError(err)           // Toast notifications
```

---

## Go Backend Changes (main.go)

**New handler functions (~200 LOC):**

```go
func (s *server) handlePostNote(w http.ResponseWriter, r *http.Request)
  - Parse vault + path from query
  - Validate path safety (safePath utility)
  - Read body (initial content)
  - Create parent dirs: os.MkdirAll(parentDir)
  - WriteFile
  - Update index: idx.updateNote()
  - Return 201 Created + path

func (s *server) handleDeleteNote(w http.ResponseWriter, r *http.Request)
  - Parse vault + path
  - Validate path safety
  - os.Remove (hard delete)
  - Update index: idx.deleteNote() [NEW METHOD]
  - Return 204 No Content

func (s *server) handleMove(w http.ResponseWriter, r *http.Request)
  - Parse vault + {from, to}
  - Validate both paths
  - os.Rename (fails if target exists)
  - Scan vault for wikilinks: findAndUpdateBacklinks()
  - Index update: rebuild paths + inbound/outbound
  - Return 200 {moved, backlinksUpdated}

func (s *server) handlePostFolder(w http.ResponseWriter, r *http.Request)
  - Parse vault + path
  - os.MkdirAll
  - Return 201 Created

func (s *server) handleDeleteFolder(w http.ResponseWriter, r *http.Request)
  - Parse vault + path + ?recursive
  - If non-empty && !recursive: 409 Conflict
  - os.RemoveAll
  - Index cleanup: remove all notes under path
  - Return 204 or 200 {deleted, itemsAffected}
```

**Index changes (~50 LOC):**

```go
type NoteIndex struct {
  // existing...
  // ADD THIS METHOD:
}

func (idx *NoteIndex) deleteNote(vault, path string)
  - Remove from allNotes
  - Remove from outbound/inbound
  - Cleanup backlinks

func findAndUpdateBacklinks(vault, oldBasename, newBasename string) error
  - Walk all .md files in vault
  - Regex: [[oldBasename]] → [[newBasename]]
  - Write changes back
  - Return count updated
```

**Utility functions (~100 LOC):**

```go
func isPathSafe(basePath, userPath string) bool
  // Already exists; reuse
  // Prevents ../ directory traversal, absolute paths, null bytes

func updateBacklinksOnMove(vaultPath, oldPath, newPath string) error
  // NEW: core of rename feature
  // Returns: count of files updated

func copyFile(src, dst string) error
  // Already exists; no change needed
```

---

## API Endpoint Summary

### Routing in main()

```go
mux.HandleFunc("/api/note", srv.handleNote)  // PATCH existing to dispatch on method
  GET  → existing handleGetNote
  PUT  → existing handlePutNote
  POST → NEW handlePostNote
  DELETE → NEW handleDeleteNote

mux.HandleFunc("/api/move", srv.handleMove)  // NEW

mux.HandleFunc("/api/folder", srv.handleFolder)  // NEW dispatcher
  POST → handlePostFolder
  DELETE → handleDeleteFolder
```

---

## Testing Strategy

### Go Unit Tests (~50 tests)

```go
TestCreateNote_Success()
TestCreateNote_Conflict()
TestCreateNote_InvalidPath()
TestDeleteNote_Success()
TestDeleteNote_NotFound()
TestMoveNote_Success()
TestMoveNote_UpdateBacklinks()
TestMoveNote_Conflict()
TestCreateFolder_Success()
TestDeleteFolder_Empty()
TestDeleteFolder_NonEmpty_NoRecursive()
TestDeleteFolder_NonEmpty_Recursive()
```

### Frontend Integration Tests (Dogfood)

- Create note in current folder → verify appears in sidebar
- Create note in subfolder → navigate + create → verify hierarchy
- Delete note → verify gone from sidebar + no backlinks broken
- Rename note with backlinks → verify all links updated + show count
- Create folder → navigate into it → create note there
- Delete empty folder → verify gone
- Delete non-empty folder → verify error + count shown
- Context menu on file → test all options
- Context menu on folder → test all options
- Error cases: invalid filenames, duplicate names, permission errors

**Dogfood target:** 20+ scenarios passing with no console errors.

---

## P1 Deferred (Next Sprint)

These are valuable but **not blocking**. Schedule for Sprint 2 (1–2 weeks later).

- Undo/Redo (CodeMirror has native support; client-only)
- Keyboard hotkeys (Ctrl+N, Ctrl+D, etc.)
- Trash/soft-delete (adds complexity; most users don't need)
- Quick Switcher (Ctrl+P fuzzy search)
- Command Palette (Ctrl+Shift+P)
- Tag extraction + filtering
- Mobile gestures (can use web UI as-is)
- Templates (blank note sufficient for MVP)
- Bulk operations (rare, complex)

---

## Risk Mitigations

| Risk | Severity | Mitigation |
|------|----------|-----------|
| Path traversal attacks | HIGH | Use existing safePath() utility, validate both from + to |
| Accidental deletion | HIGH | Confirmation modals with explicit filename shown |
| Backlink update slow (1000+ files) | MEDIUM | Show spinner + async tree-walk, return count to user |
| Concurrent edit (Syncthing + web) | MEDIUM | mtime check on load; warn if external change detected |
| User creates `.hidden` folders | LOW | Document that folders starting with `.` are hidden |
| Wikilink resolution breaks | MEDIUM | Test with complex vault structures; handle case-insensitive |
| Modal form submission fails | LOW | Client-side validation + error toast + clear error message |
| Index out-of-sync after mutation | HIGH | Rebuild index in every write handler; use mu.Lock() |

---

## Success Criteria (Definition of Done)

- [ ] All 5 CRUD endpoints working end-to-end (create, delete, rename, folders)
- [ ] Context menu (right-click) functional on all file/folder items
- [ ] Create note modal: filename validation + auto-open in edit
- [ ] Delete modals: show item count for folders, clear confirmation text
- [ ] Rename modal: validate no duplicates, show "Updating links..." spinner
- [ ] Backlinks updated when notes renamed/moved (regex replacement tested)
- [ ] Sidebar tree refreshes immediately after mutations
- [ ] All edge cases handled: empty filenames, special chars, deep paths, existing files
- [ ] No console errors in browser DevTools
- [ ] Dogfood: 20+ test scenarios passing
- [ ] Performance acceptable: create/delete/rename <200ms on 100+ note vault

---

## Implementation Timeline

**Total Sprint:** 16–20 hours implementation + 4–6 hours dogfood + polish.

| Task | Hours | Owner | Dependencies |
|------|-------|-------|--------------|
| Create note endpoint + tests | 3 | Coder | safePath() |
| Delete note endpoint + tests | 2 | Coder | index.deleteNote() |
| Rename/move endpoint + tests + backlink walk | 6 | Coder | Highest risk; most complex |
| Folder CRUD endpoints + tests | 3 | Coder | — |
| Create/delete/rename modals | 2 | Designer | After API spec |
| Context menu (file + folder) | 2 | Designer | After modals |
| Validation + error handling | 2 | Designer | Parallel with modals |
| Alpine.js integration | 2 | Designer | After modals |
| Dogfood + bug fixes | 4–6 | Both | After integration |

**Critical path:** Coder starts with safePath audit + rename/move (highest complexity). Designer starts with context menu + modals (can be parallelized).

---

## What We're **NOT** Doing (Scope Cutouts)

❌ **Soft delete to trash folder** — complexity, storage mgmt, GC cron. P1.  
❌ **Undo/Redo system** — nice-to-have, CodeMirror has native support. P1.  
❌ **Hotkeys (Ctrl+N, Ctrl+D, etc.)** — power user feature, not MVP. P1.  
❌ **Quick Switcher (Ctrl+P)** — fuzzy search is polish. P1.  
❌ **Command Palette** — discoverable but not blocking. P1.  
❌ **Tag extraction + filtering** — nice-to-have, no schema impact. P1.  
❌ **Templates system** — blank note is sufficient. P1.  
❌ **Bulk operations** — rare, complex state management. P2.  
❌ **Mobile gestures** — web UI works fine; native is future. P2.  
❌ **Favorites/pinning** — vanity feature. P2.  
❌ **Frontmatter editor** — metadata rarely edited via web. P2.  

---

## Decision Log

| Decision | Rationale | Consensus |
|----------|-----------|-----------|
| P0 = CRUD only | Unblocks users immediately; everything else is polish | ✅ APPROVED |
| Hard delete (no trash) | Simplifies implementation; P0 deadline critical | ✅ APPROVED |
| Backlink update on rename | Expected behavior; prevents link rot | ✅ APPROVED |
| Context menu for all ops | Better UX than modal-only; right-click familiar | ✅ APPROVED |
| Regex backlink replacement | Simple, sufficient for vault-scoped wikilinks | ✅ APPROVED |
| Async backlink update | Large vaults need progress feedback | ✅ APPROVED |
| No templates P0 | Blank note sufficient; P1 if time | ✅ APPROVED |
| No hotkeys P0 | Power user feature; web UI works | ✅ APPROVED |
| No undo/redo P0 | Confirmation modals prevent accidents | ✅ APPROVED |

---

## Consensus Voting

- **⚙ Coder:** Supports. Scope is tight, technical feasibility clear, backlink update is complex but doable.
- **◎ Designer:** Supports. Modals + context menu are straightforward UX; can defer templates + hotkeys.
- **⚔ Contrarian:** **APPROVED.** This is MVP. Everything else is P1+.

---

## Next Steps (Round 2 Execution)

1. ✅ Coder: Review safePath() utility; identify backlink update complexity
2. ✅ Designer: Sketch context menu + modals (no code yet)
3. ✅ Contrarian: Sign off on scope; challenge any scope creep
4. 👉 **Start implementation:** Coder takes create/delete (simple) + rename (complex); Designer takes UI
5. 👉 **Daily sync:** Track blockers + backlink complexity feedback
6. 👉 **Dogfood:** Continuous validation on real vaults

---

**Status:** ✅ Locked scope. Ready for implementation.

**Signed:**
- ⚙ Coder (engineering feasibility)
- ◎ Designer (UX complexity)
- ⚔ Contrarian (scope governance)
