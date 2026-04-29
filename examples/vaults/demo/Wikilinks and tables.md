---
title: Wikilinks and tables
tags: [reference, gotchas]
created: 2026-04-29
---

# Wikilinks inside tables

A subtle interaction between Obsidian wikilinks and markdown tables: the alias separator `|` collides with the table-cell separator `|`. Without special handling, goldmark splits the wikilink across columns and the link breaks.

VaultReader handles this transparently — you can use aliased wikilinks inside table cells without escaping anything.

## Examples

Plain wikilink in a cell:

| Field | Value |
|---|---|
| Source | [[Welcome]] |
| Author | [[Linked notes/Hub]] |

With alias (alias-pipe inside cell-pipe — no escaping needed):

| Field | Value |
|---|---|
| File | [[Linked notes/Hub|the hub note]] |
| Linked-from | [[Welcome|the welcome page]] |

VaultReader handles the alias pipe `|` transparently — you don't need to escape it as `\|` like Obsidian sometimes recommends. Both forms render correctly.

> [!info] How it works
> Before the markdown reaches goldmark, the renderer swaps the alias `|` inside `[[…|…]]` for a sentinel character. After rendering, the sentinel is restored. Goldmark's table parser sees no extra pipes, so the cell stays intact.

## Edge cases

- Aliased wikilinks **outside tables**: work natively, no rewriting needed.
- Multiple aliased wikilinks in the same cell: all handled (each one's first `|` swapped).
- Image embeds inside tables: also work — they go through the same pipeline.

If you ever see a wikilink rendered as raw `[[name` in a table, that's a bug — please file an issue with the source markdown.
