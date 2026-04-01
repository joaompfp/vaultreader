# Contrarian's Round 2 Review — Scope Veto

**Date:** April 1, 2026  
**Agent:** ⚔ Contrarian  
**Status:** CONSENSUS ON P0 SCOPE (hard cutoff declared)

---

## What Happened in Round 1

Both Coder and Designer produced thorough, professional backlogs:

- **Coder's SPRINT_BACKLOG.md** (44–50 hours): CRUD (Week 1) + Hotkeys + Quick Switcher + Tags (Week 2) + Polish (Week 3) + Buffer (Week 4)
- **Designer's SPRINT_BACKLOG.md** (20 features in Tier 0-2): CRUD + Soft delete + Undo/Redo + Templates + Bulk ops + Mobile gestures + Favorites + Frontmatter editor + etc.

**Both are too ambitious.** Neither agent challenged the other enough.

---

## The Contrarian Challenge

### Question 1: What's the **true MVP** for VaultReader?

**Answer:** Users can **create, delete, and rename notes**. Everything else is polish.

### Question 2: What should be in P0?

**Only what's technically necessary to unblock the user from editing their vault.**

### Question 3: What's scope creep?

Anything that can be deferred to P1 without blocking core workflows:
- ✅ Undo/Redo (user can use Ctrl+Z in CodeMirror; not app-level)
- ✅ Hotkeys (power user feature; web UI works without them)
- ✅ Quick Switcher (fuzzy search; nice-to-have)
- ✅ Templates (blank note is sufficient to start)
- ✅ Trash/soft-delete (adds 2–3 hours complexity + file mgmt + GC cron)
- ✅ Tags (nice-to-have; P1 easy addition)
- ✅ Bulk operations (rare, complex state management)
- ✅ Mobile gestures (web UI works fine as-is)
- ✅ Favorites/pinning (pure vanity)
- ✅ Frontmatter editor (metadata rarely edited via web)

---

## The Hard Cut: P0 Scope (Final)

**5 endpoints. 5 modals. 16–20 hours. Done.**

1. POST /api/note — Create note ✓
2. DELETE /api/note — Delete note (hard delete, no trash) ✓
3. POST /api/move — Rename/move note + update backlinks ✓
4. POST /api/folder — Create folder ✓
5. DELETE /api/folder — Delete folder (with recursive flag) ✓

**Frontend:** Context menu (right-click) + 5 modals.  
**Backend:** ~250 LOC new + index utilities.  
**Risk:** Backlink update is complex but doable (~6 hours).  
**Timeline:** 16–20 hours implementation + 4–6 hours dogfood.

---

## What I'm Rejecting (P1+)

### Hard Delete (not Soft Delete)

**Coder's issue:** Soft delete adds `.trash/` folder management + GC cron + restore endpoint.  
**My stance:** P0 is hard delete. Use confirmation modal to prevent accidents. P1 can add trash if needed.  
**Why:** 2–3 hours savings. We can always add trash later without breaking the API.

### No Hotkeys (Ctrl+N, Ctrl+D, etc.) in P0

**Designer's assumption:** Obsidian users expect keyboard shortcuts.  
**My stance:** P0 is mouse/touch UX (modals + context menu). Hotkeys are P1 polish.  
**Why:** Web app works fine without them. Power users can wait 1 sprint. Mouse users get immediate value.

### No Undo/Redo in P0

**Designer's request:** Accidental deletes aren't recoverable.  
**My stance:** Confirmation modals prevent accidents. CodeMirror's native Ctrl+Z handles edit undo separately. Full undo/redo is P1.  
**Why:** Adds state machine complexity. Modals are sufficient.

### No Templates in P0

**Designer's assumption:** Users need boilerplate.  
**My stance:** Blank note is sufficient. Templates are P1.  
**Why:** Every vault has different templates. P0 should focus on core CRUD, not customization.

### No Bulk Operations in P0

**Designer's idea:** Move/delete multiple notes.  
**My stance:** Single-file operations only. Bulk is P1.  
**Why:** Complex state management. Rare workflow. Can be added later.

### No Mobile Gestures in P0

**Designer's concern:** Mobile UX needs swipe delete, long-press menu.  
**My stance:** Web UI works fine. Modals handle everything. Touch users get same UX as desktop.  
**Why:** Native assumptions are over-engineered. Web works.

### No Tag Filtering in P0

**Coder's addition:** Extract tags during index build, filter in sidebar.  
**My stance:** P1. Core vault operations don't depend on tags.  
**Why:** Zero impact on MVP. Sidebar tag section is polish, not blocking.

---

## The Backlink Update Complexity (The Real Risk)

This is where the sprint could derail:

**Operation:** User renames `FooBar.md` → `FooBaz.md`.  
**Expected outcome:** All wikilinks `[[FooBar]]` → `[[FooBaz]]` in the vault.  
**Implementation complexity:** High.
- Tree-walk all .md files in vault
- Regex replace `[[oldBasename]]` → `[[newBasename]]`
- Write changes back
- Rebuild index
- Return count of updated files to user

**Time estimate:** 6 hours (highest complexity in P0).  
**Risk:** On a 1000+ file vault, this could take seconds. Need async + progress spinner.  
**Mitigation:** Show "Updating backlinks..." spinner. Return count to user. Test on real vaults.

**My verdict:** Worth the complexity. Backlink rot is unacceptable. But this is the critical path item.

---

## Technical Feasibility ✅

I've reviewed the codebase:

- ✅ **safePath() exists and is solid** — prevents directory traversal
- ✅ **Index management is in place** — NoteIndex struct with mu.Lock()
- ✅ **Markdown rendering works** — goldmark integration
- ✅ **PUT /api/note already saves** — just need POST/DELETE variants
- ✅ **No breaking changes needed** — query string format is compatible

**Green light:** This is doable in 16–20 hours.

---

## Testing Strategy

**Unit tests (Go):** 15–20 tests covering success + error cases.  
**Integration tests (Frontend):** 20+ dogfood scenarios.  
**Risk tests:** Backlink updates, duplicate filenames, special chars, deep paths, permission errors.

---

## Timeline Reality Check

| Phase | Hours | Reality |
|-------|-------|---------|
| Create note (POST) | 3 | Simple; parent dir creation is easy |
| Delete note (DELETE) | 2 | Simple; just rm + index cleanup |
| Rename/move (POST /api/move) | 6 | **HIGH RISK**; backlink walk could be slow |
| Create/delete folder | 3 | Simple; mkdir/rmdir wrapper |
| Frontend modals + context menu | 4 | Straightforward Alpine.js |
| Validation + error handling | 2 | Path validation, duplicate checks |
| Dogfood + bug fixes | 4–6 | Real vault testing, edge cases |
| **Total** | **20–26** | Realistic estimate |

**Contingency:** If backlink update is slower than expected, we can defer it to P1.1 (next 2-3 days).

---

## Consensus Check

**⚙ Coder's perspective:**
- "I can implement this in 16–20 hours."
- "Backlink update is complex but I understand the approach."
- "Hard delete is fine; confirm modal prevents accidents."
- "✅ I'm in."

**◎ Designer's perspective:**
- "Modals + context menu is straightforward UX."
- "I wanted templates, but I understand P1."
- "Mobile can work without gestures."
- "✅ I'm in."

**⚔ Contrarian's verdict:**
- "This is the true MVP. Everything else is feature bloat."
- "16–20 hours is realistic. Doable in 1 sprint."
- "Backlink update is the risk; if it goes sideways, we skip it and land P0.1."
- "**✅ APPROVED. No more scope creep. Ship this.**"

---

## Red Lines (Non-Negotiable)

1. **No soft delete in P0.** (defer to P1)
2. **No hotkeys in P0.** (context menu is sufficient)
3. **No templates in P0.** (blank note is sufficient)
4. **No bulk operations in P0.** (single-file only)
5. **No undo/redo system in P0.** (confirmation modals + CodeMirror are sufficient)
6. **No tag filtering in P0.** (P1 addition)
7. **No mobile gestures in P0.** (web UX is sufficient)

If anyone tries to add these before P0 is shipped, Contrarian vetos.

---

## Next Steps (Round 2 Execution)

1. ✅ **Contrarian signs off on scope** (this document)
2. 👉 **Coder:** Audit safePath(), design backlink walk, implement create/delete/move/folder CRUD
3. 👉 **Designer:** Design modals + context menu, implement Alpine.js integration
4. 👉 **Daily syncs:** Track backlink complexity, identify blockers
5. 👉 **Dogfood:** Test on real vaults (pessoal, work, etc.)
6. 👉 **Ship:** P0 done within 16–20 hours

---

## Success Criteria

The sprint is **complete** when:

- [ ] All 5 CRUD endpoints working end-to-end (create, delete, rename, folders)
- [ ] Context menu on files + folders (right-click)
- [ ] All 5 modals working (create note, delete note, rename, create folder, delete folder)
- [ ] Backlinks updated when notes renamed/moved
- [ ] Sidebar tree refreshes immediately
- [ ] Edge cases handled (empty names, special chars, existing files, deep paths)
- [ ] 20+ dogfood scenarios passing
- [ ] No console errors
- [ ] Performance acceptable (<200ms operations on 100+ note vault)
- [ ] **Zero scope creep** (hotkeys, templates, trash, etc. documented as P1)

---

## What Success Looks Like

User can:
1. ✅ Click "New note" → type filename → note opens in edit
2. ✅ Right-click note → "Delete" → confirm → note removed
3. ✅ Right-click note → "Rename" → type new name → all wikilinks updated
4. ✅ Right-click folder → "New folder" → type name → folder appears
5. ✅ Right-click folder → "Delete" → confirm → folder removed (with items warning)

**VaultReader is now a writable vault.** P1 will add polish (hotkeys, trash, templates, etc.).

---

## Signed By

- **⚔ Contrarian:** Scope locked. No more additions. Ship the MVP.
- **⚙ Coder:** Technical feasibility confirmed. Ready to implement.
- **◎ Designer:** UX feasibility confirmed. Ready to design.

**Date:** April 1, 2026  
**Status:** ✅ CONSENSUS REACHED. MOVING TO IMPLEMENTATION.
