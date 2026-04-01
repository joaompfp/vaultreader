# VaultReader Sprint Backlog: CRUD + UX Features

**Objective:** Transform VaultReader from a read-centric browser viewer into a day-to-day Obsidian replacement with full create/delete/rename workflows and UX polish.

---

## Tier 0: Core CRUD Operations (P0 — Required)

| # | Feature | Problem | Complexity | Design Notes |
|---|---------|---------|-----------|--------------|
| **1** | **Create Note** | No way to capture new ideas | M | Ctrl+N → filename dialog → auto-edit. Creates in current folder. |
| **2** | **Delete Note** | No cleanup mechanism | M | Right-click → Delete. Moves to `.trash/` (soft delete). Confirmation required. |
| **3** | **Rename/Move Note** | Can't fix typos or reorganize | M | Double-click or right-click → inline rename. Full path shown. Detects cross-folder moves. |
| **4** | **Create Folder** | Vault structure is static | S | Right-click → New Folder. Creates in current directory. |
| **5** | **Delete Folder** | Can't clean up outdated sections | M | Right-click → Delete Folder. Shows item count. Moves to `.trash/`. |

### New API Endpoints (P0)
```
POST   /api/note?vault=X&path=Y          // Create with {raw: "..."}
DELETE /api/note?vault=X&path=Y          // Soft-delete to .trash/
POST   /api/move?vault=X&from=...&to=... // Rename/move file
POST   /api/folder?vault=X&path=Y        // Create folder
DELETE /api/folder?vault=X&path=Y        // Delete folder (recursive)
```

---

## Tier 1: UX Polish & Discoverability (P1)

| # | Feature | Problem | Complexity | Design Notes |
|---|---------|---------|-----------|--------------|
| **6** | **Context Menu** | No affordances for actions | M | Right-click + long-press mobile. Dynamic options: file/folder-aware. Keyboard accessible (Ctrl+Shift+M). |
| **7** | **Undo/Redo** | Accidental deletes aren't recoverable | L | Ctrl+Z / Ctrl+Shift+Z. Stores 50 actions. Works on all CRUD ops. |
| **8** | **Keyboard Shortcuts** | Power users can't move fast | M | Ctrl+N (new) | Ctrl+Shift+D (delete) | Ctrl+K (search) | Ctrl+R (rename) | Ctrl+Shift+C (copy link). Help overlay with `?`. |
| **9** | **Trash UI** | Deleted items are gone (or stuck in folder) | M | `.trash/` folder. Sidebar "Trash" button. Restore action. Empty trash confirmation. Auto-purge 30+ days. |
| **10** | **Filename Validation** | Special chars cause errors | S | Real-time validation. Sanitize: `< > : " / \ | ? *`. Show inline error. Suggest corrected name. |

---

## Tier 2: Power Features & Mobile UX (P2)

| # | Feature | Problem | Complexity | Design Notes |
|---|---------|---------|-----------|--------------|
| **11** | **Note Templates** | Repetitive structure boilerplate | M | `.templates/` folder. Template picker in new note dialog. Variables: `{{date}}`, `{{time}}`, `{{vault}}`. |
| **12** | **Folder Collapse/Expand** | Deep hierarchies make sidebar unusable | M | Convert to tree view. Expand/collapse icons. Persist state in localStorage. Keyboard: Arrow keys. |
| **13** | **Quick Note** | Capture ideas without thinking about org | M | Button → creates note in `.inbox/` or `Daily/` with date filename (e.g., `2026-04-01.md`). Opens in edit. |
| **14** | **Bulk Operations** | Moving multiple notes is tedious | L | Checkboxes in file list. Ctrl+A select all. Batch move. Progress bar for large ops. |
| **15** | **Favorites / Pinning** | Important notes buried in structure | S | Star icon on files. Starred items at top of list. Ctrl+Shift+S to toggle. localStorage per vault. |
| **16** | **Search Navigation** | Search finds content but not structure | S | Extend search: show files + folders. Type `>` to filter folders. Enter opens file, Shift+Enter navigates. |
| **17** | **Copy / Duplicate Note** | Can't fork structure quickly | S | Right-click → Duplicate. Creates `Note copy.md`. Opens in edit mode. |
| **18** | **Mobile Sidebar Gestures** | Touch UX for sidebar is poor | M | Swipe left = delete reveal. Long-press = context menu. Swipe right = back. Pull down = refresh. Haptic feedback. |
| **19** | **Edit Frontmatter** | Users can see YAML but not edit | M | Frontmatter bar: click fields to edit. Add field button. YAML validation inline. Same PUT endpoint. |
| **20** | **Recent Files / Navigation History** | After deep nav, users lose context | S | Recent Folders breadcrumb. Click to jump back. History stored in sessionStorage (max 20). |

---

## Tier 3: Advanced Features (P3 — Future)

| # | Feature | Notes |
|---|---------|-------|
| **21** | **Multi-Vault Operations** | Move notes between vaults. Cross-vault link resolution. |
| **22** | **Note Locking** | Lock important notes to prevent accidental edits. |
| **23** | **Collaborative Editing** | WebSocket sync for multiple users on same vault. |

---

## Implementation Timeline

### **Sprint 1 (Week 1-2): Core CRUD**
- Create Note (Ctrl+N)
- Delete Note with Trash
- Rename/Move Note
- Create/Delete Folder
- Context Menu (right-click + mobile long-press)

**Backend:** POST /api/note, DELETE /api/note, POST /api/move, POST /api/folder, DELETE /api/folder, trash handling  
**Frontend:** Dialogs, context menu, trash viewer, file list updates

---

### **Sprint 2 (Week 3): UX Polish**
- Keyboard Shortcuts (Ctrl+N, Ctrl+Z, etc.)
- Filename Validation
- Undo/Redo system
- Trash interface + restore
- (Optional) Folder collapse/expand tree

**Backend:** Action history logging (client-side), restore endpoint  
**Frontend:** Hotkey routing, validation feedback, history UI

---

### **Sprint 3 (Week 4): Power Features & Mobile**
- Note Templates
- Quick Note creation
- Mobile swipe gestures
- Bulk operations (if time)
- Favorites/pinning

---

## Design Principles

✅ **Safety:** Soft delete (trash), confirmation dialogs, undo/redo  
✅ **Velocity:** Keyboard-first (Obsidian-familiar shortcuts), no unnecessary dialogs  
✅ **Discoverability:** Context menus, tooltips, help overlay  
✅ **Mobile-First:** 44px touch targets, swipe gestures, long-press context menu  
✅ **Obsidian Parity:** Familiar shortcuts, sidebar tree, backlinks, status bar  

---

## Open Questions for Implementation Round

1. **Trash retention:** Auto-purge at 30 days or manual?
2. **Template variables:** Just `{{date}}`/`{{time}}` or support `{{frontmatter.key}}`?
3. **Undo scope:** All CRUD ops, or include edit changes too?
4. **Multi-select drag:** Allow drag-to-move, or context menu only?
5. **Hidden folders:** Icon treatment for `.trash` and `.templates`?
6. **Mobile actions:** Bottom sheet or swipe reveal for delete?
7. **Backlink updates on rename:** Re-scan entire vault or trust exact wikilinks?

---

## API Summary (All New Endpoints)

```go
// Create note with initial content
POST /api/note?vault=X&path=Y/NewNote.md
  Body: {raw: "markdown..."}
  Response: {status: "created", path: "Y/NewNote.md", mtime: ...}

// Delete note (soft delete to .trash/)
DELETE /api/note?vault=X&path=Y/Note.md
  Response: {status: "deleted", movedTo: ".trash/Note.md", timestamp: ...}

// Rename or move note
POST /api/move?vault=X&from=old/path.md&to=new/path.md
  Response: {status: "moved", newPath: "new/path.md"}

// Create folder
POST /api/folder?vault=X&path=Y/NewFolder
  Response: {status: "created", path: "Y/NewFolder"}

// Delete folder (recursive, soft delete)
DELETE /api/folder?vault=X&path=Y/OldFolder
  Response: {status: "deleted", itemsAffected: 7, movedTo: ".trash/OldFolder"}

// Restore from trash (future endpoint)
POST /api/restore?vault=X&path=.trash/OldFile.md
  Response: {status: "restored", newPath: "path/OldFile.md"}
```

---

## Notes for Engineering Teams

**Go Backend:**
- File operations: `os.Create`, `os.Remove`, `os.Rename`, `os.MkdirAll`
- Trash handling: Create `.trash/` at vault root if missing
- Path validation: Sanitize special characters before file creation
- No breaking changes to existing GET endpoints

**Alpine.js + CodeMirror:**
- Context menu: Custom floating component with pointer event listeners
- Keyboard routing: Centralized handler in app state
- Action history: Client-side stack (50 items max, in-memory)
- Filename validation: Real-time regex, suggest corrections
- Mobile gestures: Pointer events (touchstart/touchmove/touchend), CSS transforms

**No external dependencies:** Keep embedded JS only (Alpine, CodeMirror bundle). No CDN calls.

---

**Status:** Ready for Round 2 implementation. Prioritized and scoped for 3-sprint delivery. Design intent clear, API surface minimal and additive.
