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
		allNotes: make(map[string]NoteRef),
	}
}

func normalizeName(name string) string {
	// strip .md extension, lowercase
	name = strings.TrimSuffix(name, ".md")
	return strings.ToLower(name)
}

func extractTitle(content, filename string) string {
	// look for first H1
	m := headingRe.FindStringSubmatch(content)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSuffix(filepath.Base(filename), ".md")
}

func (idx *NoteIndex) buildAll(vaultsPath string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.outbound = make(map[string][]string)
	idx.inbound = make(map[string][]string)
	idx.allNotes = make(map[string]NoteRef)

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
		vaultPath := filepath.Join(vaultsPath, vaultName)
		_ = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if shouldSkip(info.Name()) || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			rel, _ := filepath.Rel(vaultPath, path)
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			content := string(data)
			_, body := parseFrontmatter(content)
			title := extractTitle(body, rel)
			nname := normalizeName(filepath.Base(rel))
			key := vaultKey(vaultName, rel)

			// Add compound key for vault-scoped lookup
			compoundKey := vaultName + ":" + nname
			idx.allNotes[compoundKey] = NoteRef{Vault: vaultName, Path: rel, Title: title}
			// Keep global key as fallback
			idx.allNotes[nname] = NoteRef{Vault: vaultName, Path: rel, Title: title}

			// extract wikilinks
			matches := wikilinkRe.FindAllStringSubmatch(body, -1)
			var targets []string
			for _, m := range matches {
				targets = append(targets, normalizeName(m[1]))
			}
			idx.outbound[key] = targets
			for _, t := range targets {
				idx.inbound[t] = append(idx.inbound[t], key)
			}
			_ = nname
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
	key := vaultKey(vault, path)

	// update allNotes (both compound and global keys)
	compoundKey := vault + ":" + nname
	idx.allNotes[compoundKey] = NoteRef{Vault: vault, Path: path, Title: title}
	idx.allNotes[nname] = NoteRef{Vault: vault, Path: path, Title: title}

	// remove old outbound links from inbound index
	old := idx.outbound[key]
	for _, t := range old {
		links := idx.inbound[t]
		var filtered []string
		for _, l := range links {
			if l != key {
				filtered = append(filtered, l)
			}
		}
		idx.inbound[t] = filtered
	}

	// add new outbound links
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
	key := vaultKey(vault, path)
	compoundKey := vault + ":" + nname

	delete(idx.allNotes, compoundKey)
	delete(idx.allNotes, nname)

	// remove outbound links from inbound index
	old := idx.outbound[key]
	for _, t := range old {
		links := idx.inbound[t]
		var filtered []string
		for _, l := range links {
			if l != key {
				filtered = append(filtered, l)
			}
		}
		idx.inbound[t] = filtered
	}
	delete(idx.outbound, key)
}

func (idx *NoteIndex) resolve(name, preferVault string) (string, string, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	nname := normalizeName(name)
	// Pass 1: prefer current vault
	if preferVault != "" {
		if ref, ok := idx.allNotes[preferVault+":"+nname]; ok {
			return ref.Vault, ref.Path, true
		}
	}
	// Pass 2: global fallback
	ref, ok := idx.allNotes[nname]
	if !ok {
		return "", "", false
	}
	return ref.Vault, ref.Path, true
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
		ref, ok := idx.allNotes[normalizeName(filepath.Base(p))]
		title := ref.Title
		if !ok {
			title = strings.TrimSuffix(filepath.Base(p), ".md")
		}
		refs = append(refs, BacklinkRef{Vault: v, Path: p, Title: title, Excerpt: ""})
	}
	return refs
}
