# VaultReader

A lightweight, self-hosted web reader and editor for [Obsidian](https://obsidian.md) vaults. Runs as a single static binary inside a tiny Docker container (~8MB).

![Go](https://img.shields.io/badge/Go-1.23-blue) ![Docker](https://img.shields.io/badge/Docker-scratch-lightgrey) ![License](https://img.shields.io/badge/license-MIT-green)

## Features

- Browse multiple Obsidian vaults side by side
- Read notes with rendered Markdown and wikilinks
- Edit notes inline (CodeMirror 6)
- Full-text search across vaults
- Backlinks panel
- Create, delete, rename notes and folders (soft-delete to `.trash/`)
- Syncthing sync status indicator
- Dark mode toggle
- Mobile-friendly responsive layout
- Custom vault icons via `appdata/icons/`

## Quick start

```bash
docker run -p 8080:8080 \
  -v /path/to/your/vaults:/vaults:rw \
  ghcr.io/vaultreader/vaultreader:latest
```

Then open http://localhost:8080.

## Docker Compose

Copy `docker-compose.example.yml` to `docker-compose.yml`, adjust the volume paths, and run:

```bash
docker compose up -d
```

## Configuration

| Flag / Env | Default | Description |
|---|---|---|
| `-vaults` | `/vaults` | Path to your vaults directory |
| `-appdata` | `/appdata` | Path to appdata directory (icons, customisations) |
| `-port` | `8080` | HTTP port to listen on |
| `SYNCTHING_API_KEY` | — | Syncthing API key for sync status |
| `SYNCTHING_API_URL` | — | Syncthing API URL (e.g. `https://syncthing:8384`) |

## Vault icons

Drop an image file into `appdata/icons/` named after your vault (e.g. `work.png`, `personal.svg`). VaultReader serves it automatically — no restart needed.

Supported formats: PNG, SVG, JPG, WebP.

If no icon exists for a vault, a generic folder icon is shown.

## Building from source

```bash
go build -o vaultreader .
./vaultreader -vaults /path/to/vaults -port 8080
```

Or with Docker:

```bash
docker build -t vaultreader .
```

## Vault structure

VaultReader reads any directory structure. Each subdirectory of the vaults mount is treated as a separate vault. Notes are `.md` files; everything else is ignored.

## License

MIT
