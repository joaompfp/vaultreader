# VaultReader

A lightweight Obsidian vault web reader/editor written in Go.

## Features

- 📂 **Multi-vault** — browse any number of Obsidian vaults from a single UI
- ✏️ **In-browser editor** — CodeMirror 6 with Markdown syntax highlighting, undo/redo, line wrap
- 🔗 **Wikilinks** — `[[Note Name]]` and `[[Note Name|Alias]]` rendered as clickable links with backlink tracking
- ↩️ **Backlinks panel** — see every note that links to the current one
- 🔍 **Search** — Ctrl+K full-text search across all notes in the vault
- 🏷️ **Frontmatter** — collapsible YAML frontmatter viewer per note
- 💾 **Auto-save** — debounced 1.5 s after last keystroke, atomic write (no data loss)
- 🌙 **Dark mode** — follows `prefers-color-scheme`
- 📱 **Responsive** — sidebar drawer on mobile

## Architecture

```
vaultreader/
├── main.go              # Go HTTP server (~400 lines)
├── go.mod / go.sum
├── static/
│   ├── index.html       # Alpine.js SPA + CodeMirror 6 from CDN
│   └── style.css        # 3-panel flex layout, light+dark
├── Dockerfile           # multi-stage, scratch final image
└── docker-compose.yml   # Traefik labels for notes.joao.date
```

## API

| Method | Endpoint               | Description                         |
|--------|------------------------|-------------------------------------|
| GET    | `/api/vaults`          | List vault names                    |
| GET    | `/api/tree?vault=X`    | File tree JSON                      |
| GET    | `/api/note?vault=X&path=Y` | Note: raw, HTML, frontmatter, backlinks |
| PUT    | `/api/note?vault=X&path=Y` | Save raw markdown                   |
| GET    | `/api/search?q=X&vault=X`  | Full-text search results            |
| GET    | `/api/resolve?name=X&vault=X` | Resolve wikilink name → path     |

## Running locally

```bash
# With Go installed
go mod tidy
go run . -vaults /path/to/your/vaults -port 8080

# With Docker
docker compose up --build
```

The server expects vaults as **subdirectories** inside the `--vaults` path:

```
/vaults/
  Personal/
    index.md
    Projects/
      ...
  Work/
    ...
```

## Docker deployment (f3nix / Traefik)

```bash
docker compose pull   # or build
docker compose up -d
```

The compose file assumes:
- Traefik reverse proxy with `t2_proxy` network
- Cloudflare DNS cert resolver
- Authelia + Tailscale middleware chain
- Vaults at `/home/joao/vaults` on the host

## Configuration

| Flag       | Default    | Description              |
|------------|------------|--------------------------|
| `--vaults` | `/vaults`  | Path to vaults directory |
| `--port`   | `8080`     | Port to listen on        |

## Tech stack

- **Backend**: Go 1.21, `net/http` stdlib, [goldmark](https://github.com/yuin/goldmark) (Markdown), [yaml.v3](https://gopkg.in/yaml.v3)
- **Frontend**: [Alpine.js 3](https://alpinejs.dev/) (reactive UI), [CodeMirror 6](https://codemirror.net/) (editor), vanilla CSS
- **Image**: ~14 MB (`scratch` base)
