---
title: Welcome to VaultReader
tags: [getting-started, overview]
pinned: true
created: 2026-04-29
---

# Welcome to VaultReader

If you can read this, your VaultReader install is working. This vault is the **demo vault** — a starter vault that ships with the project to give a fresh install something to render.

## Where you are

You're reading [[Welcome]] from the `demo` vault. The sidebar on the left shows every other note in this vault. Click around — everything you'll see in this vault is meant to be edited or deleted.

## Five things to try

1. **Open the syntax showcase** — [[Syntax showcase]] demonstrates every renderer feature: callouts, math, mermaid diagrams, image embeds, frontmatter chips, code blocks, and tables.
2. **Try the graph view** — click the graph icon in the toolbar. The notes in `Linked notes/` form a small hub-and-spoke structure so the graph isn't empty.
3. **Edit this note** — toggle Edit mode (✏️ in the toolbar, or press `E`). The demo vault is in `rw_paths`, so saves work.
4. **Search** — `Ctrl+K` opens search. Try `tag:getting-started`, `path:Linked`, `modified:>1d`.
5. **Share it** — click the share icon (next to the copy-wikilink icon in the toolbar). Pick "Read-only · no expiry" — you'll get a public link copied to your clipboard.

## Configuring write access

Out of the box, the seed `appdata/config.json` allows writes only to the `demo` vault. To enable writes elsewhere, edit:

```
appdata/config.json
```

…and add vault names or sub-paths to `rw_paths`. See [docs/configuration.md](https://github.com/joaompfp/vaultreader/blob/main/docs/configuration.md) for the full schema.

## Setting an admin token

The shipped `config.json` has an empty `admin_token`. Admin endpoints (`/api/admin/*`) are disabled until you set one:

```bash
# Generate a token
openssl rand -hex 32

# Drop it into appdata/config.json as "admin_token"
# Restart not required — the file is polled.
```

See [docs/security.md](https://github.com/joaompfp/vaultreader/blob/main/docs/security.md) for the full security model.

## Pointing at your own vaults

When you're ready to use VaultReader for real, swap `/vaults` to point at your own Obsidian vault directory:

```yaml
volumes:
  - /path/to/your/vaults:/vaults:rw
  - ./appdata:/appdata:rw
```

Each top-level directory under `/vaults` becomes a vault. Drop a matching icon at `appdata/icons/<vault-name>.{svg|png|jpg|webp}` to customize.

## Where to go next

- [[Syntax showcase]] — every markdown feature in one place
- [[Wikilinks and tables]] — wikilinks inside table cells (handles the alias-pipe gotcha)
- [[Linked notes/Hub]] — the graph view has something to chew on
- [docs/](https://github.com/joaompfp/vaultreader/tree/main/docs) — full feature, API, syntax, security, configuration reference
