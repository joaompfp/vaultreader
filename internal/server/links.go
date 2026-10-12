// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"path"
	"path/filepath"
	"strings"
)

// ─── Link resolution for the index (backlinks + graph) ───────────────────────
//
// outbound stores each note's wikilink targets in a normalised form (see
// linkTarget); inbound is keyed by the target's basename so that [[Note]],
// [[Folder/Note]], [[Folder/Note|alias]] and [[Note#Heading]] all land in the
// same bucket. Which note a target actually points at is decided by
// resolveLinkLocked, which follows the same tier order as the renderer
// (resolveWikilinkTarget).

// linkTarget normalises the raw target of a [[...]] link: drops a trailing
// `\` (Obsidian's escaped alias pipe), any #heading / ^block suffix and a .md
// extension, uses forward slashes, and lower-cases. "" means "no target"
// (e.g. [[#Heading]] within the same note).
func linkTarget(raw string) string {
	t := strings.TrimRight(raw, `\`)
	if i := strings.IndexAny(t, "#^"); i >= 0 {
		t = t[:i]
	}
	t = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(t), `\`, "/"))
	t = strings.TrimSuffix(t, ".md")
	if t == "" || strings.HasSuffix(t, "/") {
		return ""
	}
	return t
}

// setOutboundLocked indexes the wikilinks in body as note key's outbound
// links. Caller holds the write lock and has dropped any previous links.
func (idx *NoteIndex) setOutboundLocked(key, body string) {
	var targets []string
	for _, m := range wikilinkRe.FindAllStringSubmatch(body, -1) {
		if t := linkTarget(m[1]); t != "" {
			targets = append(targets, t)
		}
	}
	idx.outbound[key] = targets
	for _, t := range targets {
		b := path.Base(t)
		idx.inbound[b] = append(idx.inbound[b], key)
	}
}

// dropOutboundLocked removes note key's outbound links from both maps.
func (idx *NoteIndex) dropOutboundLocked(key string) {
	for _, t := range idx.outbound[key] {
		b := path.Base(t)
		idx.inbound[b] = removeKey(idx.inbound[b], key)
	}
	delete(idx.outbound, key)
}

// resolveLinkLocked returns the note that target (a linkTarget value) in
// note src points at, in the renderer's order: bare name → proximity among
// same-named notes; path → relative to src's folder, then vault root, then
// any note whose path ends with it, then the basename alone. Caller holds
// at least the read lock.
func (idx *NoteIndex) resolveLinkLocked(target string, src NoteRef) (string, string, bool) {
	cands := idx.byName[path.Base(target)]
	if len(cands) == 0 {
		return "", "", false
	}
	srcDir := filepath.Dir(src.Path)
	if !strings.Contains(target, "/") {
		v, p := pickBest(cands, src.Vault, srcDir)
		return v, p, true
	}
	rel := path.Clean(path.Join(strings.ToLower(filepath.ToSlash(srcDir)), target))
	root := path.Clean(strings.TrimPrefix(target, "/"))
	noExt := func(c NoteRef) string { return strings.ToLower(strings.TrimSuffix(c.Path, ".md")) }
	tiers := []func(NoteRef) bool{
		func(c NoteRef) bool { return c.Vault == src.Vault && noExt(c) == rel },
		func(c NoteRef) bool { return c.Vault == src.Vault && noExt(c) == root },
		func(c NoteRef) bool { lp := noExt(c); return lp == root || strings.HasSuffix(lp, "/"+root) },
	}
	for _, match := range tiers {
		var hit []NoteRef
		for _, c := range cands {
			if match(c) {
				hit = append(hit, c)
			}
		}
		if len(hit) > 0 {
			v, p := pickBest(hit, src.Vault, srcDir)
			return v, p, true
		}
	}
	v, p := pickBest(cands, src.Vault, srcDir)
	return v, p, true
}

// backlinkKeysLocked returns the vaultKeys of notes with at least one link
// resolving to vault:notePath, in index order. Caller holds the read lock.
func (idx *NoteIndex) backlinkKeysLocked(vault, notePath string) []string {
	base := normalizeName(filepath.Base(notePath))
	var out []string
	seen := make(map[string]bool)
	for _, k := range idx.inbound[base] {
		if seen[k] {
			continue
		}
		seen[k] = true
		src, ok := idx.byPath[strings.ToLower(k)]
		if !ok {
			continue
		}
		for _, t := range idx.outbound[k] {
			if path.Base(t) != base {
				continue
			}
			if v, p, ok := idx.resolveLinkLocked(t, src); ok && v == vault && strings.EqualFold(p, notePath) {
				out = append(out, k)
				break
			}
		}
	}
	return out
}
