# VaultReader UX Design Spec — P0 CRUD Features

**Status:** Round 2 — Ready for implementation  
**Designer:** ◎ Designer Agent  
**Timeline:** Sprint 1 (Core CRUD + UX)

---

## Overview

This document specifies the **UX flows, modal designs, keyboard shortcuts, and interaction patterns** for VaultReader's P0 (must-have) CRUD features. The goal is to make VaultReader a day-to-day Obsidian replacement with:

- ✅ Fast note creation (Ctrl+N)
- ✅ Safe deletion (trash, soft-delete, confirm)
- ✅ Rename/move without link breakage
- ✅ Folder creation & cleanup
- ✅ Context menus + mobile gesture fallbacks
- ✅ Keyboard accessibility throughout

**Constraint:** All UI uses embedded Alpine.js + vanilla CSS. No CDN, no external libs.

---

## Design Principles

1. **Safety First:** All destructive actions (delete, move) require confirmation or use soft-delete (trash).
2. **Keyboard Velocity:** Power users can do everything via hotkeys (Ctrl+N, Ctrl+Shift+D, Ctrl+R, etc.).
3. **Mobile Parity:** Every action has a touch alternative (long-press context menu, bottom-sheet modals).
4. **Discoverability:** Tooltips on hover, help overlay with `?`, breadcrumb navigation.
5. **No Dialogs on Happy Path:** Create & rename should auto-focus input, not show a dialog first.
6. **Real-time Feedback:** Inline validation, loading states, success toasts.

---

## P0 Features: UX Design

### 1. CREATE NOTE

**Problem:** Users can't capture new thoughts; must use desktop or edit filesystem.

**Keyboard Shortcut:** `Ctrl+N`  
**Context Menu:** Right-click folder → "New Note" (if in folder view)  
**Toolbar Button:** (Optional Phase 2) "+" button in toolbar

#### Flow: Keyboard (Ctrl+N)

```
User presses Ctrl+N
  ↓
1. If NO vault selected: Show toast "Select a vault first"
2. If vault selected, NO folder open:
   → Focus: "/" root folder
   → Show inline filename input in sidebar (float above file list)
3. If vault selected & folder open:
   → Default path: current folder
   → Show inline filename input in sidebar

User types filename (e.g., "My Research")
  ↓
Real-time validation:
  • Strip spaces → underscores OR use as-is (hyphens/underscores OK)
  • Disallow: < > : " / \ | ? *
  • Show inline error if invalid
  • Suggest: "MyResearch.md" if typing "My Research"

User presses ENTER
  ↓
1. POST /api/note?vault=work&path=projects/my-research.md
   Body: {raw: "# My Research\n\n"}
2. On success:
   • Close input, add to sidebar file list (animated)
   • Auto-open note in editor
   • Focus cursor in editor
3. On conflict (409):
   • Show error: "Note already exists"
   • Keep input focused for retry
4. On error (500):
   • Show error toast: "Failed to create note"
   • Input remains (can retry)

User presses ESCAPE
  ↓
Cancel input, close inline editor
```

#### Flow: Context Menu (Right-Click Folder)

```
User right-clicks folder in sidebar
  ↓
Show floating context menu (absolute position, pointer-aware)
Options:
  • 📝 New Note
  • 📁 New Folder
  • 📌 Pin Folder (future)
  • (more options below in context menu spec)

User clicks "New Note"
  ↓
Same as Ctrl+N flow (above)
```

#### Flow: Mobile (Long-Press Folder)

```
User long-presses folder in sidebar (500ms)
  ↓
Show bottom-sheet modal (slides up from bottom)
  • Same options as context menu
  • Large 44px touch targets
  • Haptic feedback (vibrate)

User taps "New Note"
  ↓
Same as Ctrl+N flow
```

#### Inline Filename Input (Visual Design)

```
<div class="new-note-input">
  <input type="text" placeholder="Note name..." autofocus>
  <span class="input-hint">Press ENTER to create, ESC to cancel</span>
  <span class="input-error" x-show="newNoteError">Note already exists</span>
</div>
```

- **Placement:** Floating above file list in sidebar, full width
- **Styling:** `.new-note-input` with light background, border glow on focus
- **Validation feedback:** Live check against existing files
- **Hint text:** "Press ENTER to create, ESC to cancel"
- **Error:** Red text inline, no modal
- **Auto-focus:** Input receives focus immediately

**API Endpoint:**
```
POST /api/note?vault=X&path=Y.md
Body: {raw: "# Title\n\n"}
Response: {status: "created", path, mtime, created: true}
Error (409): {error: "note exists"}
```

---

### 2. DELETE NOTE

**Problem:** No way to remove notes; vault accumulates clutter. Permanent deletion feared.

**Keyboard Shortcut:** `Ctrl+Shift+D` (when note is open or selected)  
**Context Menu:** Right-click file → "Delete"  
**Toolbar:** Delete button in toolbar (trash icon, when note open)

#### Flow: Keyboard (Ctrl+Shift+D)

```
User presses Ctrl+Shift+D (note must be open/active)
  ↓
Show confirmation dialog:
  Title: "Delete "{Note Name}"?"
  Message: "This moves the note to trash. You can restore it later."
  Buttons: [Cancel] [Move to Trash]
  Default focus: Cancel (safe default)

User clicks "Move to Trash"
  ↓
1. DELETE /api/note?vault=X&path=Y.md
2. On success:
   • Remove from sidebar file list (animated fade)
   • Close note editor
   • Show toast: "Note moved to trash"
   • Offer undo button in toast (for 5s)
3. On error:
   • Show error toast: "Failed to delete note"
   • Keep note open

User clicks Cancel (or presses ESC)
  ↓
Close dialog, keep note open
```

#### Flow: Context Menu (Right-Click File in Sidebar)

```
User right-clicks file in sidebar
  ↓
Show floating context menu:
  • Open
  • 🗑️ Delete
  • ✏️ Rename
  • 📋 Duplicate (future)

User clicks "Delete"
  ↓
Same confirmation dialog as Ctrl+Shift+D
```

#### Flow: Mobile (Swipe or Long-Press File)

```
Swipe Left on File Item (Mobile)
  ↓
Reveal "Delete" button (red) on right side
User taps it → Same confirmation dialog

Long-Press File (Mobile)
  ↓
Show bottom-sheet context menu → User taps "Delete"
→ Same confirmation dialog
```

#### Confirmation Dialog (Visual Design)

```html
<div class="modal-backdrop" x-show="deleteNoteModal">
  <div class="modal-content">
    <h3>Delete "{note}"?</h3>
    <p>This moves the note to trash. You can restore it later.</p>
    <div class="modal-buttons">
      <button class="btn btn-secondary" @click="deleteNoteModal = false">Cancel</button>
      <button class="btn btn-danger" @click="confirmDeleteNote()">Move to Trash</button>
    </div>
  </div>
</div>
```

- **Default focus:** Cancel button (prevent accidental deletion)
- **Danger color:** Red (#ef4444 or similar)
- **Hint:** "You can restore it from Trash later"
- **Keyboard:** Tab between buttons, Enter to confirm, ESC to cancel

**API Endpoint:**
```
DELETE /api/note?vault=X&path=Y.md
Response: {status: "deleted", movedTo: ".trash/Y.md", timestamp: 1712016000}
```

**Backend Logic:**
- Don't actually remove file; move to `.trash/` folder
- Preserve original path structure (e.g., `projects/my-note.md` → `.trash/projects_my-note.md` or `.trash/my-note_TIMESTAMP.md`)
- Rebuild index after delete (remove from allNotes)

---

### 3. RENAME / MOVE NOTE

**Problem:** Can't fix typos or reorganize notes; locked to creation path.

**Keyboard Shortcut:** `Ctrl+R` (when note is open)  
**Context Menu:** Right-click file → "Rename"  
**UX Pattern:** Inline edit in sidebar (not a modal)

#### Flow: Keyboard (Ctrl+R)

```
User presses Ctrl+R (note must be open)
  ↓
Sidebar scrolls to note, highlights it
Shows inline rename input (like new note, but with current name)

User edits name (e.g., "Old Name" → "New Name")
  ↓
Real-time validation:
  • Same rules as create (no special chars)
  • Show error if filename already exists in this folder
  • Suggest: "OldName" → "Old-Name" or "Old_Name"

User presses ENTER
  ↓
1. POST /api/move?vault=X&from=projects/old-name.md&to=projects/new-name.md
2. On success:
   • Update note title in editor
   • Update sidebar file list
   • Show toast: "Note renamed"
   • (P1: Auto-update backlinks in other notes)
3. On conflict (409):
   • Show error: "Filename already exists"
   • Keep input focused for retry
4. On error:
   • Show error toast: "Failed to rename note"

User presses ESC
  ↓
Cancel rename, close input
```

#### Flow: Context Menu (Right-Click File)

```
User right-clicks file in sidebar
  ↓
Show context menu:
  • Open
  • 🗑️ Delete
  • ✏️ Rename
  • 📋 Duplicate (future)

User clicks "Rename"
  ↓
Same inline edit flow as Ctrl+R
```

#### Flow: Double-Click File (Optional)

```
User double-clicks file in sidebar
  ↓
Same inline rename input (if not already open in editor)
```

#### Inline Rename Input (Visual Design)

```html
<div class="rename-input">
  <input type="text" x-model="renameValue" @keydown.enter="confirmRename()" autofocus>
  <span class="input-hint">Press ENTER to save, ESC to cancel</span>
  <span class="input-error" x-show="renameError" x-text="renameError"></span>
</div>
```

- **Placement:** Inline in sidebar, replace filename in list item
- **Styling:** Light background, border, focus glow
- **Validation:** Real-time check for duplicates in current folder
- **Error:** Red text, no modal
- **Auto-focus:** Cursor at end of filename (before `.md`)

**API Endpoint:**
```
POST /api/move?vault=X
Body: {from: "old/path.md", to: "new/path.md"}
Response: {status: "moved", newPath: "new/path.md"}
Error (409): {error: "target already exists"}
```

**Backend Logic:**
- Validate both paths (no ../, etc.)
- os.Rename old → new
- Rebuild index (update wikilink references)
- (P1) Scan all notes in vault, update wikilinks: `[[old-path]]` → `[[new-path]]`

---

### 4. CREATE FOLDER

**Problem:** Can't organize notes; forced flat structure.

**Keyboard Shortcut:** `Ctrl+Shift+N` (when folder is open or at root)  
**Context Menu:** Right-click folder → "New Folder"

#### Flow: Keyboard (Ctrl+Shift+N)

```
User presses Ctrl+Shift+N
  ↓
1. If NO vault selected: Show toast "Select a vault first"
2. If vault selected:
   → Show inline folder name input in sidebar

User types folder name (e.g., "Projects")
  ↓
Validation:
  • Same rules as note (no special chars)
  • Real-time check for duplicates in current folder
  • Suggest: "My Projects" → "my-projects" or "my_projects"

User presses ENTER
  ↓
1. POST /api/folder?vault=work&path=projects/subfolder
2. On success:
   • Add to sidebar file list (with folder icon)
   • Keep input focused (allow chaining multiple folder creates)
   • Or close input and auto-enter new folder
3. On conflict (409):
   • Show error: "Folder already exists"
   • Keep input focused for retry

User presses ESC
  ↓
Cancel input, close
```

#### Flow: Context Menu (Right-Click Folder)

```
User right-clicks folder in sidebar
  ↓
Show context menu:
  • 📝 New Note
  • 📁 New Folder
  • 🗑️ Delete Folder
  • ✏️ Rename Folder (future)
  • (more options)

User clicks "New Folder"
  ↓
Same as Ctrl+Shift+N flow
```

#### Inline Folder Input (Visual Design)

```html
<div class="new-folder-input">
  <span class="input-icon">📁</span>
  <input type="text" placeholder="Folder name..." autofocus>
  <span class="input-hint">Press ENTER to create</span>
  <span class="input-error" x-show="newFolderError">Folder already exists</span>
</div>
```

- **Icon:** Folder emoji or SVG
- **Styling:** Similar to new note input
- **Placement:** Inline in file list
- **Auto-focus:** Receives focus immediately

**API Endpoint:**
```
POST /api/folder?vault=X&path=projects/newfolder
Response: {status: "created", path: "projects/newfolder"}
Error (409): {error: "folder exists"}
```

**Backend Logic:**
- os.MkdirAll(fullPath)
- Rebuild tree after create

---

### 5. DELETE FOLDER

**Problem:** Can't remove empty or obsolete folders; vault structure becomes junk.

**Context Menu:** Right-click folder → "Delete Folder"  
**Confirmation:** Shows item count

#### Flow: Context Menu (Right-Click Folder)

```
User right-clicks folder in sidebar
  ↓
Context menu shows "🗑️ Delete Folder"

User clicks "Delete Folder"
  ↓
Show confirmation dialog:
  Title: "Delete "{Folder Name}"?"
  Message: "This folder contains X notes and Y subfolders."
  Message: "All items will be moved to trash."
  Buttons: [Cancel] [Move to Trash]
  Default focus: Cancel

User clicks "Move to Trash"
  ↓
1. DELETE /api/folder?vault=X&path=projects/archived
2. On success:
   • Remove from sidebar (animated)
   • If folder was open, navigate up to parent
   • Show toast: "Folder moved to trash (X items)"
3. On error:
   • Show error toast

User clicks Cancel (or ESC)
  ↓
Close dialog, keep folder open
```

#### Confirmation Dialog with Item Count

```html
<div class="modal-backdrop" x-show="deleteFolderModal">
  <div class="modal-content">
    <h3>Delete "{folder}"?</h3>
    <p x-text="'This folder contains ' + folderStats.notes + ' notes and ' + folderStats.folders + ' subfolders.'"></p>
    <p>All items will be moved to trash. You can restore them later.</p>
    <div class="modal-buttons">
      <button class="btn btn-secondary" @click="deleteFolderModal = false">Cancel</button>
      <button class="btn btn-danger" @click="confirmDeleteFolder()">Move to Trash</button>
    </div>
  </div>
</div>
```

- **Item count:** Show summary before delete
- **Reassurance:** Mention trash recovery option
- **Safe default:** Cancel is focused

**API Endpoint:**
```
DELETE /api/folder?vault=X&path=projects/archived
Response: {status: "deleted", movedTo: ".trash/archived", itemsAffected: 7}
```

**Backend Logic:**
- Recursively move folder & contents to `.trash/`
- Count items before delete (for UI feedback)
- Rebuild index after delete

---

## Context Menus & Hover Interactions

### Right-Click Context Menu (Desktop)

**Trigger:** Right-click on file or folder in sidebar

**Menu Structure:**
```
File items:
  └─ Open
  ├─ 🗑️ Delete
  ├─ ✏️ Rename
  ├─ 📋 Duplicate (future)
  └─ 📌 Pin (future)

Folder items:
  ├─ 📝 New Note
  ├─ 📁 New Folder
  ├─ 🗑️ Delete Folder
  ├─ ✏️ Rename (future)
  └─ 📌 Pin (future)
```

**Styling:**
- Position: `absolute`, relative to cursor or element
- Background: `var(--surface)` (elevated, light)
- Border: 1px `var(--border-light)`
- Shadow: subtle drop shadow
- Rounded corners: 4px
- Padding: 4px 0 (items inside have padding)
- Each item: 32px height, 44px minimum touch target
- Hover: background highlight
- Icons: 16px, left-aligned, emoji or SVG

**Keyboard Navigation (future):**
- Arrow up/down: Navigate menu items
- Enter: Select item
- ESC: Close menu

**Positioning Strategy:**
- If context menu would overflow right edge, position left of cursor
- If overflow bottom, position above cursor
- Keep 8px padding from viewport edges

### Long-Press Context Menu (Mobile)

**Trigger:** Long-press (500ms) on file or folder

**Behavior:**
- Show bottom-sheet modal (slides up from bottom)
- Large 44px touch targets
- Haptic feedback (vibrate 50ms)
- Same menu options as right-click

**Styling:**
```
.context-menu-mobile {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  background: var(--surface);
  border-radius: 12px 12px 0 0;
  padding: 12px 0;
  animation: slideUp 200ms ease-out;
  box-shadow: 0 -4px 16px rgba(0,0,0,0.1);
}

.context-menu-item {
  padding: 12px 16px;
  height: 44px;
  display: flex;
  align-items: center;
  gap: 12px;
  border-bottom: 1px solid var(--border);
  cursor: pointer;
}

.context-menu-item:active {
  background: var(--highlight);
}
```

**Dismissal:**
- Tap outside (scrim)
- Swipe down
- Select an option
- ESC key

---

## Keyboard Shortcuts Summary

| Action | Shortcut | Fallback | Context |
|--------|----------|----------|---------|
| New Note | `Ctrl+N` | (mobile: long-press folder) | Vault selected |
| New Folder | `Ctrl+Shift+N` | (mobile: long-press folder) | Folder open |
| Delete Note | `Ctrl+Shift+D` | (right-click file) | Note open |
| Rename Note | `Ctrl+R` | (right-click file) | Note open |
| Search | `Ctrl+K` | (search button in toolbar) | Always |
| Copy Link | `Ctrl+Shift+C` | (copy button in toolbar) | Note open |
| Toggle Sidebar | `Ctrl+B` | (menu button) | Always |
| Undo | `Ctrl+Z` | (future: menu) | After CRUD |
| Redo | `Ctrl+Shift+Z` | (future: menu) | After undo |
| Help | `?` | (menu) | Always |

**Implementation Notes:**
- Use `@keydown.window` on the app root to capture shortcuts
- Check context (note open, folder selected, etc.) before executing
- Prevent default browser behavior (Ctrl+N usually opens new window)
- On mobile: Show toast hints for shortcuts, or map to buttons

---

## Mobile UX Special Cases

### Small Screen Adaptations (< 700px)

1. **Sidebar Collapse:** Sidebar collapses on mobile by default
   - Menu button (☰) toggles sidebar
   - Clicking a file opens it, auto-hides sidebar

2. **Context Menus:** Use bottom-sheet instead of floating menus
   - Larger touch targets (44px min)
   - Easier to reach on small screens

3. **Input Fields:** Inline rename/create should match keyboard size
   - Textarea if editing long notes
   - Regular input for filenames

4. **Confirmation Dialogs:** Full-width modals on mobile
   - Buttons stacked vertically
   - 44px minimum tap targets

5. **Swipe Gestures:**
   - Swipe left on file: Reveal delete button
   - Swipe right: Back/close sidebar
   - Long-press: Context menu

### Touch Affordances

- **44px minimum touch targets** (button, menu item, file list item)
- **Haptic feedback** on long-press (navigator.vibrate(50))
- **No hover states** — use background color on active/pressed states
- **Visual feedback:** Ripple or highlight when tapped
- **Enough spacing** between touch targets to prevent mis-taps

---

## Error Handling & Validation

### Filename Validation Rules

**Allowed:**
- Letters, numbers
- Hyphens, underscores, spaces
- Dots (but not leading or trailing; `.md` added by system)

**Disallowed:**
- Special chars: `< > : " / \ | ? *`
- Leading/trailing spaces
- Names reserved by OS: `CON`, `PRN`, `AUX`, `NUL` (Windows)

**Real-Time Feedback:**
```html
<div class="validation-feedback">
  <input x-model="filename" @input="validateFilename()" />
  <template x-if="!isValidFilename">
    <span class="error">Invalid: <span x-text="errorMessage"></span></span>
  </template>
  <template x-if="!isUniqueFilename">
    <span class="warning">File already exists: <strong x-text="suggestedName"></strong></span>
  </template>
</div>
```

**Error Messages:**
- "Invalid character: `:`"
- "Filename too long (max 200 characters)"
- "File already exists in this folder"
- "Reserved name (Windows): CON"

**Suggestions:**
- "My Research" → "My-Research" or "My_Research"
- "file:draft" → "file-draft"
- "my note.md" → "my-note" (system adds .md)

### API Error Responses

**409 Conflict:**
```json
{
  "error": "file exists",
  "path": "projects/my-research.md",
  "suggestion": "my-research-2.md"
}
```

**400 Bad Request:**
```json
{
  "error": "invalid path",
  "reason": "contains .."
}
```

**500 Server Error:**
```json
{
  "error": "internal server error",
  "message": "failed to write file"
}
```

**Client-Side Toast (with auto-dismiss in 5s):**
```
✅ "Note created"
❌ "Failed to create note" (show retry?)
⚠️ "Note already exists"
```

---

## Loading & Animation States

### Create/Delete/Move Operations

**During operation:**
- Show loading spinner next to item in sidebar
- Disable input/buttons
- Show "Creating..." in tooltip or status bar

**After operation:**
```
Success:
  1. Animate item into/out of sidebar (fade + slide)
  2. Show success toast (2s auto-dismiss)
  3. Update tree, focus next action

Error:
  1. Keep modal/input open
  2. Show error message inline
  3. Allow user to retry
```

**Example: Creating a note:**
```
[📝 Creating note...] (spinner beside name in list)
→ (after 300ms) Note appears in list
→ Toast: "✅ Note created"
→ Auto-open note in editor
```

---

## Accessibility Checklist

- ✅ **Keyboard navigation:** All actions accessible via keyboard
- ✅ **Focus management:** Auto-focus inputs, trap focus in modals
- ✅ **ARIA labels:** Use `aria-label` on icon buttons
- ✅ **Semantic HTML:** `<button>`, `<input>`, `<dialog>` as appropriate
- ✅ **Color contrast:** All text meets WCAG AA (4.5:1 for normal text)
- ✅ **Mobile touch:** 44px minimum targets, haptic feedback
- ✅ **Status messages:** Use `aria-live="polite"` for toasts
- ✅ **Screen readers:** Describe actions, not just "Delete"
  - "Delete note 'My Research'" not just "Delete"
- ✅ **Escape key:** Always closes modals/inputs

---

## Testing Checklist (Implementation Validation)

- [ ] Create note: Ctrl+N, context menu, auto-opens in editor
- [ ] Create note: Validation catches invalid filenames in real-time
- [ ] Create note: 409 conflict shows suggestion
- [ ] Delete note: Ctrl+Shift+D shows confirmation
- [ ] Delete note: Note moved to `.trash/` (verify filesystem)
- [ ] Delete note: Sidebar updates, editor closes
- [ ] Rename note: Ctrl+R inline edit, validates, updates everywhere
- [ ] Rename note: 409 conflict shows duplicate error
- [ ] Create folder: Ctrl+Shift+N, shows in sidebar
- [ ] Delete folder: Shows item count, moves to trash
- [ ] Context menu: Right-click shows correct options
- [ ] Context menu: Mobile long-press works
- [ ] Keyboard: All shortcuts work, prevent default browser behavior
- [ ] Mobile: Bottom-sheet modals on small screens
- [ ] Mobile: Swipe reveal delete (file list item)
- [ ] Focus: Auto-focus inputs, trap in modals
- [ ] Toast: Error/success messages auto-dismiss in 5s
- [ ] Undo toast: "Delete X undone" with undo button (future)

---

## Notes for Round 2 Implementation

### Frontend (Alpine.js) Tasks
1. Add state variables to vaultApp():
   - `newNoteInput`, `newNoteError`
   - `renameInput`, `renameTarget`, `renameError`
   - `deleteNoteModal`, `deleteFolderModal`
   - `newFolderInput`, `newFolderError`
   - `contextMenu` (position, options, target)
   - `toast` (message, type, duration)

2. Implement handlers:
   - `createNote()`, `renameNote()`, `deleteNote()`, `createFolder()`, `deleteFolder()`
   - Context menu: `showContextMenu(type, item, event)`, `hideContextMenu()`
   - Validation: `validateFilename(name)`, `checkDuplicate(name, path)`
   - Toast: `showToast(msg, type)`, auto-dismiss

3. Add keyboard routing:
   - Listen for `Ctrl+N`, `Ctrl+Shift+N`, `Ctrl+R`, `Ctrl+Shift+D`
   - Route to correct handler based on context

4. Update file list items:
   - Add `@contextmenu.prevent` for right-click
   - Add long-press detection (touchstart timer)
   - Add swipe-left reveal for delete (mobile)

5. CSS:
   - `.context-menu` (floating), `.context-menu-mobile` (bottom-sheet)
   - `.new-note-input`, `.rename-input`, `.new-folder-input`
   - Modal styles: `.modal-backdrop`, `.modal-content`
   - Toast styles: `.toast-container`, `.toast.success`, `.toast.error`
   - Error/warning inline: `.input-error`, `.input-warning`

### Backend (Go) Tasks
1. **Path validation utility:** Sanitize, reject `../`, special chars, etc.
2. **POST /api/note:** Create with initial body, rebuild index
3. **DELETE /api/note:** Soft-delete to `.trash/`, rebuild index
4. **POST /api/move:** Rename/move, rebuild index, (P1: update wikilinks)
5. **POST /api/folder:** Create with MkdirAll
6. **DELETE /api/folder:** Recursively move to `.trash/`
7. **Trash implementation:** `.trash/` folder with timestamps or original paths

### Testing Recommendations
- Unit tests for path validation (no `../`, no special chars)
- Integration tests for CRUD endpoints with malformed input
- QA: Test each keyboard shortcut on desktop and mobile
- QA: Test context menus on different screen sizes
- QA: Verify soft-delete: can files in `.trash/` be restored?

---

## Future Phases (P1+)

- **Undo/Redo:** Client-side action stack, Ctrl+Z/Ctrl+Shift+Z
- **Trash UI:** Sidebar "Trash" section, restore button
- **Auto-Update Wikilinks:** After rename, scan vault, update references
- **Bulk Operations:** Multi-select, batch move/delete
- **Folder Collapse:** Tree view instead of flat list
- **Templates:** `.templates/` folder, pick template on create
- **Quick Note:** Auto-create dated note (daily log pattern)
- **Favorites:** Star notes, appear at top of list
- **Keyboard Help:** Overlay with `?` showing all shortcuts

---

**End of Design Spec. Ready for implementation in Round 2.**
