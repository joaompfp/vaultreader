# `examples/` — starter scaffolding

These files exist so a fresh clone has something to render. Without them, VaultReader boots into a blank screen with `[]` for vaults — friendly to nobody.

## What's here

```
examples/
├── vaults/
│   └── demo/                 — sample vault, 7 notes + 2 image assets
│       ├── Welcome.md
│       ├── Syntax showcase.md
│       ├── Wikilinks and tables.md
│       ├── Daily/
│       │   └── 2026-04-29.md
│       ├── Linked notes/
│       │   ├── Hub.md
│       │   ├── Spoke A.md
│       │   ├── Spoke B.md
│       │   └── Spoke C.md
│       ├── templates/
│       │   └── Meeting.md
│       └── assets/
│           ├── banner.svg
│           └── diagram.svg
└── appdata/
    ├── config.json           — admin_token: "" + rw_paths: ["demo"]
    ├── shares.json           — empty
    └── icons/
        └── demo.svg          — vault icon for the sidebar
```

## How to use

**Quickest path** — let docker bind-mount the examples directly:

```bash
docker run -d \
  --name vaultreader \
  -p 8080:8080 \
  -v "$PWD/examples/vaults:/vaults:rw" \
  -v "$PWD/examples/appdata:/appdata:rw" \
  ghcr.io/joaompfp/vaultreader:latest

# Open http://localhost:8080 — you'll land in the demo vault.
```

**Or copy the seed to your real paths** and edit from there:

```bash
mkdir -p /srv/vaultreader/{vaults,appdata}
cp -r examples/vaults/demo /srv/vaultreader/vaults/
cp -r examples/appdata/* /srv/vaultreader/appdata/
```

## Pivoting to your own vaults

Once you've poked around, replace the demo with real vaults:

1. **Add real vault directories** under `vaults/` — each top-level subdirectory becomes a separate vault in the sidebar. Drop your existing Obsidian vault in there, or start from scratch.

2. **Update `appdata/config.json`** to allow writes where you want them. The default `"rw_paths": ["demo"]` only allows writes inside the demo vault; add your own vault names to enable editing them via the web UI.

3. **Set an admin token** if you want admin endpoints (`/api/admin/*`) enabled — generate with `openssl rand -hex 32` and drop into `admin_token`.

4. **Add custom vault icons** at `appdata/icons/<vault-name>.{png|svg|jpg|webp}` — anything will do, the first matching extension wins.

5. **Delete the demo** when you don't want it any more — `rm -rf vaults/demo`. The seed icon and config will continue to work but the `demo` entry in `rw_paths` becomes dead and can be removed.

## What this is **not**

- **Not a test fixture.** Tests should `cp -r examples/vaults/demo` to a temp directory before mutating. The demo is canonical content; mutating it breaks the showcase.
- **Not opinionated configuration.** The values in `config.json` are minimal defaults. You'll outgrow them quickly. See [docs/configuration.md](../docs/configuration.md) for the full schema.
- **Not protected from being committed accidentally.** If you edit the demo vault from a running VaultReader, your changes will appear in `git status`. The actual `appdata/` (one level up, at the repo root) is gitignored — only `examples/appdata/` is tracked.

## Why the split?

The repo's top-level `appdata/` is gitignored — that's where production-style state lives (real admin tokens, real shares, real icons). `examples/appdata/` is intentionally separate and intentionally checked in: the project ships starter content so anyone cloning the repo can boot to a working installation in one command.

You should never mix the two paths. Production deployments mount their own `appdata/` somewhere outside the repo. The `examples/` directory is for the first-impression demo only.
