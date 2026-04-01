# VaultReader Icon Design Round — Planning Phase

**Task:** Design and produce 3 production-ready SVG icons for VaultReader vault buttons  
**Deadline:** 5 rounds (Planning → Iteration → Consensus)  
**Council:** ⚙ Coder (Planning Lead), ✦ Iconographer, ⚔ Critic  
**Output:** `ICON_PROPOSALS.md` with final agreed SVGs  

---

## Objective

Create 3 **32x32 vault button icons** that:

1. **visually align** with the SOS Racismo hand logo + PCP (Portuguese Communist Party) star logo
2. Are **production-ready inline SVGs** (no strokes, no external files)
3. Match the **bold political movement aesthetic** (filled silhouettes, organic hand-drawn feel)
4. Work at **actual render size (28x28px)** with zero pixel loss
5. Follow **SVG path best practices** (bezier curves, asymmetric control points, anti-perfect geometry)

### The 3 Icons Required

| Name | Context | Requirements |
|------|---------|--------------|
| **pessoal** | Personal life vault | Home/house silhouette or bold leaf/plant form |
| **work** | Professional/work vault | Hammer or hourglass (time = work) silhouette |
| **projects** | Side projects vault | Star with spark lines OR bold lightning bolt |

---

## Critical Visual DNA (from Logo Analysis)

### SOS Racismo Hand Logo
- **Filled silhouette**, not outline
- Bold red: `#9c1c1f` (warm CMYK)
- Single monolithic form
- Coordinates have organic asymmetry (e.g., `16.3`, `4.7`, not `16.0`, `4.0`)
- No stroke-width, no outline effects
- Readable at small scale (hand gesture is instantly recognizable)

### PCP Star Logo
- **Filled silhouette**, not outline
- Bold red: `#ed1c24` (warm CMYK)
- Perfect 5-pointed star with bold proportions
- `fill-rule="evenodd"` for internal hollow star center cutout
- Clean, monolithic, single-color fill
- No complexity that disappears at 32px

### Synthesis: The Visual DNA
1. **FILLED SILHOUETTES ONLY** — No `stroke`, no `stroke-width` ever
2. **ORGANIC COORDINATES** — Simulate hand-tracing with asymmetric bezier handles
3. **MONOLITHIC BOLD FORMS** — Single recognizable shape, zero detail loss at small scale
4. **WARM CMYK REDS** — Use `#b91c1c` or `#cc1111` (NOT pure `#ff0000`)
5. **EVENODD FILL** — For internal cutouts (like PCP's hollow star center)

---

## SVG Technique Reference

### Hand-Traced Feel (Organic vs. Robotic)

**Robotic (BAD):**
```svg
<path d="M16 4 C14 3 12 3 10 5 L10 20 L22 20 L22 5 C20 3 18 3 16 4" 
      fill="#b91c1c"/>
```
— All coordinates are integers, control points perfectly spaced

**Organic (GOOD):**
```svg
<path d="M16.3 4.7 C14.2 3.1 11.8 2.9 10.1 4.8 L9.9 20.2 L22.1 19.8 L22.3 5.1 C20.1 2.9 18.4 3.2 16.3 4.7" 
      fill="#b91c1c"/>
```
— Coordinates vary (16.3 not 16), control point distances asymmetric, anchor points offset slightly

### Techniques

1. **Asymmetric Control Points** — vary handle distances by 1–3px
   - Instead of `C x1 y1, x2 y2, x y` where both handles are equidistant
   - Use different distances: one handle 2px away, other 4px away

2. **Micro-Offsets in Anchor Points** — e.g., `16.3` instead of `16.0`
   - Creates the "hand-drawn by a stamp" feel
   - 0.1–0.5px variation is subtle but effective

3. **Shallow Curves on "Straight" Edges** — no actual straight lines
   - Instead of `L x y` (line), use shallow `C x1 y1, x2 y2, x y`
   - Keep y1 and y2 within 0.3–0.8px of the start/end

4. **Mental QA Check** — "Would this look auto-traced from a scanned stamp, or typed?"
   - If it looks perfectly symmetrical, add asymmetry
   - If all coordinates are integers, offset some by 0.1–0.5
   - If all curves are perfect arcs, break them slightly

---

## Design Constraints

| Constraint | Value | Reason |
|-----------|-------|--------|
| **viewBox** | `0 0 32 32` | Standard icon grid |
| **Color** | `#b91c1c` (or varied warm red) | Align with SOS/PCP visual DNA |
| **Stroke** | FORBIDDEN | Political logos use fills only |
| **Paths per icon** | Max 3 | Keep silhouettes simple, readable |
| **Actual render size** | 28x28px | That's how buttons display them |
| **Fill-rule** | `evenodd` for cutouts | Internal hollow forms |
| **Output format** | Inline SVG (no `<img>`, no files) | HTML embedded |

---

## Candidate Designs by Icon

### 1. PESSOAL (Personal Life Vault)

**Option A: House Silhouette**
- Roof triangle (bold angle, no straight peak)
- Body rectangle (slightly skewed for organic feel)
- Tiny window cutout (via evenodd fill-rule)
- 3 shapes total: main house path + internal cutout

**Option B: Bold Leaf/Plant**
- Single organic curved form
- Vein details via evenodd cutout
- Represents growth, personal development
- 1–2 paths

**Design recommendation:** Start with house (more instantly recognizable at 28px)

---

### 2. WORK (Professional/Work Vault)

**Option A: Hammer Silhouette**
- HEAD: Bold rectangular form, slightly rotated
- HANDLE: Curved stroke-like form (filled, not stroked)
- Angle: NOT the PCP hammer orientation (must be visually distinct)
- 2 shapes: head + handle merged as single path or two

**Option B: Hourglass (Time = Work)**
- Top bulb, bottom bulb, narrow waist
- Filled silhouette, organic curves
- Instantly suggests "work takes time"
- 1–2 paths

**Design recommendation:** Hammer with different angle than PCP, or hourglass if hammer feels too similar

---

### 3. PROJECTS (Side Projects Vault)

**Option A: Chunky 5-Pointed Star with Spark Lines**
- Main star (distinct from PCP's proportions — bolder, chunkier)
- Spark lines radiating from points (via separate thin paths)
- Represents "shiny new ideas"
- 2–3 paths total

**Option B: Bold Lightning Bolt**
- Filled silhouette, sharp angles but organic curves at joints
- Represents "fast execution", "energy"
- 1 main path

**Design recommendation:** Star with spark lines (more visually distinct from PCP, represents innovation)

---

## Round Structure

### Round 1: PLANNING (This)
- ⚙ Coder writes `plan.md` (strategic vision)
- ✦ Iconographer & ⚔ Critic review and validate
- Output: This document + consensus to proceed

### Round 2: PROPOSALS
- ✦ Iconographer produces all 3 icons (SVG code)
- ⚙ Coder reviews for SVG quality, organic feel
- ⚔ Critic evaluates against visual DNA + readability
- Output: 3 proposed icons per agent

### Round 3: FIRST CRITIQUE
- ⚔ Critic tears each icon apart (alignment, readability, DNA fit)
- ✦ Iconographer reflects on feedback
- ⚙ Coder notes technical concerns
- Output: Critique report + revision priorities

### Round 4: ITERATION
- ✦ Iconographer refines based on feedback
- ⚙ Coder validates SVG paths (organic coords, asymmetry)
- ⚔ Critic spot-checks improvements
- Output: Revised icons

### Round 5: CONSENSUS
- All three agents vote on final 3 icons
- Write to `ICON_PROPOSALS.md`
- [CONSENSUS: YES]

---

## Success Criteria

Each icon must:

- [ ] **Visual DNA Match** — Instantly recognizable as "bold political movement graphic"
- [ ] **Filled Silhouettes** — Zero strokes, 100% filled paths
- [ ] **Organic Coords** — No perfect integers, asymmetric bezier handles visible
- [ ] **32x32 Readability** — Rendered at 28x28, no pixel loss, instantly readable
- [ ] **No Complexity** — Max 3 paths, no details disappear at small scale
- [ ] **Warm Red** — `#b91c1c` or similar CMYK red, not pure `#ff0000`
- [ ] **SVG Valid** — Well-formed path data, no syntax errors
- [ ] **Monolithic** — Single shape aesthetic, not a collage of small pieces
- [ ] **Political DNA** — Feels like it belongs on a fist or a communist banner

---

## Reference: Logo Analysis

### SOS Racismo Hand
- Silhouette: Raised fist, fingers together, thumb out
- Color: `#9c1c1f` warm red
- Coordinates: Asymmetric hand outline, no perfect geometry
- Strokes: ZERO
- Complexity: One bold shape

### PCP Star
- Silhouette: 5-pointed star, bold proportions
- Color: `#ed1c24` warm red
- Coordinates: Nearly perfect geometry BUT with subtle organic offsets
- Fill-rule: `evenodd` for hollow center
- Strokes: ZERO
- Complexity: One star + one internal cutout

### What VaultReader Icons Must Inherit
✓ Filled silhouettes (no strokes)  
✓ Warm CMYK reds  
✓ Monolithic bold forms  
✓ Organic asymmetric coordinates  
✓ Readable at 32px  
✓ Zero-detail minimalism  

---

## Preparation for Round 2

### For ✦ Iconographer
1. Study SOS hand logo coordinates (analyze the path data)
2. Study PCP star coordinates (analyze the evenodd fill-rule)
3. Prepare 3 icon concepts (pencil sketches or mental image)
4. Have SVG code ready for all 3 icons
5. Test at 28x28px rendering (mental check: "Is this readable?")

### For ⚙ Coder (this agent)
1. Be ready to review SVG path quality (bezier smoothness, organic feel)
2. Validate coordinate asymmetry (no perfect integers)
3. Spot-check fill-rule=evenodd usage
4. Mental visual QA: "Does this feel hand-drawn or robotic?"

### For ⚔ Critic
1. Prepare ruthless design critique lens (DNA fit, readability, boldness)
2. Have examples of "too simple" vs "too complex"
3. Be ready to judge: "Does this feel like it belongs on a protest banner?"

---

## Timeline

| Round | Task | Duration | Lead |
|-------|------|----------|------|
| **1** | Plan + Consensus | 30 min | ⚙ Coder |
| **2** | All 3 icons proposed | 1 hour | ✦ Iconographer |
| **3** | Critique + feedback | 45 min | ⚔ Critic |
| **4** | Refine + iterate | 1 hour | ✦ Iconographer |
| **5** | Final consensus + ship | 30 min | All 3 |

**Total: ~4 hours of council time**

---

## Definition of Done

✅ Round 5 consensus achieved  
✅ 3 final SVG icons written to `ICON_PROPOSALS.md`  
✅ Each icon has explanation (design intent, visual DNA connection)  
✅ All agents signed off on quality  
✅ Ready to embed in VaultReader UI  

---

## Next Steps

1. ✦ Iconographer reviews this plan
2. ⚔ Critic reviews this plan
3. If consensus: proceed to Round 2 (proposals)
4. If feedback: iterate plan first

---

## Consensus (Round 1 — Planning Phase)

**Status:** Ready for Round 2

All agents will indicate consensus after reviewing this plan.

---

**Generated by:** ⚙ Coder (Planning Phase)  
**Date:** April 2, 2026  
**Status:** Ready for Review
