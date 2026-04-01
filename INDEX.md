# VaultReader Planning Index — Round 2 Complete

**Status:** ✅ Ready for implementation  
**Agent:** ⚔ Contrarian (Round 2 governance)  
**Consensus:** YES (all agents agreed)

---

## Start Here

**If you have 15 minutes, read these (in order):**

1. **SPRINT_BACKLOG_FINAL.md** — What's being built (5 endpoints, 5 modals, 16–20 hours)
2. **README_ROUND2.md** — Quick start guide (team assignments, timeline, success criteria)

**If you have 5 minutes:**
- **ROUND2_SUMMARY.md** — My final verdict on scope cuts

---

## Document Map

### **Core Planning Documents** (This Round)

| Document | Purpose | Read Time | Audience |
|----------|---------|-----------|----------|
| **SPRINT_BACKLOG_FINAL.md** | Authoritative P0 spec (what you're building) | 15 min | ⚙ Coder, ◎ Designer |
| **CONTRARIAN_REVIEW.md** | Scope governance + why things were cut | 10 min | Everyone (understand rationale) |
| **README_ROUND2.md** | Quick start for implementation | 5 min | ⚙ Coder, ◎ Designer |
| **ROUND2_SUMMARY.md** | My final verdict (as Contrarian) | 5 min | Everyone (leadership summary) |
| **ROUND2_COMPLETE.md** | Consolidation of all Round 2 work | 10 min | Reference |

### **Round 1 Reference Documents** (Superseded but kept)

| Document | Purpose |
|----------|---------|
| **plan.md** | Original comprehensive planning (all 15 features, P0-P2) |
| **SPRINT_BACKLOG.md** | Coder's 44–50 hour plan (superseded) |
| **.worktrees/Coder/SPRINT_BACKLOG.md** | Coder's detailed breakdown (superseded) |
| **.worktrees/Designer/SPRINT_BACKLOG.md** | Designer's 20-feature matrix (superseded) |

---

## TL;DR — The MVP

**5 API endpoints. 5 modals. 16–20 hours.**

### What You're Building
✅ Create notes (POST /api/note)  
✅ Delete notes (DELETE /api/note)  
✅ Rename/move notes (POST /api/move) + **update backlinks**  
✅ Create folders (POST /api/folder)  
✅ Delete folders (DELETE /api/folder)  
✅ Right-click context menu  
✅ Input validation + error handling  

### What's NOT in P0
❌ Soft delete / trash  
❌ Undo/redo system  
❌ Hotkeys (Ctrl+N, Ctrl+D, etc.)  
❌ Quick Switcher  
❌ Command Palette  
❌ Templates  
❌ Bulk operations  
❌ Tag filtering  
❌ Mobile gestures  

**All deferred to P1+. Documented in SPRINT_BACKLOG_FINAL.md for future reference.**

---

## Key Decisions (Contrarian's Governance)

| Decision | What Was Proposed | What I Chose | Savings |
|----------|-------------------|--------------|---------|
| Delete behavior | Soft delete to trash | Hard delete + confirmation | 2–3 hrs |
| Hotkeys | Ctrl+N, Ctrl+D, etc. in P0 | P1 addition; context menu sufficient | 4–6 hrs |
| Undo/Redo | Full system | Confirmation modals + CodeMirror native | 2–3 hrs |
| Templates | Full template system | P1; blank note sufficient | 4 hrs |
| Bulk operations | Move/delete multiple files | P1; single-file only in P0 | 4 hrs |
| Mobile | Swipe gestures + long-press | Web UI modals work on touch; P2 | 2 hrs |
| Tags | Extract + filter in P0 | P1; zero MVP impact | 4 hrs |

**Total scope reduction: 26 hours (54–56h → 20–26h)**

---

## Success Criteria (Definition of Done)

The sprint is **complete** when:

- [ ] All 5 CRUD endpoints working end-to-end
- [ ] Context menu (right-click) on files and folders
- [ ] All 5 modals working (create, delete, rename, folder ops)
- [ ] Backlinks updated when notes renamed/moved
- [ ] Edge cases handled (empty names, special chars, duplicates, deep paths)
- [ ] Sidebar tree refreshes immediately
- [ ] 20+ dogfood scenarios passing
- [ ] No console errors
- [ ] Performance <200ms for CRUD on 100+ note vault

---

## Timeline

**Start:** April 2, 2026  
**End:** April 9–10, 2026  
**Duration:** 20–26 hours of implementation + dogfood  
**Critical path:** Backlink update on rename (6 hours, highest risk)

| Days | Task | Hours |
|------|------|-------|
| 1–2 | POST/DELETE /api/note | 5 |
| 2–4 | POST /api/move + backlinks | 6 |
| 4–5 | POST/DELETE /api/folder | 3 |
| 5–6 | Frontend modals + menu | 4 |
| 6–7 | Integration | 2 |
| 7–9 | Dogfood | 4–6 |

---

## Team Assignments

| Agent | Focus | Hours | Status |
|-------|-------|-------|--------|
| **⚙ Coder** | Go backend (CRUD endpoints + backlink walk + index updates) | 12–14 | Ready |
| **◎ Designer** | Alpine.js (modals + context menu + validation) | 4–6 | Ready |
| **⚔ Contrarian** | Scope governance + PR review + dogfood | 2–4 | Ready |

---

## Red Lines (Non-Negotiable)

If any of these are added to P0, Contrarian vetos immediately:

1. **No soft delete** (hard delete only, saves 2–3 hours)
2. **No hotkeys** (context menu sufficient, saves 4–6 hours)
3. **No templates** (blank note sufficient, saves 4 hours)
4. **No bulk operations** (single-file only, saves 4 hours)
5. **No undo/redo** (confirmation modals sufficient, saves 2–3 hours)
6. **No tag filtering** (P1, saves 4 hours)
7. **No mobile gestures** (web UI sufficient, saves 2 hours)

---

## Consensus Agreement

All three agents signed off:

- **⚙ Coder:** "I can implement this in 16–20 hours. Backlink update is complex but doable. ✅"
- **◎ Designer:** "Modals + context menu are straightforward. I understand the P1 deferrals. ✅"
- **⚔ Contrarian:** "This is the true MVP. Everything else is feature bloat. ✅ APPROVED."

---

## Risk Register

| Risk | Severity | Mitigation |
|------|----------|-----------|
| Backlink update slow on large vaults | HIGH | Show spinner; return count; test on real vaults |
| Path traversal attacks | HIGH | Use safePath(); audit carefully |
| Accidental deletion | HIGH | Confirmation modals with explicit filename |
| Concurrent edit (Syncthing + web) | MEDIUM | mtime check on load; warn if changed |
| Wikilink resolution breaks | MEDIUM | Test with complex structures |
| Index out-of-sync | HIGH | Rebuild in every write handler |

---

## What's Not in Scope (P1+)

Clearly documented for future reference:

- **Soft delete** — Adds folder mgmt + GC cron (P1)
- **Undo/redo** — Confirmation modals sufficient (P1)
- **Hotkeys** — Power user feature (P1)
- **Quick Switcher** — Fuzzy search is polish (P1)
- **Command Palette** — Discoverable but not MVP (P1)
- **Templates** — Blank note sufficient (P1)
- **Bulk operations** — Rare, complex (P1)
- **Tag filtering** — Nice-to-have (P1)
- **Mobile gestures** — Web UI sufficient (P2)
- **Favorites/pinning** — Pure vanity (P2)
- **Frontmatter editor** — Rarely edited via web (P2)

---

## Next Meeting

**When:** April 2, 2026, 9:00 AM  
**Agenda:**
1. Review safePath() implementation
2. Confirm backlink walk approach
3. Assign branches + worktrees
4. Start implementation

---

## Quick Links

**Implementation:** Start with SPRINT_BACKLOG_FINAL.md  
**Timeline:** See README_ROUND2.md  
**Rationale:** See CONTRARIAN_REVIEW.md  
**Reference:** See plan.md for full backlog  

---

## Consensus

**[CONSENSUS: YES]**

All agents agreed on P0 scope. Ready for Round 2 Execution (implementation).

---

**Generated by:** ⚔ Contrarian  
**Date:** April 1, 2026  
**Status:** ✅ Complete
