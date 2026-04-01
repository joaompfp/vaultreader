# Round 4 Final Icons — Simplified for 28px Rendering

**Philosophy:** Maximum simplicity, monolithic forms, readable at actual 28px size

---

## Icon 1: PESSOAL (Closed Fist)

**Simplified approach:** Bold closed fist with organic curves

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <!-- Closed fist: simple, bold, monolithic -->
  <path d="M9.2 10.3 C9.8 9.4 10.9 9.2 12.0 9.4 L14.1 9.9 C15.2 10.1 16.3 10.0 17.3 9.6 L19.4 8.9 C20.6 8.4 21.9 8.7 22.8 9.5 L24.2 10.8 C25.4 11.9 25.9 13.5 25.6 15.0 L25.2 17.3 C25.0 18.6 24.4 19.8 23.6 20.7 L21.5 23.1 C20.6 24.1 19.3 24.6 18.0 24.2 L15.9 23.6 C14.7 23.3 13.4 23.3 12.2 23.6 L10.1 24.3 C8.8 24.7 7.4 24.2 6.5 23.2 L5.2 21.8 C4.0 20.6 3.5 19.0 3.8 17.5 L4.3 15.1 C4.5 13.8 5.1 12.6 5.9 11.6 L7.8 9.5 C8.5 8.7 9.6 8.0 10.8 8.2 C11.4 8.3 11.9 8.5 12.3 9.0 C12.6 9.3 13.0 9.1 12.9 8.7 C12.7 7.9 13.3 7.2 14.1 7.1 C15.0 7.0 15.8 7.7 15.9 8.6 L16.2 10.8 C16.3 11.4 16.6 11.9 17.0 12.3 L9.2 10.3 Z" fill="#b91c1c"/>
</svg>
```

Wait, I'm still making this too complex. Let me think about what a **truly simple fist** looks like:

- A circle/oval for the fist body
- A smaller protrusion for the thumb
- That's it. No internal details.

Let me use **basic geometric shapes** combined in simple ways:

---

## Round 4 Icons — TRULY SIMPLIFIED (Geometric + Organic Curves)

### PESSOAL: Closed Fist (Simplified)

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <!-- Fist body: main oval -->
  <ellipse cx="16" cy="15" rx="10" ry="11" fill="#b91c1c"/>
  <!-- Thumb: small lobe -->
  <ellipse cx="7.5" cy="12" rx="3.5" ry="4.5" fill="#b91c1c"/>
  <!-- Wrist taper: bottom extension -->
  <path d="M12.5 26 L13.5 26 L13.2 28.5 L12.8 28.5 Z" fill="#b91c1c"/>
</svg>
```

Actually wait — this uses multiple shapes (ellipse + ellipse + path). The Critic said **"single monolithic silhouette"**. Let me merge them into ONE path:

---

## ICON 1: PESSOAL (Closed Fist — MONOLITHIC)

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="M7.2 13.0 C7.0 11.8 7.6 10.6 8.6 10.0 C9.6 9.3 10.9 9.3 11.9 10.0 L12.9 10.6 C13.8 11.2 14.9 11.5 16.0 11.4 L17.1 11.3 C18.3 11.1 19.5 11.5 20.4 12.3 L21.8 13.5 C23.0 14.5 23.8 15.9 23.9 17.4 L24.0 19.8 C24.1 21.4 23.6 22.9 22.7 24.1 L21.0 26.2 C20.0 27.4 18.5 28.0 17.0 27.8 L15.2 27.5 C13.9 27.3 12.6 27.5 11.5 28.0 L9.5 28.9 C8.2 29.5 6.8 29.0 6.0 28.0 L5.1 26.8 C4.2 25.6 3.8 24.2 4.0 22.8 L4.4 20.2 C4.6 18.8 5.2 17.5 6.1 16.4 L7.2 13.0 Z" fill="#b91c1c"/>
</svg>
```

OK this is better but still not simple enough for 28px rendering. Let me test it:

Actually, I realize the problem: I should **TEST these paths AT 28px first** before committing them. The browser vision said they looked OK, but I need to see them rendered actually on screen.

Let me create a **super simple test** with VERY BASIC shapes:

---

## ULTRA-SIMPLIFIED ICONS (Test Version)

### Icon 1: PESSOAL — Fist (Just thumbs up + circular fist)

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <!-- Monolithic fist: simple curved silhouette -->
  <path d="M6.0 12.0 Q5.0 14.0 5.5 16.0 Q5.8 18.0 6.5 20.0 Q7.2 22.0 8.5 23.5 Q10.0 25.0 11.8 25.8 Q14.0 26.8 16.5 27.0 Q19.0 27.0 21.0 26.0 Q23.0 25.0 24.2 23.2 Q25.5 21.0 26.0 18.5 Q26.5 16.0 26.2 13.5 Q25.8 11.2 24.8 9.5 Q23.5 7.2 21.5 6.0 Q19.0 4.5 16.0 4.5 Q13.0 4.5 10.5 6.0 Q8.0 7.5 6.5 10.0 Q5.5 11.0 6.0 12.0 M6.5 13.0 Q6.0 12.0 5.2 12.5 Q4.5 13.0 4.8 14.0 Q5.0 15.0 6.0 15.2 Q7.0 15.0 6.5 13.0 Z" fill="#b91c1c" fill-rule="evenodd"/>
</svg>
```

Hmm, this is still getting messy. Let me step back and think about **what actually works for protest graphics**.

Looking back at the Critic's feedback, they said:
- **"SOS Racismo is ONE hand"** — it's a hand silhouette, fully filled
- **"PCP's star is ONE star"** — simple 5-point geometric shape

So the shapes should be:
1. **PESSOAL:** A hand silhouette (in a fist position) — ONE continuous filled form
2. **WORK:** Another hand (open, solidarity position) — ONE continuous filled form  
3. **PROJECTS:** A 5-point star — ONE continuous filled form

The problem with my fist designs: they're **too organic and complex**. For 28px rendering, I need **BOLD SIMPLICITY**.

Let me use a **different approach**: Instead of complex curves, use **simple silhouettes** with asymmetric details that create the hand-drawn feel:

---

## Final Round 4 Icons — Simple Silhouettes with Hand-Drawn Feel

### PESSOAL: Closed Fist (Minimalist)

Think: A simple closed fist shape, slightly asymmetrical to feel hand-drawn

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="M9.0 10.5 C8.2 11.2 7.8 12.2 8.0 13.2 L8.5 15.8 C8.8 17.2 9.5 18.5 10.5 19.5 L12.5 21.3 C13.5 22.2 14.8 22.8 16.2 23.0 L18.8 23.3 C20.2 23.4 21.5 22.9 22.5 21.9 L24.0 20.3 C25.2 19.0 25.8 17.3 25.6 15.6 L25.2 12.8 C24.9 11.2 24.0 9.8 22.7 8.9 L20.5 7.5 C19.0 6.5 17.2 6.2 15.5 6.6 L13.0 7.2 C11.2 7.7 9.6 8.8 8.5 10.2 L9.0 10.5 Z" fill="#b91c1c"/>
</svg>
```

### WORK: Open Hand (Solidarity)

Think: An open hand with fingers slightly spread, palm facing up/out

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="M10.5 16.0 C10.2 15.2 10.4 14.3 11.0 13.7 L12.3 12.4 C12.8 11.9 13.1 11.2 13.1 10.5 L13.2 8.5 C13.3 7.3 14.2 6.4 15.4 6.3 C16.5 6.2 17.5 7.0 17.6 8.1 L17.8 10.5 C17.9 11.4 18.3 12.3 18.9 13.0 L20.3 14.6 C20.9 15.3 21.2 16.2 21.1 17.1 L20.9 19.5 C20.8 20.7 19.9 21.6 18.8 21.8 C17.6 22.0 16.5 21.3 16.3 20.2 L16.2 18.3 C16.1 17.6 15.7 17.0 15.2 16.6 L13.9 15.5 C13.3 15.0 13.0 14.3 12.9 13.5 L12.6 11.3 L12.3 10.8 C12.0 10.8 11.8 10.9 11.6 11.1 L10.5 16.0 Z" fill="#b91c1c"/>
</svg>
```

### PROJECTS: 5-Point Star (Organic)

Use a simple 5-point star with slightly off-axis points for hand-drawn feel

```xml
<svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg">
  <path d="M16.1 2.2 C16.4 1.7 17.1 1.7 17.4 2.2 L19.9 8.3 L26.3 9.2 C26.9 9.3 27.2 10.0 26.8 10.5 L22.0 15.4 L23.3 21.8 C23.4 22.4 22.9 23.0 22.3 22.8 L16.0 19.4 L9.7 22.8 C9.1 23.0 8.6 22.4 8.7 21.8 L10.0 15.4 L5.2 10.5 C4.8 10.0 5.1 9.3 5.7 9.2 L12.1 8.3 L14.6 2.2 C14.9 1.7 15.8 1.7 16.1 2.2 Z" fill="#b91c1c"/>
</svg>
```

These are much simpler! Let me test them in a browser now:

