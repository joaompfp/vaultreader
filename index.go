package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
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

	entries, err := os.ReadDir(vaultsPath)
	if err != nil {
		log.Printf("index: cannot read vaults dir %s: %v", vaultsPath, err)
		return
	}

	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		vaultName := e.Name()
		vaultRoot := filepath.Join(vaultsPath, vaultName)
		_ = filepath.Walk(vaultRoot, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if shouldSkip(info.Name()) || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			rel, _ := filepath.Rel(vaultRoot, p)
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			content := string(data)
			_, body := parseFrontmatter(content)
			title := extractTitle(body, rel)
			nname := normalizeName(filepath.Base(rel))
			pk := pathKey(vaultName, rel)
			key := vaultKey(vaultName, rel)

			ref := NoteRef{Vault: vaultName, Path: rel, Title: title}
			idx.byName[nname] = append(idx.byName[nname], ref)
			idx.byPath[pk] = ref

			matches := wikilinkRe.FindAllStringSubmatch(body, -1)
			var targets []string
			for _, m := range matches {
				targets = append(targets, normalizeName(m[1]))
			}
			idx.outbound[key] = targets
			for _, t := range targets {
				idx.inbound[t] = append(idx.inbound[t], key)
			}
			return nil
		})
	}
}

func (idx *NoteIndex) updateNote(vault, path, content string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	_, body := parseFrontmatter(content)
	title := extractTitle(body, path)
	nname := normalizeName(filepath.Base(path))
	pk := pathKey(vault, path)
	key := vaultKey(vault, path)

	// Remove old byName entry and byPath entry.
	idx.byName[nname] = removeRef(idx.byName[nname], vault, path)
	delete(idx.byPath, pk)

	// Remove old outbound links from inbound index.
	old := idx.outbound[key]
	for _, t := range old {
		idx.inbound[t] = removeKey(idx.inbound[t], key)
	}

	// Add updated entries.
	ref := NoteRef{Vault: vault, Path: path, Title: title}
	idx.byName[nname] = append(idx.byName[nname], ref)
	idx.byPath[pk] = ref

	// Reindex outbound links.
	matches := wikilinkRe.FindAllStringSubmatch(body, -1)
	var targets []string
	for _, m := range matches {
		targets = append(targets, normalizeName(m[1]))
	}
	idx.outbound[key] = targets
	for _, t := range targets {
		idx.inbound[t] = append(idx.inbound[t], key)
	}
}

func (idx *NoteIndex) removeNote(vault, path string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	nname := normalizeName(filepath.Base(path))
	pk := pathKey(vault, path)
	key := vaultKey(vault, path)

	idx.byName[nname] = removeRef(idx.byName[nname], vault, path)
	delete(idx.byPath, pk)

	old := idx.outbound[key]
	for _, t := range old {
		idx.inbound[t] = removeKey(idx.inbound[t], key)
	}
	delete(idx.outbound, key)
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

	nname := normalizeName(filepath.Base(path))
	keys := idx.inbound[nname]

	var refs []BacklinkRef
	seen := make(map[string]bool)
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		v, p := parts[0], parts[1]
		pk := pathKey(v, p)
		ref := idx.byPath[pk]
		title := ref.Title
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(p), ".md")
		}
		refs = append(refs, BacklinkRef{Vault: v, Path: p, Title: title, Excerpt: ""})
	}
	return refs
}

// removeRef filters out the entry with the given vault+path from a []NoteRef slice.
func removeRef(refs []NoteRef, vault, path string) []NoteRef {
	out := refs[:0]
	for _, r := range refs {
		if !(r.Vault == vault && r.Path == path) {
			out = append(out, r)
		}
	}
	return out
}

// removeKey filters a string out of a slice.
func removeKey(keys []string, key string) []string {
	out := keys[:0]
	for _, k := range keys {
		if k != key {
			out = append(out, k)
		}
	}
	return out
}
