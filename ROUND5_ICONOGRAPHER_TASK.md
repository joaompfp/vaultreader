# Round 5 — Iconographer Task Brief

**Agent:** ✦ Iconographer  
**Task:** Fix PESSOAL (restart with monolithic design) + validate WORK  
**Status:** Awaiting your response  
**Deadline:** This round  

---

## Your Mission

### Task 1: RESTART PESSOAL Icon (Critical)

**Problem:** Current design has TWO DISCONNECTED OVALS — violates monolithic principle.

**Current Score:** 1.5/5 ❌ FAIL  
- Readability at 28px: ❌ Reads as "8", not leaf
- Monolithic form: ❌ Two separate shapes
- Visual boldness: ⚠️ Weak, fragmented
- Political aesthetic: ❌ Generic, corporate feel

**Your Challenge:**

Design a **SINGLE CONTINUOUS PATH** that:
1. ✅ Reads as a LEAF at 28x28px (instantly recognizable)
2. ✅ Is one monolithic filled silhouette (no separate parts)
3. ✅ Has organic, hand-drawn aesthetic (fractional coords, asymmetric curves)
4. ✅ Works as a political movement symbol (would appear on a banner)
5. ✅ Uses warm red (#b91c1c)

**Concept Options:**

#### Option A: Classic Leaf Silhouette
- Outline: One continuous path starting from stem base
- Features: 
  - Pointed tip at top (clearly "leaf-like")
  - Curved blade on both sides (leaf body)
  - Visible stem at bottom
  - Organic irregularities (left side slightly fuller than right, for example)
- Viewbox fill: ~60-70%
- Example structure:
  ```
  M10.2 24.1 (stem base)
  C10.8 22.3, 11.4 20.5, 12.1 18.3 (left blade curve)
  C13.2 15.8, 14.8 13.2, 16.1 11.4 (narrowing to tip)
  C17.3 13.5, 18.9 16.1, 19.8 18.6 (right blade curve — asymmetric!)
  C20.5 20.2, 21.1 22.4, 20.9 24.2 (back to base)
  C20.1 24.0, 10.2 24.1, 10.2 24.1 Z
  ```

#### Option B: Abstract Organic Shape (if classic leaf doesn't read well)
- Inspired by SOS Racismo's hand aesthetic
- Could be: a bold, organic teardrop with stem
- Or: a stylized growth form (upward curve, organic edges)
- Must maintain: pointed top, stem bottom, monolithic form

#### Option C: Switch Concept Entirely
If leaf isn't working, consider:
- **Book/Knowledge** — Monolithic book silhouette (spine visible, pages suggested via fill-rule:evenodd cutout)
- **Fist** — Solidarity fist, monolithic, bold (used in political movements)
- **Heart** — Personal/emotional symbol, monolithic, warm (represents "life")

**Most Likely:** Stick with Option A (classic leaf) but **fix the execution** — make it ONE shape, not two.

---

### Task 2: Validate WORK Icon ✅

**Current Status:** APPROVED (5/5 score)

**Your Job:** Confirm the hammer is production-ready.
- [ ] Verify monolithic form (head + handle are one shape)
- [ ] Check 28px readability (is it instantly recognizable as a hammer?)
- [ ] Confirm organic coordinates throughout (no perfect integers)
- [ ] Assess political aesthetic (would it appear on a labor poster?)

**Expected Outcome:** Confirm WORK can ship without changes.

---

## Design Constraints (From Round 1 Brief)

✅ **FILLED SILHOUETTES, NOT STROKES** — Zero stroke-width, 100% filled  
✅ **ORGANIC FRACTIONAL COORDINATES** — Use values like 16.3, 14.8, 20.7 (not 16, 15, 21)  
✅ **ASYMMETRIC BEZIER CURVES** — Control point handles vary 0.5–2.1px (natural variation)  
✅ **MONOLITHIC BOLD FORMS** — Single unified shape, no decorative add-ons  
✅ **WARM CMYK RED** — #b91c1c (NOT #ff0000)  
✅ **READABLE AT 28x28px** — Instantly recognizable at actual render size  
✅ **VIEWBOX="0 0 32 32"** — Standard 32px canvas  
✅ **MAX 3 PATHS** — Leaf (1 path), Hammer (2 paths max), Lightning (1 path)  

---

## SVG Technique Reminder

To simulate hand-traced aesthetic:
1. **Use C (cubic bezier) curves** — even "straight" edges should be shallow curves
2. **Fractional coordinates** — M16.3 2.1 C17.8 1.4 19.2 1.8 20.4 3.1
3. **Asymmetric handles** — Control points at different distances:
   ```
   C18.8 13.9, 19.8 17.1, 19.5 20.4 (control distances vary: 1.3, 2.8, 2.1)
   ```
4. **No perfect right angles** — Replace L with shallow C curves

---

## Output Format

When you submit your redesigned PESSOAL + validated WORK:

```markdown
## PESSOAL (Redesigned)

**Concept:** [Brief description of the new design]

**Design Rationale:** [Why this design works better]

### SVG (Inline, Production-Ready)

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="..." fill="#b91c1c"/>
</svg>
```

**Visual Gravity Test:**
- Would this appear on a protest poster? YES/NO
- Is it readable at 28x28px? YES/NO
- Does it feel political/movement-like? YES/NO

**Readability Confirmation:** [Describe how the new design is instantly recognizable]

---

## WORK (Validation)

**Status:** APPROVED ✅

**Confirmation:**
- [ ] Monolithic form verified
- [ ] 28px readability confirmed
- [ ] Organic coordinates checked
- [ ] Ready for shipping

### Final SVG

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <rect x="6.8" y="3.6" width="11.4" height="8.1" fill="#b91c1c" rx="0.5"/>
  <path d="M17.2 11.4 C18.8 13.9 19.8 17.1 19.5 20.4 C19.2 23.2 17.9 25.7 16.0 26.9 C14.1 28.1 11.7 28.3 9.6 27.0 C7.5 25.7 6.2 23.4 6.4 20.9 C6.7 17.6 7.9 14.4 9.7 11.9 Z" fill="#b91c1c"/>
</svg>
```
```

---

## Critic's Specific Feedback on PESSOAL

From the Round 4 critique:

> The current two-oval design **violates the monolithic principle**. A true monolithic leaf would be one unified silhouette, not two separated pieces.

> At 28x28px, the icon reads as "8" or an abstract symbol, NOT a leaf. Zero leaf-like cues (no stem, no pointed tip, no blade structure).

> The icon is **explicitly split into TWO DISCONNECTED SHAPES**. This undermines its ability to function as a cohesive symbol.

> The ovals are perfectly symmetrical and smooth, resembling vector-generated shapes rather than organic, hand-traced curves.

**TL;DR:** Merge the two shapes into ONE continuous leaf silhouette, and add obvious leaf markers (pointed tip, visible stem, blade curves).

---

## Questions to Guide Your Redesign

1. **Monolithic Test:** If you trace the outline with one continuous finger, does it visit all parts of the icon without lifting?
2. **28px Test:** Cover the label and show a 28px thumbnail to someone. Do they instantly say "leaf"?
3. **Political Test:** Would you be comfortable printing this on a protest poster or environmental banner?
4. **Organic Test:** Do the curves look hand-drawn, or do they look like a vector tool's auto-smooth?

If all answers are YES, you're ready to ship.

---

## Timeline

- **Now:** Design new PESSOAL, validate WORK
- **Coder reviews:** SVG syntax, rendering quality
- **Critic re-evaluates:** Visual DNA fit
- **Round 5 consensus vote:** Approved for shipping or further refinement

---

## Success Criteria

✅ PESSOAL:
- Single continuous path (one monolithic form)
- Readable as "leaf" at 28px
- Organic hand-drawn aesthetic
- Political symbol quality

✅ WORK:
- Confirmed production-ready
- No changes needed

---

**Agent:** ✦ Iconographer  
**Awaiting:** Your new PESSOAL design + WORK validation  
**Next Step:** Submit designs, await Coder syntax validation, then Critic re-evaluation

---

[AWAITING ICONOGRAPHER RESPONSE]
