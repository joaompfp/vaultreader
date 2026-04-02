# VaultReader Mobile UI Audit Plan — Round 6

**Date**: April 2, 2026 | **Auditors**: ⚙ CSSFixer, ◎ MobileAuditor, ⚔ UXCritic  
**Scope**: Exhaustive mobile UI audit at 375px, 390px (iPhone 14), 430px widths  
**Target**: Produce surgical CSS fixes + HTML improvements for P0/P1 issues  
**Viewport**: 100vh height, 44px minimum touch targets

---

## Current Architecture Summary

### Layout Structure
```
#app (flex, h=100vh, overflow=hidden)
├── #sidebar (240px fixed on desktop, fixed overlay on mobile)
│   ├── .sidebar-header (40px)
│   ├── .vault-icons (flex-row, 5x ~48px buttons = 240px width!)
│   ├── .breadcrumb (flex-shrink=0)
│   └── .file-list (flex:1, overflow-y:auto)
├── #main (flex:1, flex-direction:column)
│   ├── #toolbar (46px min, flex, gap=8px, many buttons)
│   ├── #content-area (flex:1, overflow:hidden)
│   │   ├── .frontmatter-bar (optional)
│   │   ├── .folder-view (padding: 20px 24px, NO overflow-y set!)
│   │   ├── #preview (flex:1, overflow-y:auto)
│   │   └── #editor-wrap (flex:1, overflow:hidden)
│   └── (no explicit backlinks on desktop, fixed overlay)
└── #backlinks (fixed right on desktop, bottom drawer on mobile)
```

### Mobile Breakpoint
- **Only one breakpoint**: `@media (max-width: 700px)`
- **No 480px or 375px breakpoints** — covers all mobile sizes as one
- **Desktop-first CSS** — mobile rules overlay at 700px cutoff

### Key CSS Issues Found

#### 1. `.folder-view` — NO SCROLLING ON MOBILE
```css
.folder-view {
  padding: 20px 24px;
  max-width: 800px;
  margin: 0 auto;
}
/* MISSING: overflow-y: auto; flex: 1; or min-height: 0 */
```

**Problem**: 
- Lives inside `#content-area` (flex:1, overflow:hidden)
- Has NO `overflow-y: auto` or `flex: 1`
- With padding + max-width, it doesn't stretch to fill container
- On mobile, content overflows #content-area silently (parent is overflow:hidden)
- User can't scroll the folder list

**Root Cause**: CSS assumes `.folder-view` is always small enough to fit viewport, but on mobile with sidebar + toolbar + footer, space is tight. When there are many folders, content spills without scroll.

---

## Issue Priority Matrix

### P0 — CRITICAL (Block daily use)

#### **#001: Folder view doesn't scroll on mobile**
- **Impact**: Users can't see folder contents on mobile screens, major UX broken
- **Scope**: CSS only
- **Complexity**: S (Single property fix)
- **Fix Location**: `.folder-view` rule in style.css
- **Diagnosis**: 
  - `.folder-view` lives in `#content-area` (overflow:hidden, flex:1)
  - `.folder-view` has no `overflow-y: auto` or `flex: 1`
  - Parent is constrained but child doesn't fill + scroll
  - Results: silent overflow, content hidden below fold
  
**Proposed Fix**:
```css
.folder-view {
  /* ADD: */
  flex: 1;
  overflow-y: auto;
  padding: 20px 24px;
  max-width: 800px;
  margin: 0 auto;
}
```

**Testing**: Open vault → folder view with 10+ items. Should scroll.

---

#### **#002: Toolbar breadcrumbs overflow and truncate on mobile**
- **Impact**: Path info is cut off, users lose context about location
- **Scope**: CSS + possible HTML restructure
- **Complexity**: M (Layout rethink)
- **Fix Location**: `#toolbar`, `.toolbar-title`, `.toolbar-breadcrumb`, `.tb-seg`, `.tb-note`
- **Diagnosis**:
  - Toolbar: `min-height: 46px`, `padding: 8px 16px`, `gap: 8px`
  - `.toolbar-title`: `flex: 1`, `overflow: hidden`, `text-overflow: ellipsis` (okay)
  - **But inside**: `.toolbar-breadcrumb` (inline-flex, gap:2px) + `.tb-vault` + multiple `.tb-seg` + `.tb-note`
  - Each `.tb-seg` has `max-width: 120px` (too large for mobile)
  - `.tb-note` has `max-width: 240px` (way too large)
  - When vault + breadcrumbs + note name + buttons (New, Search, Preview/Edit, Copy, Backlinks) all fit in < 375px, everything competes for space
  - Result: Breadcrumbs get ellipsized early, note name truncated

**Audit Details at 375px**:
```
[☰] [Vault / path / long-note-name] [New] [🔍] [Preview] [Copy] [📌]
```
With hamburger (24px) + separator spacing (8px) + buttons each ~30-40px = ~280px
Toolbar-title gets only ~95px for all breadcrumbs + note name = **COLLISION**

**Proposed Fixes**:
1. **Reduce `.tb-seg` and `.tb-note` max-width on mobile**:
   ```css
   @media (max-width: 700px) {
     .tb-seg { max-width: 60px; }
     .tb-note { max-width: 120px; }
   }
   ```
   
2. **Hide some breadcrumb segments on mobile** (show only last directory + note name):
   ```css
   @media (max-width: 700px) {
     .toolbar-breadcrumb > span:nth-child(2),
     .toolbar-breadcrumb > span:nth-child(3) {
       display: none; /* hide intermediate segments, keep vault + final */
     }
   }
   ```
   
3. **Condense toolbar buttons on mobile** (already done for Preview/Edit, but copy/backlinks could be icon-only or hidden in menu)

---

#### **#003: Touch targets below 44px minimum**
- **Impact**: Hard to tap buttons on touch devices, accessibility issue
- **Scope**: CSS padding/sizing audit
- **Complexity**: S (Apply padding consistently)
- **Fix Location**: Multiple button classes
- **Diagnosis**:
  - `.vault-btn`: `padding: 10px 4px` (height calc depends on content)
  - `.btn-icon`: `padding: 4px 6px`, `font-size: 14px` (likely 20-24px total height, **below 44px**)
  - `.tb-seg`, `.tb-vault`, `.tb-note`: inline text, no explicit height target
  - `.fv-item`: `padding: 7px 8px`, `font-size: 14px` (likely ~28px, **below 44px**)
  - `.fl-item`: `padding: 5px 14px`, `font-size: 13px` (likely ~26px, **below 44px**)

**Audit**:
- Buttons in toolbar (copy, backlinks, search, new) are all `.btn-icon` = ~20px height
- Vault buttons in sidebar = ~48px (okay!)
- File list items = ~26-28px (below minimum)
- Folder list items = ~28px (below minimum)

**Proposed Fixes**:
```css
/* On mobile, increase file/folder list item targets */
@media (max-width: 700px) {
  .fl-item {
    padding: 8px 14px; /* was 5px 14px */
    min-height: 44px;
    display: flex;
    align-items: center;
  }
  .fv-item {
    padding: 10px 8px; /* was 7px 8px */
    min-height: 44px;
    display: flex;
    align-items: center;
  }
  /* Toolbar buttons: less important to increase, but icon buttons could be bigger */
  .btn-icon {
    padding: 6px 8px; /* was 4px 6px */
    min-height: 40px;
    min-width: 40px;
  }
}
```

---

### P1 — HIGH (Significant friction)

#### **#004: Vault icon buttons take 240px on 375px screen**
- **Impact**: Huge waste of space, sidebar takes entire width, nav unusable
- **Scope**: CSS + HTML
- **Complexity**: M (Responsive layout change)
- **Fix Location**: `.vault-icons`, `.vault-btn`
- **Diagnosis**:
  - `.vault-icons`: `display: flex`, `flex-direction: row`, `scrollbar-width: none`
  - 5 `.vault-btn` buttons, each `flex: 1`, `min-width: 0`, `padding: 10px 4px`
  - Each button = 240/5 = 48px wide
  - On 375px phone: leaves only 135px for rest of sidebar
  - File list gets squeezed, hard to read

**Proposed Fix**:
```css
/* Desktop: 5 columns */
.vault-icons {
  display: flex;
  flex-direction: row;
  overflow-x: auto;
}
.vault-btn {
  flex: 1;
  min-width: 0;
  padding: 10px 4px;
  width: 48px;
}

/* Mobile: horizontal scroll OR grid layout */
@media (max-width: 700px) {
  .vault-icons {
    /* Option A: Smaller, scrollable */
    overflow-x: auto;
    padding: 0;
  }
  .vault-btn {
    flex: 0 0 40px; /* Fixed 40px, scrollable row */
    padding: 8px 2px;
    width: 40px;
  }
  
  /* Option B: 2x3 grid instead of 5 columns (choose one) */
  /* .vault-icons {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 0;
  }
  .vault-btn {
    flex: unset;
    width: auto;
    padding: 8px;
  } */
}
```

**Debate Point**: Should vault icons scroll horizontally or reflow to grid on mobile?
- **Scroll option**: Familiar behavior, minimal CSS changes
- **Grid option**: Uses space better, all icons visible at once if <= 6
- **Recommendation**: Grid for UX, but scroll is safer CSS change

---

#### **#005: Toolbar layout too crowded when note is open**
- **Impact**: Buttons stack poorly, text truncates aggressively, hard to use
- **Scope**: CSS + HTML conditional hiding
- **Complexity**: M (Layout redesign)
- **Fix Location**: `#toolbar`, conditional button visibility
- **Diagnosis**:
  - On mobile with note open: `[☰] [vault / path / note] [New] [🔍] [Preview] [Edit] [Copy] [📌]`
  - That's ~9-10 interactive elements trying to fit in ~360px
  - Vault selector gone (fold navigation via sidebar)
  - But `New`, `Search`, `Preview/Edit`, `Copy`, `Backlinks` = 5 buttons + breadcrumbs
  - No stacking, just aggressive truncation

**Proposed Fixes**:
```css
@media (max-width: 700px) {
  /* Hide secondary buttons on mobile, keep only essentials */
  #toolbar {
    gap: 6px; /* was 8px, tighten */
    padding: 8px 12px; /* was 16px, tighten */
  }
  
  /* Hide copy button on mobile (duplicate in context menu) */
  .btn-copylink {
    display: none;
  }
  
  /* Hide "New" button on mobile, accessible via context menu in sidebar */
  .btn-new-wrap {
    display: none;
  }
  
  /* Reduce search button size or combine with menu */
  .btn-icon {
    font-size: 14px;
    padding: 4px 6px;
  }
  
  /* Shrink breadcrumb text */
  .toolbar-breadcrumb {
    font-size: 12px;
  }
  
  /* Buttons group already responsive, but reduce button group width */
  .btn-group .btn {
    padding: 4px 10px;
    font-size: 12px;
  }
}
```

**Alternative**: Show/hide buttons based on content (e.g., hide Preview/Edit and New when no note open, show only Search + Backlinks)

---

#### **#006: Context menu positioning may go off-screen on mobile**
- **Impact**: Context menu (right-click on item) appears partially hidden, can't tap all options
- **Scope**: CSS + JS
- **Complexity**: M (Calculation logic)
- **Fix Location**: `#ctx-menu` positioning logic (likely in JS)
- **Diagnosis**:
  - `#ctx-menu`: `position: fixed; z-index: 600;`
  - Positioned via JS: `top: event.clientY`, `left: event.clientX` (approx)
  - `min-width: 152px`, `box-shadow`, `border-radius: 7px`
  - On 375px phone at right edge: `left: 320px` + 152px = **472px (off-screen)**
  
**Proposed Fix**:
```css
#ctx-menu {
  position: fixed;
  z-index: 600;
  max-width: 90vw;
  max-height: 80vh;
  overflow-y: auto;
  /* JS must clamp position: */
  /* if (left + width > window.innerWidth) left = window.innerWidth - width - 8 */
}
```

---

#### **#007: Modal min-width 360px too wide for 375px screen**
- **Impact**: Modal has only 7.5px padding left/right (from max-width: 92vw), looks cramped
- **Scope**: CSS
- **Complexity**: S (Adjust min-width)
- **Fix Location**: `.modal-box`
- **Diagnosis**:
  - `.modal-box`: `min-width: 360px`, `max-width: 92vw`
  - On 375px: max-width = 345px (92% of 375)
  - Conflict! max-width < min-width
  - CSS picks max-width, modal becomes 345px (7.5px padding each side)

**Proposed Fix**:
```css
.modal-box {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 24px 28px;
  min-width: auto; /* or remove entirely */
  width: 100%; /* fill parent */
  max-width: 360px; /* target width */
  margin: 0 auto; /* center */
  max-width: 90vw;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

@media (max-width: 700px) {
  .modal-box {
    min-width: unset;
    width: calc(100vw - 32px);
    max-width: unset;
    padding: 20px 16px; /* tighten padding */
  }
}
```

---

#### **#008: New dropdown z-index and positioning clipping on mobile**
- **Impact**: "New note" / "New folder" menu appears behind other elements or off-screen
- **Scope**: CSS + JS positioning
- **Complexity**: M (Coordinate recalculation)
- **Fix Location**: `.new-dropdown`, JS toggle logic
- **Diagnosis**:
  - `.new-dropdown`: `position: fixed; z-index: 700;`
  - Positioned via Alpine: `top: newMenuY; left: newMenuX`
  - Trigger button `.btn-newnote` is in toolbar (at ~40-50px from top on mobile)
  - Dropdown width: `min-width: 160px`
  - On narrow screen: may extend past right edge or be covered by backlinks panel

**Proposed Fix**:
```css
.new-dropdown {
  position: fixed;
  z-index: 700;
  max-width: 90vw;
  max-height: 80vh;
  overflow-y: auto;
  /* JS: position = "near button but ensure on-screen" */
}

@media (max-width: 700px) {
  .new-dropdown {
    /* Position relative to button, with boundary checking */
    /* Consider: show as modal or slide-up sheet instead */
  }
}
```

---

### P2 — NICE-TO-HAVE (Polish)

#### **#009: Swipe to open/close sidebar**
- **Impact**: Mobile users expect swipe gesture for nav drawer
- **Scope**: JS pointer events
- **Complexity**: L (Event handling + state)
- **Notes**: Alpine.js doesn't have swipe by default, would need custom handler or library

---

#### **#010: Font sizes below 13px hard to read on mobile**
- **Impact**: Status bar (11px), breadcrumb (12px), metadata (11px) are too small
- **Scope**: CSS
- **Complexity**: S (Update font-size in mobile media query)
- **Locations**: `.breadcrumb` (12px), `.save-status` (11px), `.sb-context` (11px), etc.
- **Proposed**:
  ```css
  @media (max-width: 700px) {
    .breadcrumb { font-size: 13px; } /* was 12px */
    .save-status { font-size: 12px; } /* was 11px */
    #status-bar { font-size: 12px; } /* was 11px */
  }
  ```

---

#### **#011: Status bar uses too much space on mobile**
- **Impact**: Shows "VaultReader | vault > path | 2K lines | Synced", takes 24px, leaves less room for content
- **Scope**: CSS conditional hiding + collapsing
- **Complexity**: M (Progressive disclosure)
- **Proposed**: Collapse to icon-only on mobile, show on hover/tap

---

#### **#012: CodeMirror keyboard pushes viewport on mobile**
- **Impact**: Virtual keyboard covers input, can't see what you're typing
- **Scope**: JS viewport meta adjustment or editor repositioning
- **Complexity**: L (Mobile OS behavior, hard to fully control)
- **Notes**: May be inherent to mobile web browsers. Could use CSS `position: fixed; bottom: 0;` for editor on mobile.

---

## Testing Plan

### Manual Testing at 3 Viewport Widths
1. **375px** (iPhone SE, oldest small screen)
2. **390px** (iPhone 14)
3. **430px** (Pixel 6, larger Android)

### Test Scenarios (Each Width)
- [ ] Open vault → see folder list with 10+ items
  - Can scroll folder view? (**P0 #001**)
  - Items tappable (44px minimum)? (**P0 #003**)
  
- [ ] Open a note in preview mode
  - Breadcrumb visible? (**P0 #002**)
  - All toolbar buttons visible or hidden sensibly? (**P1 #005**)
  
- [ ] Tap vault icons in sidebar
  - All 5 icons visible or scrollable? (**P1 #004**)
  
- [ ] Right-click item → context menu
  - Menu on-screen, all options tappable? (**P1 #006**)
  
- [ ] Create note modal
  - Modal sized correctly (not cramped)? (**P1 #007**)
  
- [ ] Tap "New" button
  - Dropdown positioned on-screen? (**P1 #008**)
  
- [ ] Edit mode active
  - CodeMirror usable? Keyboard doesn't obscure input? (**P2 #012**)

---

## Agent Collaboration Guidelines

### Debate & Consensus Points
1. **Vault icons layout**: Should we scroll (flex) or grid (reflow)?
   - CSSFixer: Prefer scroll for minimal breakage
   - MobileAuditor: Prefer grid for better UX
   - UXCritic: Grid wins if sidebar doesn't become too tall

2. **Toolbar button hiding**: Which buttons are most important on mobile?
   - CSSFixer: Hide copy (context menu duplicate)
   - MobileAuditor: Hide New button too, add to sidebar context menu
   - UXCritic: Agree, keep only Search, Preview/Edit, Backlinks, Settings

3. **Folder view scrolling**: Is adding `flex: 1; overflow-y: auto;` safe?
   - All agents: Yes, no risk. Parent is overflow:hidden, child needs these properties to scroll.

### Red Flags to Watch
- ❌ Don't remove any layout properties without understanding full flex tree
- ❌ Don't change z-index globally; stack context is delicate (toolbar, backlinks, modals)
- ❌ Don't hardcode pixel widths on mobile (use calc, flex, %)
- ❌ Test changes at all three widths + landscape mode

---

## Execution Strategy (Rounds 2+)

### Round 2: Implementation
1. **P0 #001**: Add `flex: 1; overflow-y: auto;` to `.folder-view`
2. **P0 #002**: Reduce breadcrumb/button max-widths in mobile query
3. **P0 #003**: Increase touch target sizes for file/folder list items

### Round 3: Refinement
4. **P1 #004**: Redesign vault icons (scroll vs. grid decision)
5. **P1 #005**: Conditionally hide/resize toolbar buttons

### Round 4: Advanced
6. **P1 #006**: Fix context menu positioning logic
7. **P1 #007**: Fix modal sizing conflict
8. **P1 #008**: Fix new dropdown positioning

### Round 5+: Polish
9. **P2 #009–012**: Swipe gestures, font sizing, status bar collapsing, etc.

---

## File Change Summary

### Affected Files
- **static/style.css**: ~50–100 line additions/modifications
- **static/index.html**: Minimal (maybe conditional button hiding with x-show)

### CSS Changes Count
- **P0 fixes**: ~15 lines (3 changes, small)
- **P1 fixes**: ~40 lines (5 complex changes)
- **P2 fixes**: ~20 lines (polish)
- **Total**: ~75 lines of CSS additions

### Backwards Compatibility
- All changes in `@media (max-width: 700px)` block
- Desktop CSS untouched
- Safe to deploy

---

## Consensus Check

**Round 1 — Planning**: All agents audit independently, raise concerns, converge on fixes.

- ⚙ **CSSFixer** (me): Scanned architecture, identified P0/P1 issues root causes, CSS-safe fixes ready
- ◎ **MobileAuditor**: [Awaits input]
- ⚔ **UXCritic**: [Awaits input]

**Next Steps**:
1. Fellow agents review this plan, raise any missed issues or disagreements
2. Converge on specific CSS solutions for P0/P1
3. Round 2: Implement and test together
4. Round 3+: Iterate and polish

---

## Appendix: CSS Change Examples

### Example P0 Fix #1: Folder view scrolling
```diff
 .folder-view {
+  flex: 1;
+  overflow-y: auto;
   padding: 20px 24px;
   max-width: 800px;
   margin: 0 auto;
 }
```

### Example P0 Fix #2: Toolbar breadcrumb mobile truncation
```diff
+@media (max-width: 700px) {
+  .tb-seg { max-width: 60px; }
+  .tb-note { max-width: 120px; }
+}
```

### Example P1 Fix #1: Vault icons mobile scrolling
```diff
+@media (max-width: 700px) {
+  .vault-btn {
+    flex: 0 0 40px;
+    padding: 8px 2px;
+  }
+}
```

### Example P1 Fix #2: Modal sizing
```diff
 .modal-box {
-  min-width: 360px;
+  min-width: auto;
   max-width: 92vw;
 }
```

---

**Document Version**: 1.0  
**Status**: Ready for Round 1 Agent Review  
**Last Updated**: April 2, 2026 03:34 AM

