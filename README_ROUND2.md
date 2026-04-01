# VaultReader Round 2 — Planning Complete

**Status:** ✅ Ready for implementation (Round 2 Execution)  
**Date:** April 1, 2026  
**Council:** ⚙ Coder, ◎ Designer, ⚔ Contrarian

---

## What This Round Delivered

Round 1 (Planning) produced **two competing visions**:
- **Coder's sprint plan:** 44–50 hours, 4-week timeline, CRUD + hotkeys + tags + polish
- **Designer's feature backlog:** 20 features across 3 tiers, Obsidian parity goal

**Round 2 (Governance) reduced scope ruthlessly:**
- **Final P0:** 5 API endpoints + 5 modals = **16–20 hours**
- **Scope cutouts:** No hotkeys, templates, trash, undo/redo, bulk ops, mobile gestures, tags (all P1+)
- **Success:** Users can create, delete, rename notes. **That's MVP.**

---

## Documents in This Directory

Read in this order:

### 1. **SPRINT_BACKLOG_FINAL.md** (START HERE)
The authoritative P0 specification.
- 5 API endpoints with exact specs
- 5 modals + context menu UX
- Frontend + backend changes (line counts, not full code)
- Testing strategy
- Risk mitigations
- Timeline: 16–20 hours
- **This is what you're building.**

### 2. **CONTRARIAN_REVIEW.md**
Scope governance + red lines.
- What was cut and why
- Why hard delete instead of soft
- Why no hotkeys in P0
- Technical feasibility ✅
- Consensus agreement signed by all agents
- **Read if you want to understand the cuts.**

### 3. **plan.md** (Round 1)
Original comprehensive planning doc from all three agents.
- Full feature backlog (P0/P1/P2)
- Architecture notes
- Risk analysis
- Decision tracker
- **Reference only; superseded by Round 2 cuts.**

### 4. **.worktrees/Coder/SPRINT_BACKLOG.md** (Round 1)
Coder's 44–50 hour plan (superseded).
- 4-week timeline with weekly breakdown
- Detailed backend implementation notes
- Alpine.js state properties
- **Reference only; P0 scope is smaller.**

### 5. **.worktrees/Designer/SPRINT_BACKLOG.md** (Round 1)
Designer's 20-feature plan (superseded).
- Tier 0-2 feature matrix
- Design principles
- API summary
- **Reference only; cut to P0 only.**

---

## Quick Start for Implementation

1. **Read SPRINT_BACKLOG_FINAL.md** — 15 min
2. **Clone to your worktree:**
   ```bash
   git checkout -b feature/crud-operations
   ```
3. **Start with Coder** (Go backend):
   - Audit safePath() — 30 min
   - Implement POST /api/note — 3 hours
   - Implement DELETE /api/note — 2 hours
   - Implement POST /api/move + backlink walk — 6 hours (HIGH RISK)
   - Implement POST/DELETE /api/folder — 3 hours
   - Tests — 2 hours
4. **Parallel: Designer starts** (Alpine.js):
   - Context menu (right-click handler) — 2 hours
   - 5 modals (create, delete, rename, folder) — 2 hours
   - Input validation — 1 hour
   - Tree refresh on mutations — 1 hour
5. **Integration** — 2 hours
6. **Dogfood** — 4–6 hours (both agents, real vaults)

**Total:** 20–26 hours (16–20 implementation + 4–6 dogfood)

---

## Critical Path

**Blocker:** Backlink update on rename (6 hours).
- If this proves slower than expected, defer to P0.1 (next sprint).
- Fallback: Ship P0 without backlink updates, mark as "TODO" with feature flag.

---

## Success Criteria (Definition of Done)

When all of these are true, P0 is complete:

- [ ] User can create note (POST /api/note working, modal functional)
- [ ] User can delete note (DELETE /api/note working, confirmation modal)
- [ ] User can rename/move note (POST /api/move working, backlinks updated)
- [ ] User can create folder (POST /api/folder working)
- [ ] User can delete folder (DELETE /api/folder working, recursive flag)
- [ ] Context menu works (right-click on file/folder)
- [ ] All edge cases handled (empty names, special chars, duplicates, deep paths)
- [ ] Sidebar tree refreshes immediately after mutations
- [ ] Backlinks updated when notes renamed/moved (regex tested on real vaults)
- [ ] Dogfood: 20+ test scenarios passing
- [ ] No console errors in DevTools
- [ ] Performance acceptable (<200ms for create/delete/rename on 100+ note vault)

---

## What We're NOT Building (P1+)

These are documented for future sprints. **Do not add to P0.**

- Soft delete / trash folder (P1)
- Undo/redo system (P1)
- Hotkeys (Ctrl+N, Ctrl+D, etc.) (P1)
- Quick Switcher / fuzzy search (P1)
- Command Palette (P1)
- Tag extraction & filtering (P1)
- Note templates (P1)
- Bulk operations (P1)
- Mobile swipe gestures (P2)
- Favorites/pinning (P2)
- Frontmatter editor (P2)

---

## Known Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| Backlink update slow on large vaults | Show spinner; return count; test on real vaults |
| Path traversal attacks | Use existing safePath() utility; audit carefully |
| Accidental deletion | Confirmation modals with explicit filename shown |
| Concurrent edit (Syncthing + web) | mtime check on load; warn if changed externally |
| Wikilink resolution breaks | Test with complex structures; handle case-insensitive |
| Index out-of-sync | Rebuild in every write handler; use mu.Lock() |

---

## Team Assignments

| Role | Focus | Hours |
|------|-------|-------|
| **⚙ Coder** | Go backend (POST/DELETE/MOVE/FOLDER) + index updates + backlink walk | 12–14 |
| **◎ Designer** | Alpine.js modals + context menu + validation + tree refresh | 4–6 |
| **⚔ Contrarian** | Review PRs for scope creep; challenge complexity; verify dogfood | 2–4 |

---

## Communication Plan

- **Daily standup:** 10–15 min, track blockers + backlink complexity
- **Review points:**
  - After POST /api/note (1 endpoint done)
  - After all endpoints (backend done)
  - After modals (frontend done)
  - Before dogfood (integration check)
- **Dogfood:** Continuous on real vaults (pessoal, work, etc.)
- **Final review:** Contrarian signs off on no scope creep before ship

---

## Timeline

```
Start: April 2, 2026
Days 1–2:  POST /api/note + DELETE /api/note (5 hours)
Days 2–4:  POST /api/move + backlink walk (6 hours, critical path)
Day 4–5:   POST/DELETE /api/folder (3 hours)
Day 5–6:   Frontend modals + context menu (4 hours)
Day 6–7:   Integration + validation (2 hours)
Day 7–9:   Dogfood + bug fixes (4–6 hours)
End: April 9–10, 2026

Total: 20–26 hours over 8–9 calendar days
```

---

## Next Meeting

**Time:** April 2, 2026, 9:00 AM  
**Agenda:**
1. Review safePath() implementation
2. Confirm backlink walk approach
3. Assign worktrees + branches
4. Start implementation

---

**Status:** ✅ Round 2 complete. Scope locked. Ready for Round 2 Execution.

**Signed:**
- ⚙ **Coder:** Engineering sign-off
- ◎ **Designer:** UX sign-off
- ⚔ **Contrarian:** Scope sign-off
