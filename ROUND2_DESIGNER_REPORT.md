# Round 2 — Designer Agent Report

**Status:** Complete  
**Responsible Agent:** ◎ Designer  
**Deliverables:** Feature backlog, UX design spec, implementation guidance

---

## Executive Summary

The Designer has completed the **planning and UX specification phase** for VaultReader's P0 CRUD features. All critical workflows are designed, interaction patterns documented, and implementation guidance provided to the Coder. The backlog is ready for immediate development.

**Key Achievement:** Transformed abstract "create/delete/rename" requirements into concrete, keyboard-friendly, mobile-aware UX with real-time validation, error handling, and accessibility.

---

## Deliverables

### 1. SPRINT_BACKLOG.md ✅
- **5 Tiers of features:** P0 (5 core CRUD), P1 (12 QoL features), P2 (3 advanced), P3 (future)
- **Feature table:** Name, problem, complexity, design notes
- **API endpoint summary:** POST /api/note, DELETE /api/note, POST /api/move, POST /api/folder, DELETE /api/folder
- **Design principles:** Safety (trash), velocity (keyboard), discoverability, mobile parity, Obsidian familiarity
- **3-sprint timeline:** Week 1-2 (core CRUD), Week 3 (UX polish), Week 4 (power features)
- **Open questions:** Trash retention, template variables, undo scope, etc.

### 2. DESIGN_SPEC.md ✅
**Comprehensive UX specification (850 lines) covering:**

#### P0 Features with Detailed Flows
1. **Create Note:**
   - Keyboard (Ctrl+N): Auto-focus inline input in sidebar
   - Context menu: Right-click folder → "New Note"
   - Mobile: Long-press, bottom-sheet modal
   - Validation: Real-time check for special chars, duplicates
   - Feedback: Inline error or success toast

2. **Delete Note:**
   - Keyboard (Ctrl+Shift+D): Confirmation dialog with safe default
   - Context menu: Right-click → "Delete"
   - Mobile: Swipe-left reveal or long-press menu
   - Safety: Soft-delete to `.trash/`, undo toast for 5s
   - Feedback: Toast with item count

3. **Rename Note:**
   - Keyboard (Ctrl+R): Inline edit in sidebar
   - Context menu: Right-click → "Rename"
   - Validation: Same as create + check duplicates
   - Feedback: Inline error, no modal on happy path

4. **Create Folder:**
   - Keyboard (Ctrl+Shift+N): Inline input
   - Context menu: Right-click → "New Folder"
   - Validation: Real-time, suggest safe alternatives
   - Chaining: Can create multiple folders sequentially

5. **Delete Folder:**
   - Context menu only (right-click folder)
   - Confirmation: Shows item count, messages about recovery
   - Behavior: Recursively move to `.trash/`

#### Context Menus & Interaction Patterns
- **Desktop:** Right-click floating menu with smart positioning
- **Mobile:** Long-press (500ms) → bottom-sheet with 44px targets
- **Menu structure:** File vs folder specific options
- **Keyboard navigation:** Arrow keys, Enter, ESC (future polish)

#### Keyboard Shortcuts Summary
| Action | Shortcut | Fallback | Context |
|--------|----------|----------|---------|
| New Note | Ctrl+N | context menu | vault selected |
| New Folder | Ctrl+Shift+N | context menu | folder open |
| Delete Note | Ctrl+Shift+D | context menu | note open |
| Rename Note | Ctrl+R | context menu | note open |
| Copy Link | Ctrl+Shift+C | button | note open |
| Search | Ctrl+K | button | always |
| Toggle Sidebar | Ctrl+B | button | always |

#### Mobile UX Considerations
- **44px minimum touch targets**
- **Haptic feedback** on long-press (navigator.vibrate)
- **Bottom-sheet modals** instead of floating menus
- **Swipe gestures:** Left=delete, right=back
- **Sidebar collapse** on screens < 700px
- **Vertical button layout** in modals

#### Validation & Error Handling
- **Real-time feedback:** Invalid chars detected as user types
- **Helpful suggestions:** "My Research" → "My-Research"
- **Duplicate detection:** Check against existing files in folder
- **API error mapping:** 409 Conflict → show suggestion, 400 Bad → explain why
- **Toast notifications:** Auto-dismiss in 5s, allow retry/undo

#### Accessibility Checklist
- ✅ All actions keyboard-accessible
- ✅ Focus management & modal trapping
- ✅ ARIA labels on buttons
- ✅ Color contrast (WCAG AA)
- ✅ 44px touch targets (mobile)
- ✅ Escape key closes everything
- ✅ Semantic HTML (`<button>`, `<input>`)
- ✅ Screen reader friendly

#### Implementation Guidance for Coder
**Alpine.js state variables needed:**
- `newNoteInput`, `newNoteError`
- `renameInput`, `renameTarget`, `renameError`
- `deleteNoteModal`, `deleteFolderModal`
- `newFolderInput`, `newFolderError`
- `contextMenu` (position, options, target)
- `toast` (message, type, duration)

**Handlers to implement:**
- `createNote()`, `renameNote()`, `deleteNote()`, `createFolder()`, `deleteFolder()`
- `showContextMenu()`, `hideContextMenu()`
- `validateFilename()`, `checkDuplicate()`
- `showToast()` with auto-dismiss

**CSS components needed:**
- `.context-menu`, `.context-menu-mobile`
- `.new-note-input`, `.rename-input`, `.new-folder-input`
- `.modal-backdrop`, `.modal-content`
- `.toast-container`, `.toast.success`, `.toast.error`
- `.input-error`, `.input-warning`

#### Testing Checklist
20+ test cases covering:
- Create/delete/rename success paths
- Validation errors & suggestions
- Context menu positioning
- Keyboard shortcuts
- Mobile interactions
- Focus management
- Toast auto-dismiss

#### Backend Guidance for Coder
**New Go functions needed:**
- Path validation utility (no `../`, special chars, length limits)
- POST handler: create note with initial body
- DELETE handler: soft-delete to `.trash/`
- POST handler: rename/move with index rebuild
- POST handler: create folder with MkdirAll
- DELETE handler: recursive delete to `.trash/`

**Key constraints:**
- No breaking changes to existing API
- Query string format only (not REST resource model)
- Soft-delete: preserve original path structure
- Index rebuild after every mutation
- Error responses with helpful suggestions

---

## UX Design Decisions & Rationale

### 1. Soft-Delete (Trash) over Permanent Deletion
**Decision:** DELETE moves files to `.trash/`, not os.Remove  
**Rationale:** 
- Users fear accidents; soft-delete is industry standard (Gmail, Obsidian)
- Enables future "Restore from Trash" feature
- No loss of data on accidental deletion
- Simple to implement: os.Rename instead of os.Remove

### 2. Inline Create/Rename Inputs over Modal Dialogs
**Decision:** No popup dialogs on happy path; auto-focus inline input  
**Rationale:**
- Faster workflow (typing immediately, no dialog click-to-focus delay)
- Familiar to users of VS Code, Obsidian, file explorers
- Mobile-friendly: less modal layering, inline validation visible
- Real-time feedback visible in context (see filename in list)

### 3. Context Menu First, Toolbar Buttons Second
**Decision:** Right-click menu is primary; toolbar delete/rename buttons optional (Phase 2)  
**Rationale:**
- Reduces toolbar clutter (already has preview/edit toggle, backlinks, search)
- Follows convention: file managers, web UIs all use right-click for actions
- Mobile long-press is natural fallback (similar to iOS/Android)
- Can add toolbar buttons later if usage data warrants

### 4. Keyboard Shortcuts with Mnemonic Keys
**Decision:** Ctrl+N (new), Ctrl+R (rename), Ctrl+Shift+D (delete)  
**Rationale:**
- Matches Obsidian conventions where applicable (Ctrl+N)
- Shift modifier signals "destructive" (delete, redo)
- Easy to remember (first letters, modified with Ctrl/Shift)
- Accessible without reaching for meta keys (no Cmd+Option+Shift combos)

### 5. Real-Time Filename Validation (No Pre-Submit Modal)
**Decision:** Inline error messages as user types, not "fix this dialog"  
**Rationale:**
- Faster feedback (user sees issue immediately)
- Less modal overhead (small screen friendly)
- Shows suggestion inline (e.g., "My Research" → "My-Research")
- Mobile users benefit: error messages visible without scrolling

### 6. Mobile Bottom-Sheet over Floating Context Menu
**Decision:** Long-press triggers bottom-sheet, not floating menu  
**Rationale:**
- Larger touch targets (full width, 44px buttons)
- Easier to reach (bottom of screen, near thumb area)
- Dismissible by swiping down or tapping scrim
- Better for small screens (no positioning headaches)

### 7. Confirmation on Delete, Not on Rename/Create
**Decision:** Delete requires modal confirm; rename/create use auto-focus validation  
**Rationale:**
- Delete is irreversible (until P1 undo), so confirm is crucial
- Rename/create errors are caught by validation (duplicate check, special chars)
- User can press Escape to undo if they catch mistake immediately
- Reduces modal spam; only confirms for destructive actions

### 8. Action-Focused Shortcuts (Ctrl+Shift+D) not Item-Focused (Delete Key)
**Decision:** Use Ctrl+Shift+D for delete instead of Delete key  
**Rationale:**
- Prevent accidental deletion from single keystroke
- Delete key often used in search inputs to clear
- Ctrl+Shift modifier signals destructive action
- Accessibility: user must intentionally press modifier combo

---

## Design Principles Applied

| Principle | How Applied in P0 Features |
|-----------|----------------------------|
| **Safety First** | Soft-delete, confirm on delete, no accidental actions from single keys |
| **Keyboard Velocity** | Every action reachable via Ctrl+key, inline editing no modal delay |
| **Mobile Parity** | 44px targets, bottom-sheet, long-press, haptic feedback, swipe gestures |
| **Discoverability** | Context menus, tooltips, help overlay (future), breadcrumbs |
| **Real-Time Feedback** | Inline validation, toast toasts, loading spinners, auto-dismiss feedback |
| **Obsidian Familiarity** | Similar shortcuts (Ctrl+N), sidebar tree, backlinks, status bar (existing) |
| **Minimal Scope** | P0 is pure CRUD + UX; P1 adds polish (undo, bulk ops), P2 adds power (templates, favorites) |

---

## Open Design Questions (For Contrarian & Coder)

1. **Trash Retention:** Auto-purge at 30 days or manual empty-trash button?
   - **Designer's lean:** Manual (let users control); easy to add auto-purge later

2. **Rename Backlink Updates:** Scan entire vault on rename, or trust exact wikilinks?
   - **Designer's lean:** P1 feature; P0 is just rename the file, links still work if using [[Note Name]] pattern

3. **Undo Scope:** Include edit changes (CodeMirror) or just CRUD?
   - **Designer's lean:** P1; for now, each CRUD action is distinct. Edit undo handled by CodeMirror

4. **Folder Rename:** Support `Ctrl+R` on folder? Or rename via context menu only?
   - **Designer's lean:** Context menu only for P0. Folder rename is less critical than note rename

5. **Create in Root vs Current Folder:** Ctrl+N always creates in current folder? Or prompt?
   - **Designer's lean:** Create in current folder. If no folder open, default to root. No prompts on happy path

6. **Swipe Sensitivity:** How long should long-press be? 300ms, 500ms, 1000ms?
   - **Designer's lean:** 500ms (iOS standard), gives users time to cancel by dragging away

7. **Toast Position:** Bottom-left, bottom-right, top-center?
   - **Designer's lean:** Bottom-right (doesn't overlap sidebar on desktop, visible on mobile)

8. **Inline Validation Debounce:** Check duplicates after every keystroke or debounced?
   - **Designer's lean:** Debounce 200ms to avoid thrashing network checks (even though local)

---

## Next Steps (Handoff to Coder & Contrarian)

### For Coder (Go Backend + Alpine.js)
1. Review DESIGN_SPEC.md, focus on "Implementation Guidance" sections
2. Implement Go CRUD handlers with path validation
3. Build Alpine.js modals, inputs, context menus in static/index.html
4. Keyboard routing (centralized hotkey handler in vaultApp())
5. Integration test each flow: create → rename → delete → open

### For Contrarian
1. Challenge scope creep: Are all P0 features needed for MVP? (Designer says yes)
2. Review API surface: Are 5 new endpoints minimal? (Designer optimized for reuse)
3. Check for over-engineering: Is soft-delete too complex for P0? (Designer: yes, worth the safety)
4. Mobile UX: Are bottom-sheet + long-press sufficient? (Designer: yes, no drag-drop needed P0)
5. Performance: Will tree rebuild on every mutation be fast enough? (Need to profile)

### For Designer (Next Round, P1+)
- Undo/redo visual design and state management
- Trash UI & restore flow
- Keyboard help overlay (?)
- Bulk operations UI (checkboxes, progress bar)
- Template system & variable substitution

---

## Files Delivered

1. **SPRINT_BACKLOG.md** (174 lines)
   - Full feature backlog: P0/P1/P2/P3 features
   - Timelines, API summary, design principles
   - Open questions for implementation

2. **DESIGN_SPEC.md** (850+ lines)
   - Detailed UX flows for all P0 features
   - Context menus, keyboard shortcuts, mobile patterns
   - Validation rules, error handling, accessibility checklist
   - Implementation guidance for all technologies
   - Testing checklist (20+ test cases)

3. **ROUND2_DESIGNER_REPORT.md** (this file)
   - Executive summary of deliverables
   - Design decisions & rationale
   - Handoff to Coder & Contrarian
   - Open questions for debate

---

## Acceptance Criteria for Round 2 (Designer's Work)

✅ Feature backlog written with clear priorities  
✅ P0 features fully specified (not just names, but flows)  
✅ API contracts defined (endpoints, request/response shapes)  
✅ Keyboard shortcuts documented and justified  
✅ Mobile UX patterns specified (long-press, swipe, bottom-sheet)  
✅ Validation rules defined (special chars, duplicates, lengths)  
✅ Error handling patterns designed (409 conflicts, 400 bad input, 500 errors)  
✅ Accessibility checklist completed (WCAG, ARIA, focus mgmt)  
✅ Implementation guidance provided (state variables, handlers, CSS)  
✅ Testing checklist created (20+ test cases)  
✅ Design decisions documented with rationale  
✅ Open questions logged for team discussion  

**Status:** ALL CRITERIA MET ✅

---

## Designer's Confidence & Risks

### What We're Confident About
- **Keyboard shortcuts:** Ctrl+N, Ctrl+R, Ctrl+Shift+D are intuitive and safe
- **Soft-delete:** Right choice; enables recovery, simple implementation
- **Inline validation:** Better UX than modal dialogs; fits Alpine.js well
- **Context menus:** Standard pattern; both desktop & mobile covered
- **Mobile interactions:** Long-press + bottom-sheet is proven pattern (iOS, Android)

### Risks & Mitigation

| Risk | Severity | Mitigation |
|------|----------|-----------|
| **Path traversal:** Malicious `../` paths break vault | High | Strict path validation in Go; whitelist safe chars |
| **Index sync:** Tree out-of-sync after CRUD | High | Rebuild index on every mutation (may need profiling) |
| **Backlink rot:** Rename breaks [[OldName]] wikilinks | Medium | P1 feature; P0 documents that user must update manually |
| **Trash bloat:** `.trash/` grows unbounded | Medium | P1 auto-purge or "Empty Trash" button |
| **Mobile UX:** Bottom-sheet might not fit small screens | Low | CSS media query, adjust padding/font if needed |
| **Keyboard conflicts:** Ctrl+N used by browser | Medium | Prevent default on hotkey handler; document for users |
| **Undo scope creep:** Users expect undo for everything | Medium | Start with CRUD only; include note edits in P1 |

---

## Conclusion

The Designer has delivered a **complete, implementable specification** for VaultReader's P0 CRUD features. Every interaction is documented, every error case addressed, and every platform (desktop, mobile, tablet) considered.

The spec balances **speed** (keyboard-first, no dialog delays) with **safety** (confirm on delete, soft-delete, validation). It's **Obsidian-familiar** (Ctrl+N, sidebar tree) while being **web-native** (no external dependencies, embedded JS only).

**The Coder can now build with confidence.** The Contrarian can challenge scope knowing the rationale. And users will have a powerful, friendly note-taking tool.

---

**Ready for Round 2 Implementation.** 🚀

*— Designer Agent, VaultReader Council*
