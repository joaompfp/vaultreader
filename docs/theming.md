# Theming & customization

VaultReader is themed via CSS variables, with a built-in dark mode and a handful of customization points.

## CSS variables

All colors and shared values live at the top of [`static/style.css`](../static/style.css) under `:root`:

```css
:root {
  --bg:          #fffaf2;        /* page background — warm cream */
  --bg2:         #f4ebd9;        /* sidebar / panels — sandy beige */
  --bg3:         #e8dcc1;        /* hover / chips */
  --text:        #1f1f1f;        /* primary text */
  --text2:       #555;
  --text3:       #888;
  --border:      #d6c9a8;
  --accent:      #b91c1c;        /* terracotta red — primary action */
  --accent-dim:  #ef4444;
  --mono:        ui-monospace, SFMono-Regular, …;

  --transition:  0.15s ease;
  --sidebar-w:   240px;          /* runtime-mutable via the drag handle */
  --text-2xl:    1.5rem;
  …
}
```

Dark mode overrides:
```css
@media (prefers-color-scheme: dark) {
  :root {
    --bg:    #1a1a1a;
    --bg2:   #232323;
    --bg3:   #2e2e2e;
    --text:  #e8e8e8;
    --accent: #f87171;
    …
  }
}

[data-theme="dark"]  { /* manual override; same values as @media */ }
[data-theme="light"] { /* manual override to force light */ }
```

The toolbar's sun/moon button toggles `[data-theme]` on `<html>` and persists to `localStorage['vr-dark']`. By default it follows `prefers-color-scheme`.

## To re-skin

Pick your palette, replace the values under `:root` and the `prefers-color-scheme: dark` block. Everything in the app inherits from these variables — there are no hard-coded color literals in component-specific rules.

Example: a teal+navy theme.

```css
:root {
  --bg:        #fafaff;
  --bg2:       #eef0fa;
  --bg3:       #dde2f0;
  --text:      #0a1628;
  --text2:     #2d3e5c;
  --text3:     #6b7d99;
  --border:    #c5cee0;
  --accent:    #0d9488;     /* teal */
  --accent-dim: #14b8a6;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg:    #0a1628;
    --bg2:   #102036;
    --bg3:   #1a2c47;
    --text:  #e6ebf5;
    --accent: #2dd4bf;
  }
}
```

Rebuild + redeploy. The accent color flows into: chips on hover, scrollbars, the sidebar resize handle, the active outline entry, the graph view's referenced nodes, the editor toolbar's active state, share-modal CTAs, danger buttons (`btn-danger` actually uses its own gradient — see below), and selected file rows.

## Buttons

The button system is two classes:
- `.btn` — neutral base (background `bg2`, text `text2`).
- `.btn-primary` — uses `--accent`.
- `.btn-danger` — uses a hardcoded red gradient (`#dc2626 → #b91c1c`); matches default theme but doesn't follow `--accent` overrides. If you re-skin to non-red, override this:
  ```css
  .btn-danger {
    background: linear-gradient(180deg, var(--accent), var(--accent-dim));
    border-color: var(--accent);
  }
  ```

## Custom vault icons

Drop image files into `appdata/icons/` named after each vault:

```
appdata/icons/
├── pessoal.png       # 32×32 PNG (or larger; downscaled)
├── work.svg          # SVG also fine
├── pcp.webp
├── projects.jpg
└── …
```

Supported extensions: `.png`, `.svg`, `.jpg`, `.jpeg`, `.webp`. The first match wins.

Served live at `/api/vault-icon?name=<vault>`; no restart. If no icon exists, a generic folder SVG is shown.

### Recommended icon sizing

- **Final display size:** 32×32 (top of sidebar). Hover label and active-state border on a 28×28 inner.
- **Source:** any size, but keep it simple — at 32px most detail is invisible. Bold silhouettes work; outlined icons usually don't.
- **Format:** SVG if it's geometric (logos, glyphs); PNG/WebP if it's photographic or has gradients.
- **Background:** transparent. The vault button has its own background (`--bg3`).

The skill doc historically had detailed notes on a Gemini-img2img → threshold → base64 PNG pipeline used to generate the original SOS-style logos. Out of scope here, but the source notes are in `~/.hermes/skills/software-development/vaultreader/SKILL.md`.

## Sidebar width

User-controlled at runtime via the drag handle on the sidebar's right edge. Clamped to 180–600px. Persisted to `localStorage['vr-sidebar-w']`.

To change the default:
```css
:root {
  --sidebar-w: 280px;   /* was 240 */
}
```

## Editor theme

CodeMirror's `oneDark` theme is enabled when the system is in dark mode. Light mode uses CodeMirror's default (no explicit theme). To use a custom CodeMirror theme:

1. Build a CodeMirror bundle that exports your theme.
2. Replace `static/codemirror.bundle.js`.
3. Reference the new export in the `<script>` block at the bottom of `index.html` where `oneDark` is destructured.

## Per-element overrides

Common targets:

| Element | Selector | Note |
|---|---|---|
| Chips in frontmatter | `.fm-chip` | Hover gets accent; rest is `bg3` |
| Pinned recent | `.recent-item.pinned` | Subtle accent gradient |
| Active sidebar note | `.fl-item.fl-file.active` | Accent border-left |
| Outline active heading | `.outline-item.active` | Accent border-left + bg3 |
| Search highlight | `mark` | `color-mix(--accent 30%, transparent)` |
| Graph "referenced" nodes | (Cytoscape inline style) | Set in JS — lookup `accent` from CSS vars at render time |
| Graph "center" node | (Cytoscape inline style) | Same accent color, larger + bolder |

## Print

There's no print stylesheet. Notes print readably (the preview pane is the only relevant area) but with the toolbar/sidebar visible. If you want clean prints, add to `style.css`:

```css
@media print {
  #sidebar, #toolbar, #status-bar, #outline, #editor-toolbar { display: none !important; }
  #main { width: 100%; }
  #preview { padding: 0; }
}
```
