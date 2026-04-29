# Roadmap

Where the project is heading, in priority order. **No commitments** — this is a personal project, items may be reordered, dropped, or expanded based on use.

> Last updated 2026-04-30, after the Tier 1–4 polish wave. Most of what was here originally has shipped; what's left is genuinely the harder / more speculative half.

## Near-term

Small, well-scoped items that fit a 1–4 hour block.

- **Inline `#tag` detection** — `/api/tags` only sees frontmatter today. Adding inline tags needs a tokenizer that skips fenced code blocks and inline code, otherwise `\`#define\`` would false-match. Probably ~80 lines of Go. Search operators (`tag:foo`) would also pick up inline tags automatically once detected.
- **Image dimensions hint** — Obsidian's `![[foo.png|400]]` width syntax. Currently rendered as a plain embed; should set `width="400"` on the `<img>`.
- **Footnotes** — goldmark has `extension.Footnote`; just plug it in.
- **Tag pane: hide automation tags** — `AI-Processed` dominates the cloud at 1080 hits. A "show automation tags" toggle (frontmatter convention or admin-defined ignore list).
- **Filter `.obsidian/`, `.smart-env/`, `.trash/` from WebDAV** — currently exposed. Could filter at the backend handler with a `webdav.FileSystem` wrapper.
- **Outline pane: nested lists** — currently flat anchor list. Could render as `<ul>` nesting following heading hierarchy.
- **Settings → General**: add a "default mode" preference (preview vs. edit) so power-users land directly in the editor.
- **Daily-note template configurable** — currently hardcodes `# YYYY-MM-DD\n\n`. Could read from `<vault>/templates/daily.md` if present, with placeholder expansion (the template-picker code path already supports this).
- **Saved searches: store as operators** — saved searches today are plain strings. Now that operators exist (`tag:foo modified:>7d`), saved searches should be first-class operator queries.

## Medium-term (a weekend each)

- **Hand-rolled canvas graph view (replaces Cytoscape).** Half-day build, half-day polish.
  - **Why:** Cytoscape's renderer is fixed-function. The `cola` layout (shipped 2026-04-29) gave us live force simulation, but the *visual ceiling* — moebio-style edge curves, node glyph variety, particle-trail edges, custom label-collision avoidance — needs canvas-level control.
  - **What:** d3-force (~50KB) for the simulation; raw canvas for rendering. ~300 lines of JS replacing 250 lines of Cytoscape config + the 365KB Cytoscape bundle + 100KB cola.
  - **Specific moebio cues to implement:** label-collision avoidance (force-based: labels repel along parent node's edge tangent); edge curvature varies with cluster density; on hover, connected sub-tree gets a soft fade-up animation; drag gives an "elastic snap" rather than rigid follow.
  - **Trade-off accepted:** lose Cytoscape's built-in pan/zoom/select machinery — reimplement (~80 lines).
  - **Trigger to actually do this:** when the current cola+Cytoscape graph still feels visually lacking after a few weeks of use AND a specific moebio cue I want is unimplementable in Cytoscape.

- **Conflict-aware writes — Phase 2 (real merge UI).** Phase 1 (shipped 2026-04-29) detects + offers Cancel / Take theirs / Keep mine. A real diff-merge UI showing your changes vs. the disk version side-by-side, line-anchored, with "take their line" / "take my line" buttons would beat the current "all or nothing" choice. Would use a JS diff lib (~30KB) + ~200 lines of merge UI.

- **Plugin-style extension API for editor + render.** Tiny JS surface (`registerCommand`, `registerRenderer`, `onNoteOpen`, `registerToolbarButton`) so users can add features without forking. This is what makes Obsidian *Obsidian*. Hardest item in this section; do last, after at least 5 features have organically wanted "an extension point".

- **Tag pane: hierarchical tags** — Obsidian supports `tag/sub/sub` nested tags. Tag cloud is currently flat; nested rendering would surface taxonomy at a glance.

- **Per-vault rate limit / per-token rate limit on shares** — currently 240/min/IP applies to every endpoint. Share-link traffic should probably get a tighter bucket so a leaked link can't DoS the server. Token-based bucket on `/share/<token>`.

- **Auto-link suggestions while typing** — when a typed word matches an existing note title, prompt to wikilinkify it. Skipped in Tier 4 because the UX is fiddly: trigger on word boundaries? inline tooltip? non-modal hint? Obsidian gets this right; doing it badly would be more annoying than not at all. Needs a real spec before building.

## Long-term / vague

- **Mobile editor.** Toolbar hides on `<700px` today. Real mobile editing needs a slimmer toolbar, `inputmode` tweaks, iOS keyboard quirks, and probably a different paste-image flow.
- **OAuth / OIDC.** Currently relies on forward-auth proxy. A built-in OIDC client for users who don't run a proxy.
- **Multi-user permissions** — different users see different `rw_paths`. Today's model is single-user-with-admin-token. Multi-tenant would require sessions, a real users table, etc. Out of scope for personal-use; needed for OSS adoption.
- **Vim/Emacs keymap for editor** — single CodeMirror extension swap.
- **Spell check** — browser-native is already on; just don't break it.
- **Diff view for git-tracked vaults** — `git diff HEAD <path>` rendered side-by-side.
- **Word-count goals** in frontmatter (`goal: 500` shows progress bar).
- **Templater syntax compatibility** — your existing `templates/` files use Obsidian's `<% tp.date.now(...) %>` syntax; my `{{date}}` expansion ignores them. Could parse a tame subset of Templater for cross-tool compatibility.
- **Live-share / collaborative cursors** — out of scope. Skip.

## OSS readiness (before public release)

The minimum bar to flip the repo from "personal hack" to "advertised OSS":
1. **Tests** for the security surface: `safePath`, `isWritable`, `expandEmbeds`, `resolveWikilinkTarget`, `handleUpload`, **the new share-asset allowlist**, **trash name encode/decode**. Around 250 lines of table tests.
2. **README rewrite** — done in the 2026-04-29 docs pass; refreshed 2026-04-30 to reflect Tier 1–4.
3. **CI in `.github/workflows/`** — at minimum: `go vet`, `go build`, `go test`, image build.
4. **Screenshot in README** — auto-generated via Playwright would be ideal; manual fine.
5. **`docker-compose.example.yml` polished** — already exists, may need a pass.
6. **Bug-report template + CONTRIBUTING.md** — light touch; "no SLA" stated up front.

## Recently shipped (2026-04-29/30)

These were on the roadmap; now done. Listed for posterity — see CHANGELOG for details:

- ✓ Frontmatter chips, settings tabs, outline pane, properties strip, pinned notes, daily-note shortcut, KaTeX math, conflict-aware writes (Phase 1), attachment manager, graph view (cola live simulation, ego/folder/vault scopes), tag pane, saved searches, WebDAV-out (read-only).
- ✓ Search ranking by title/recency, Alt+←/→ history, paste-in-preview, rename warning, wikilink right-click, reveal-in-sidebar, daily-edit-mode, save normalization, sidebar drag-and-drop.
- ✓ Undo toasts, attachment-name search, bulk select + bulk ops, note templates with `{{date}}/{{time}}/{{title}}` placeholders.
- ✓ Search operators (`tag:`, `path:`, `title:`, `modified:`).
- ✓ Mermaid + KaTeX in shared notes (via `/share/<token>/asset?name=…` route + auth-bypass).
- ✓ Trash naming overhaul (`VRTRASH_<base64>_<unix>` scheme — round-trip-safe for any path).

## Explicit non-goals

- **Will not** become a sync engine. Vault on disk = source of truth; sync is Syncthing / iCloud / Obsidian Sync's job.
- **Will not** become a generic CMS / public publishing platform. Share links are for one-off sharing; if you need a public docs site, use Quartz or Hugo.
- **Will not** add a JS build step. Frontend stays handwritten + bundled libs as `<script src>`.
- **Will not** add a database. JSON files in `appdata/` are the persistence layer. If something needs more, the answer is "add an index", not "add Postgres".
- **Will not** support arbitrary plugins (auto-loaded JS files in the vault). The plugin API (medium-term item) is opt-in by the *operator*, not the *user*.
- **Will not** add WebDAV write-mode. Syncthing / Obsidian Sync handle this better; opening write paths over HTTP is a different security model than the current read-only one.
