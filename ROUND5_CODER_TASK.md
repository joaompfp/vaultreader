# Round 5 — Coder Task Brief

**Agent:** ⚙ Coder  
**Task:** Refine PROJECTS (soften geometry) + validate syntax  
**Status:** Awaiting your response  
**Deadline:** This round  

---

## Your Mission

### Task 1: REFINE PROJECTS Icon (Minor Iteration)

**Problem:** Too geometric, not enough organic hand-drawn feel.

**Current Score:** 4/5 ⚠️ GOOD BUT NEEDS WORK  
- Readability at 28px: ✅ 4/5 (good)
- Monolithic form: ✅ 5/5 (excellent)
- Visual boldness: ✅ 5/5 (strong)
- **Organic coordinates:** ⚠️ 3/5 ❌ (TOO SHARP, TOO PERFECT)
- Political aesthetic: ✅ 4/5 (good)

**Critic's Feedback:**

> The lightning bolt is **digitally-generated geometry, not hand-drawn**. The angles are perfectly symmetrical and sharp. It looks like a computer-generated shape, not something carefully hand-traced.

> The sharp points feel like **razor angles**, not organic curves. Replace perfectly symmetric points with **slightly rounded tips** or **organic micro-curves**.

> Add **subtle irregularities** to the angles (not perfect geometry). Add **subtle asymmetry** to suggest human hand-drawing.

**Your Challenge:**

Modify the lightning bolt path to:
1. ✅ Maintain readability as "lightning bolt" at 28px
2. ✅ Keep monolithic form (one continuous path)
3. ✅ **Soften sharp points** (add 0.5–1.0px curve radius)
4. ✅ **Add organic micro-curves** instead of razor angles
5. ✅ **Introduce subtle asymmetry** (left side slightly different from right)
6. ✅ Maintain warm red (#b91c1c)

**Current SVG:**
```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="M17.2 1.3 C17.6 0.8 18.4 0.9 18.7 1.4 L22.1 8.1 L29.2 8.3 C29.8 8.3 30.1 9.1 29.7 9.5 L20.8 18.9 L24.1 26.4 C24.3 27.0 23.8 27.6 23.2 27.4 L16.3 22.8 L12.1 30.0 C11.8 30.5 11.0 30.4 10.8 29.8 L13.4 21.2 L6.3 21.0 C5.7 20.9 5.4 20.2 5.8 19.8 L14.9 10.0 L11.2 3.0 C11.0 2.4 11.5 1.8 12.1 2.0 L18.8 6.9 Z" fill="#b91c1c" fill-rule="evenodd"/>
</svg>
```

**Your Refinement Strategy:**

The current path uses **L (line) commands** which create sharp angles:
```
L22.1 8.1   ← sharp corner
L29.2 8.3   ← sharp corner
L20.8 18.9  ← sharp corner
```

**Replace these with C (bezier curve) commands** that create micro-curves:
```
L22.1 8.1 → C21.8 7.6, 22.4 7.9, 22.1 8.1
L29.2 8.3 → C29.1 8.2, 29.3 8.4, 29.2 8.3  (subtle curve, still sharp visually)
L20.8 18.9 → C21.2 18.5, 20.4 19.2, 20.8 18.9
```

Or **blend corners with bezier handles:**
- Instead of a 90° sharp angle, use a 85–87° angle via bezier curve
- The visual effect is the same (still recognizable as sharp) but feels organic

**Example Refinement:**

Original corner at (22.1, 8.1):
```
L22.1 8.1 L29.2 8.3   ← sharp 90° corner
```

Refined corner with micro-curve:
```
C21.8 7.6, 22.4 7.9, 22.1 8.1
C22.8 8.2, 29.0 8.2, 29.2 8.3   ← subtle curve, 1-2px variation
```

**Expected Visual Result:**
- Looks the same to the eye at 32px (still reads as lightning bolt)
- But at high magnification, the angles are slightly softened
- The overall effect: "hand-drawn" rather than "digital"

---

### Task 2: Validate PESSOAL & WORK SVG Syntax

When Iconographer submits new PESSOAL design + validated WORK:

**Validate Each Icon For:**
- [ ] Valid SVG syntax (xmlns, viewBox, fill attributes)
- [ ] No stroke-width or stroke attributes (100% filled)
- [ ] Proper fill-rule="evenodd" (if any holes/cutouts)
- [ ] Fractional coordinates throughout (organic feel)
- [ ] Renders cleanly at both 160px (large) and 28px (small)
- [ ] No path breaks or rendering artifacts
- [ ] Color is exactly #b91c1c (warm red)

**Test Rendering:**
- [ ] Open each SVG in browser at 160px size
- [ ] Verify at 28px size (should still be readable)
- [ ] Check for any visual glitches or stroke artifacts

---

## Technical Constraints

✅ **viewBox="0 0 32 32"** — Standard canvas  
✅ **fill="#b91c1c"** — Warm political red (not #ff0000)  
✅ **No stroke attributes** — 100% filled silhouettes  
✅ **Max 1 path** for lightning (currently 1, should remain 1)  
✅ **fill-rule="evenodd"** if using path holes  
✅ **Fractional coordinates** — All coordinates should be fractional (16.3, not 16)  

---

## Organic Coordinates Checklist

For your refined PROJECTS lightning bolt, verify:
- [ ] All x/y values are fractional (17.2, not 17)
- [ ] Bezier control points vary 0.5–2.1px apart (asymmetric)
- [ ] No perfect right angles (replace with micro-curves)
- [ ] No perfectly symmetrical peaks (one side slightly offset)

**Example of GOOD fractional asymmetry:**
```
M17.2 1.3 C17.6 0.8 18.4 0.9 18.7 1.4    ← control distances: 0.4, 0.1, 0.3
L22.1 8.1 C21.8 7.6 22.4 7.9 22.1 8.1    ← control distances: 0.3, 0.2, 0.0
```

**Example of BAD (too perfect):**
```
M17 1 C17 0 18 0 19 1                     ← whole numbers, perfect symmetry
L22 8 L29 8 L20 18                        ← all sharp angles, no curves
```

---

## Output Format

When you complete refinements:

```markdown
## PROJECTS (Refined — Organic Feel)

**Refinement Strategy:** [Describe which angles you softened, what asymmetries you added]

**Key Changes:**
- [ ] Top point softened (micro-curve added)
- [ ] Left points have organic variation
- [ ] Right points asymmetric to left
- [ ] Bottom tip slightly curved
- [ ] Overall monolithic form preserved

### SVG (Inline, Refined)

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="M17.2 1.3 C17.6 0.8 18.4 0.9 18.7 1.4 
           C ... (refined path with micro-curves and asymmetry)
           Z" fill="#b91c1c" fill-rule="evenodd"/>
</svg>
```

**Validation Results:**
- [ ] SVG syntax valid ✓
- [ ] No stroke artifacts ✓
- [ ] Readable at 28px ✓
- [ ] Organic coordinates verified ✓
- [ ] Renders cleanly at 160px ✓

---

## Testing Checklist

After refinement, test the new PROJECTS icon:

1. **Visual rendering:**
   - [ ] At 160px: still recognizable as lightning bolt?
   - [ ] At 28px: still readable (not too soft)?
   - [ ] Color correct (#b91c1c)?

2. **SVG quality:**
   - [ ] No stroke-width attributes
   - [ ] fill="#b91c1c" only
   - [ ] One continuous path
   - [ ] No rendering errors

3. **Organic feel:**
   - [ ] Points are slightly rounded (not razor-sharp)
   - [ ] Asymmetry present (left ≠ right)
   - [ ] Fractional coords throughout
   - [ ] Feels "hand-drawn" not "CAD-generated"

4. **Monolithic form:**
   - [ ] Single continuous path
   - [ ] No separate elements
   - [ ] Unified silhouette

---

## Questions for You

1. **What's the minimum amount of rounding** to soften points while keeping them visually "sharp"?
   - Answer: 0.5–1.0px curve radius (nearly imperceptible, but enough to kill the "digital" feel)

2. **How do I balance organic asymmetry with readability?**
   - Answer: Shift points by 0.3–0.8px, not dramatically. The icon should still look like a lightning bolt.

3. **Should I replace all L commands with C curves?**
   - Answer: No. Keep the monolithic silhouette sharp-looking. Just add micro-curves at angles (0.2–0.5px variation).

---

## Expected Outcome

✅ **PROJECTS refined:** Same bolt shape, but with organic micro-curves and asymmetry  
✅ **Visual grade:** 4.5/5 → 4.8/5 (minor improvement)  
✅ **Ready for:** Critic re-evaluation  

---

## Timeline

- **Now:** Refine PROJECTS, validate Iconographer's new PESSOAL
- **Critic reviews:** Visual DNA fit of refined designs
- **Round 5 consensus vote:** Approved for shipping

---

**Agent:** ⚙ Coder  
**Awaiting:** Your refined PROJECTS + Iconographer's new PESSOAL  
**Next Step:** Submit SVG, await Critic re-evaluation, then final consensus vote

---

[AWAITING CODER RESPONSE]
