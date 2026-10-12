// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─── Index ────────────────────────────────────────────────────────────────────

func newIndex() *NoteIndex {
	return &NoteIndex{
		outbound: make(map[string][]string),
		inbound:  make(map[string][]string),
		byName:   make(map[string][]NoteRef),
		byPath:   make(map[string]NoteRef),
	}
}

func normalizeName(name string) string {
	name = strings.TrimSuffix(name, ".md")
	return strings.ToLower(name)
}

func extractTitle(content, filename string) string {
	m := headingRe.FindStringSubmatch(content)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSuffix(filepath.Base(filename), ".md")
}

// pathKey returns the lower-case "vault:path" key used in byPath.
func pathKey(vault, path string) string {
	return strings.ToLower(vault + ":" + path)
}

func (idx *NoteIndex) buildAll(vaultsPath string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.outbound = make(map[string][]string)
	idx.inbound = make(map[string][]string)
	idx.byName = make(map[string][]NoteRef)
	idx.byPath = make(map[string]NoteRef)
	idx.byAttachment = make(map[string][]NoteRef)
	idx.searchByVault = make(map[string][]searchEntry)

	walkVaultFiles(vaultsPath, func(vaultName, rel string, info os.FileInfo) {
		if !strings.HasSuffix(info.Name(), ".md") {
			// Non-md attachment — track by lower-case basename so embed
			// resolution is O(1) instead of an on-demand vault walk.
			k := strings.ToLower(filepath.Base(rel))
			idx.byAttachment[k] = append(idx.byAttachment[k], NoteRef{Vault: vaultName, Path: rel})
			return
		}
		data, err := os.ReadFile(filepath.Join(vaultsPath, vaultName, rel))
		if err != nil {
			return
		}
		content := string(data)
		fm, body := parseFrontmatter(content)
		title := extractTitle(body, rel)
		nname := normalizeName(filepath.Base(rel))
		pk := pathKey(vaultName, rel)
		key := vaultKey(vaultName, rel)

		ref := NoteRef{Vault: vaultName, Path: rel, Title: title}
		idx.byName[nname] = append(idx.byName[nname], ref)
		idx.byPath[pk] = ref

		idx.searchByVault[vaultName] = append(idx.searchByVault[vaultName], searchEntry{
			rel:        rel,
			title:      title,
			titleLower: strings.ToLower(title),
			bodyLower:  strings.ToLower(content),
			tagsLower:  extractTagsLower(fm),
			mtime:      info.ModTime().Unix(),
		})

		idx.setOutboundLocked(key, body)
	})
}

func (idx *NoteIndex) updateNote(vault, path, content string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	fm, body := parseFrontmatter(content)
	title := extractTitle(body, path)
	nname := normalizeName(filepath.Base(path))
	pk := pathKey(vault, path)
	key := vaultKey(vault, path)

	// Remove old byName entry and byPath entry.
	idx.byName[nname] = removeRef(idx.byName[nname], vault, path)
	delete(idx.byPath, pk)

	idx.dropOutboundLocked(key)

	// Add updated entries.
	ref := NoteRef{Vault: vault, Path: path, Title: title}
	idx.byName[nname] = append(idx.byName[nname], ref)
	idx.byPath[pk] = ref

	idx.setOutboundLocked(key, body)

	// Refresh search cache for this note (drop old entry, append new).
	entries := idx.searchByVault[vault][:0:0]
	for _, e := range idx.searchByVault[vault] {
		if e.rel != path {
			entries = append(entries, e)
		}
	}
	entries = append(entries, searchEntry{
		rel:        path,
		title:      title,
		titleLower: strings.ToLower(title),
		bodyLower:  strings.ToLower(content),
		tagsLower:  extractTagsLower(fm),
		mtime:      time.Now().Unix(),
	})
	idx.searchByVault[vault] = entries
}

func (idx *NoteIndex) removeNote(vault, path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	nname := normalizeName(filepath.Base(path))
	pk := pathKey(vault, path)
	key := vaultKey(vault, path)

	idx.byName[nname] = removeRef(idx.byName[nname], vault, path)
	delete(idx.byPath, pk)

	idx.dropOutboundLocked(key)

	// Drop from search cache.
	entries := idx.searchByVault[vault][:0:0]
	for _, e := range idx.searchByVault[vault] {
		if e.rel != path {
			entries = append(entries, e)
		}
	}
	idx.searchByVault[vault] = entries
}

// notesUnder returns the paths of vault's notes inside folder (or equal to
// it), for callers that re-index a whole subtree.
func (idx *NoteIndex) notesUnder(vault, folder string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var out []string
	for _, ref := range idx.byPath {
		if ref.Vault == vault && (ref.Path == folder || strings.HasPrefix(ref.Path, folder+"/")) {
			out = append(out, ref.Path)
		}
	}
	return out
}

// resolve finds the best note matching name. preferVault biases toward the
// same vault; fromDir (vault-relative directory of the linking note) is used
// to break ties by folder proximity — longer shared prefix wins.
func (idx *NoteIndex) resolve(name, preferVault, fromDir string) (string, string, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	candidates := idx.byName[normalizeName(name)]
	if len(candidates) == 0 {
		return "", "", false
	}
	v, p := pickBest(candidates, preferVault, fromDir)
	return v, p, true
}

// resolvePathSuffix finds the best note whose vault-relative path ends with
// suffix (e.g. "riba3/_analysis/note.md"). Used for path-shaped wikilinks
// that did not resolve via relative or vault-root lookup. Lock must NOT be
// held by the caller.
func (idx *NoteIndex) resolvePathSuffix(suffix, preferVault, fromDir string) (string, string, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	suffix = strings.ToLower(filepath.Clean(suffix))
	var candidates []NoteRef
	for pk, ref := range idx.byPath {
		// pk is lower-case "vault:path"; strip the vault prefix to get lower-case path.
		colon := strings.IndexByte(pk, ':')
		if colon < 0 {
			continue
		}
		lp := pk[colon+1:]
		if lp == suffix || strings.HasSuffix(lp, "/"+suffix) {
			candidates = append(candidates, ref)
		}
	}
	if len(candidates) == 0 {
		return "", "", false
	}
	v, p := pickBest(candidates, preferVault, fromDir)
	return v, p, true
}

// pickBest selects the best NoteRef from candidates using vault preference
// (worth 1000 points) then folder proximity (shared path prefix depth).
func pickBest(candidates []NoteRef, preferVault, fromDir string) (string, string) {
	if len(candidates) == 1 {
		return candidates[0].Vault, candidates[0].Path
	}
	best := 0
	bestScore := -1
	for i, c := range candidates {
		score := sharedPrefixDepth(fromDir, filepath.Dir(c.Path))
		if preferVault != "" && c.Vault == preferVault {
			score += 1000
		}
		if score > bestScore {
			bestScore = score
			best = i
		}
	}
	return candidates[best].Vault, candidates[best].Path
}

// sharedPrefixDepth counts how many leading path components a and b share.
func sharedPrefixDepth(a, b string) int {
	if a == "" || b == "" || a == "." || b == "." {
		return 0
	}
	aParts := strings.Split(strings.ToLower(filepath.ToSlash(filepath.Clean(a))), "/")
	bParts := strings.Split(strings.ToLower(filepath.ToSlash(filepath.Clean(b))), "/")
	depth := 0
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] == bParts[i] {
			depth++
		} else {
			break
		}
	}
	return depth
}

func (idx *NoteIndex) getBacklinks(vault, path string) []BacklinkRef {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var refs []BacklinkRef
	for _, k := range idx.backlinkKeysLocked(vault, path) {
		ref := idx.byPath[strings.ToLower(k)]
		title := ref.Title
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(ref.Path), ".md")
		}
		refs = append(refs, BacklinkRef{Vault: ref.Vault, Path: ref.Path, Title: title, Excerpt: ""})
	}
	return refs
}

// removeRef filters out the entry with the given vault+path from a []NoteRef slice.
// Allocates a fresh slice (does NOT reuse the backing array) so callers that
// hold onto stale references to the original slice — or that iterate over a
// sibling map sharing the array — don't observe corrupted data.
func removeRef(refs []NoteRef, vault, path string) []NoteRef {
	out := make([]NoteRef, 0, len(refs))
	for _, r := range refs {
		if !(r.Vault == vault && r.Path == path) {
			out = append(out, r)
		}
	}
	return out
}

// removeKey filters a string out of a slice (allocates fresh; see removeRef).
func removeKey(keys []string, key string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}
