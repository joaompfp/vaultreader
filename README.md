# VaultReader

A self-hosted web reader and editor for [Obsidian](https://obsidian.md) vaults. Single ~8MB Go binary, no JavaScript build step, no database.

![Go](https://img.shields.io/badge/Go-1.21-blue)
![Docker](https://img.shields.io/badge/Docker-scratch-lightgrey)
![License](https://img.shields.io/badge/license-MIT-green)

> **Status:** personal project, used daily at `notes.joao.date`. PRs welcome but no SLA. The project is in active development; features land in main without long deprecation cycles.

## What it is

A read-mostly web view of your Obsidian vaults that you can put behind your reverse proxy of choice. Designed for the case where you want to:

- Read your notes from a phone or borrowed computer without installing Obsidian.
- Share a single note via a signed URL with an optional expiration.
- Make light edits when you don't have your main machine — toolbar, wikilink autocomplete, paste-to-upload images. Conflict-aware writes prevent silent overwrites when Syncthing is bringing in changes from another device.
- Browse your vault structure, tags, attachments, and the wikilink graph.

It is not a sync engine, not a multi-user collaboration platform, not a Notion replacement. The vault on disk is the source of truth; VaultReader is a window onto it.

## Features

### Reading
- Multiple vaults with custom icons (`appdata/icons/<vault>.{png,svg,jpg,webp}`)
- Wikilinks (`[[note]]`, `[[note|alias]]`) with vault-scoped resolution
- Image embeds (`![[image.png]]`, including note-relative subpaths)
- Backlinks panel with excerpts
- Outline (table-of-contents) right rail with scroll-spy
- Note properties strip — size, modified time, word count, link counts
- Frontmatter rendered with array-typed and tag-like keys as clickable chips
- Mermaid v11 diagrams (`flowchart`, `sequence`, `gantt`, `pie`, `block`, …)
- KaTeX math (`$$…$$` block, `\(…\)` inline; bare `$` deliberately not consumed to avoid currency conflicts)
- Obsidian-style callouts (`> [!info] Title`) rendered with a single accent style; type preserved as `data-callout="<type>"` for custom CSS
- Mobile-friendly layout with sliding sidebar
- Dark mode (auto + manual toggle)

### Editing
- CodeMirror 6 editor with markdown syntax highlighting
- Toolbar (14 buttons): bold, italic, strikethrough, heading-cycle, lists, task, quote, inline/block code, table, link, wikilink, mermaid (with 5 starter dropdowns)
- `[[` autocomplete from `/api/search`
- Paste/drop image upload to `<note-dir>/attachments/` — works in **both edit mode and preview mode** (paste while reading appends `![[…]]` at end of note)
- Autosave with conflict detection (mtime check; resolution modal on collision)
- Backend save normalization — strips trailing whitespace, ensures one trailing newline, so notes round-trip cleanly between Obsidian / vim / VaultReader
- Note templates from `<vault>/templates/*.md` with `{{date}}`, `{{date:FMT}}`, `{{time}}`, `{{title}}` placeholders
- Rename warning when a note has incoming wikilinks
- Admin-managed writable paths (configure vaults / subfolders that allow editing in `appdata/config.json` → `rw_paths`)

### Browsing & discovery
- Search overlay (`Ctrl+K` or `/`) with **operators**: `tag:foo`, `path:bar`, `title:baz`, `modified:>7d`, `modified:<2026-01-01`. Plain text matches name/title/body. Results ranked by title-match × recency.
- **Search across attachment names** (image filenames surface as `🖼` results)
- Saved searches (per-browser localStorage)
- Tag pane (frontmatter `tags:` aggregated across all vaults)
- Graph view via Cytoscape + cola live force simulation — three scopes (all / vault / folder / **ego graph** rooted at a note, depth 1–5). Drag any node to ripple the graph; shift-click to re-center. Toolbar icon picks the smallest meaningful scope automatically.
- Right-click a `[[wikilink]]` in preview → context menu (Open / new tab / Reveal in sidebar / Copy)
- **Reveal in sidebar** (`Ctrl+Shift+L`) — scroll the sidebar to the active note
- **Alt+← / Alt+→** for back/forward through visited notes
- Daily-note shortcut (`Ctrl+D` opens or creates `daily/YYYY-MM-DD.md`, drops you into edit mode for fresh ones)
- Pinned notes via `pinned: true` frontmatter — float to top of recents
- Sidebar with folder browser, sort options, file-commander UI; resizable; **drag-to-move**, **bulk select** (Cmd/Ctrl-click, Shift-range), **bulk delete/move** with combined undo toast

### Sharing
- Read-only share links with optional expiration (`/share/<token>`)
- **Toolbar share button + popover** — crimson active state when the open note has any link, badge with count, popover lists each share with one-click Copy / Open / Revoke and three quick-create options (RO/no-expiry, RO/7-day, editable). No more digging through Settings → Shared.
- **Mermaid + KaTeX render in shared notes** via a tightly-scoped asset route under the share-bypass (no extra Authelia config needed)
- Active-shares management with bulk revoke (single-call backend endpoint)
- Custom share URL prefix supported via reverse proxy (e.g. `notes.example.com/notas/<token>`)

### File operations
- Create / rename / delete / move notes and folders
- **Undo toast** for soft-deletes (6.5s window with one-click restore)
- Soft-delete to `.trash/` (per vault) using a base64-encoded path scheme that round-trips safely for any filename. Restore or permanently delete from the Trash tab.
- Attachment manager — list all images per vault, find orphans (no `![[…]]` references), filter by folder/name, see which notes reference each, bulk delete

### Integration
- Read-only WebDAV at `/webdav/` (point Obsidian Mobile or any WebDAV client at it)
- Syncthing API for sync status indicator
- `/health` endpoint for liveness checks
- Authelia / forward-auth friendly (proxies pass through cleanly; rate limit honors `X-Real-IP` / `X-Forwarded-For`)

### Operations
- Single Go binary (~8MB after `-s -w` strip)
- `embed.FS` static assets — no separate static dir to deploy
- 7.4MB Docker image based on `scratch`
- In-memory wikilink + backlink index, rebuilt on startup

### Keyboard shortcuts
| Shortcut | Action |
|---|---|
| `Ctrl/⌘ + K` or `/` | Open search |
| `Ctrl/⌘ + N` | New note |
| `Ctrl/⌘ + D` | Open today's daily note (creates if missing, opens fresh ones in edit mode) |
| `Ctrl/⌘ + Shift + C` | Copy wikilink for current note |
| `Ctrl/⌘ + Shift + L` | Reveal current note in sidebar |
| `Alt + ←` / `Alt + →` | Back / forward through visited notes |
| `E` | Toggle preview/edit |
| `?` | Show shortcuts overlay |
| `Esc` | Close any modal / menu / clear bulk selection |
| In editor: `Ctrl/⌘ + B / I / E / K / L` | Bold, italic, inline code, link, wikilink |
| In editor: `[[` | Trigger wikilink autocomplete |
| In sidebar: Cmd/Ctrl-click | Toggle row in bulk selection |
| In sidebar: Shift-click | Range-select |

## Quick start

The repo ships with `examples/vaults/demo/` (a sample vault) and `examples/appdata/` (config + icon) so you can boot to a working installation without preparing anything:

```bash
git clone https://github.com/joaompfp/vaultreader.git
cd vaultreader
docker run -d --name vaultreader -p 8080:8080 \
  -v "$PWD/examples/vaults:/vaults:rw" \
  -v "$PWD/examples/appdata:/appdata:rw" \
  ghcr.io/joaompfp/vaultreader:latest
```

Open <http://localhost:8080> — you'll land in the **demo vault**, which contains a syntax showcase, a small graph of cross-linked notes, a sample template, and instructions for pivoting to your real vaults. See [examples/README.md](examples/README.md) for the full layout.

When you're ready to use VaultReader for real, swap the bind-mounts to point at your own vault directory and a fresh `appdata/`:

```bash
docker run -d --name vaultreader -p 8080:8080 \
  -v /path/to/your/vaults:/vaults:rw \
  -v /path/to/your/appdata:/appdata:rw \
  ghcr.io/joaompfp/vaultreader:latest
```

Each top-level subdirectory under `/vaults` becomes a separate vault in the sidebar. See [docs/configuration.md](docs/configuration.md) for `appdata/config.json` (admin token, RW paths) and [docs/security.md](docs/security.md) for the security model before exposing the service publicly.

## Docker Compose

Copy `docker-compose.example.yml` to `docker-compose.yml`, adjust paths, then:

```bash
docker compose up -d
```

See [docs/configuration.md](docs/configuration.md) for the full configuration reference, including admin tokens, RW paths, Authelia integration, and Syncthing.

## Building from source

Host build:
```bash
go build -o vaultreader .
./vaultreader -vaults /path/to/vaults -appdata ./appdata -port 8080
```

Docker build:
```bash
docker build -t vaultreader .
```

The Dockerfile runs `go mod tidy` inside the builder stage, so adding a new Go dependency is a one-file edit (`main.go`) — no host-side `go mod tidy` round-trip needed.

## Documentation

- [Features](docs/features.md) — every feature in detail
- [Configuration](docs/configuration.md) — flags, env vars, admin token, RW paths, Authelia, Syncthing
- [Architecture](docs/architecture.md) — backend, frontend, indexing, asset bundle
- [API reference](docs/api.md) — every endpoint with request/response shapes
- [Syntax reference](docs/syntax.md) — wikilinks, embeds, mermaid, math, frontmatter conventions
- [Security model](docs/security.md) — admin token, share-link signing, RW paths, rate limit, body caps
- [Deploying behind Authelia](docs/authelia.md) — works out of the box; share links are public-by-token
- [Skin / theming](docs/theming.md) — CSS variables, dark mode, custom vault icons
- [Changelog](CHANGELOG.md)

## Project layout

```
main.go                 — single-file Go server (~2500 lines)
go.mod                  — three dependencies: goldmark, yaml.v3, x/net/webdav
static/                 — Alpine + CodeMirror + Mermaid + KaTeX + Cytoscape, all bundled
  index.html            — single-page app
  style.css             — full stylesheet
  *.min.{js,css}        — third-party libs (no build step)
  fonts/                — KaTeX font files
docs/                   — user + developer documentation
appdata/                — runtime data (gitignored): config.json, shares.json, icons/
docker-compose.example.yml
Dockerfile
```

Notes never leave the filesystem you mount. Custom user data (admin config, share tokens, vault icons) lives under `appdata/` and is gitignored — fork-safe.

## License

MIT
