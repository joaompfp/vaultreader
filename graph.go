package main

import (
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ─── Graph ─────────────────────────────────────────────────────────────────────

// handleGraph returns the wikilink graph for one or all vaults.
// GET /api/graph[?vault=X] → { nodes: [...], edges: [...] }
// handleGraph supports three scopes:
//   - none / ?vault=X         → whole vault (or all vaults if no vault).
//   - ?vault=X&folder=path    → only notes whose path is under <folder>.
//   - ?center=vault:path&depth=N → ego graph: <center> + N hops via outbound + inbound.
//
// `center` takes precedence over folder/vault filters.
func (s *server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vaultFilter := r.URL.Query().Get("vault") // empty = all vaults
	folder := strings.Trim(r.URL.Query().Get("folder"), "/")
	center := r.URL.Query().Get("center") // "vault:path"
	depthStr := r.URL.Query().Get("depth")
	depth := 1
	if d, err := strconv.Atoi(depthStr); err == nil && d >= 0 && d <= 5 {
		depth = d
	}

	type GraphNode struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Vault string `json:"vault"`
		Path  string `json:"path"`
		Refs  int    `json:"refs"`
		IsCenter bool `json:"isCenter,omitempty"`
	}
	type GraphEdge struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Target string `json:"target"`
	}

	s.idx.mu.RLock()
	defer s.idx.mu.RUnlock()

	// Step 1: build a vaultKey → NoteRef lookup of every note in the index,
	// deduped (allNotes contains both the bare name and `vault:name` keys).
	allByKey := map[string]NoteRef{}
	for _, ref := range s.idx.allNotes {
		key := vaultKey(ref.Vault, ref.Path)
		if _, ok := allByKey[key]; ok {
			continue
		}
		allByKey[key] = ref
	}

	// Step 2: pick the candidate set based on scope.
	noteByKey := map[string]NoteRef{}

	if center != "" {
		// Neighborhood graph: BFS from center using outbound + inbound.
		if _, ok := allByKey[center]; !ok {
			errResponse(w, 404, "center note not found")
			return
		}
		noteByKey[center] = allByKey[center]
		frontier := []string{center}
		for hop := 0; hop < depth; hop++ {
			next := []string{}
			for _, k := range frontier {
				// outbound: edges from k → resolve target normalized names → keys
				for _, t := range s.idx.outbound[k] {
					if ref, ok := s.idx.allNotes[t]; ok {
						tKey := vaultKey(ref.Vault, ref.Path)
						if _, seen := noteByKey[tKey]; !seen {
							noteByKey[tKey] = ref
							next = append(next, tKey)
						}
					}
				}
				// inbound: which notes link to k? inbound is keyed by normalized name.
				// k is "vault:path"; we need to find the normalized names that
				// resolve to k, then look them up in inbound.
				ref := allByKey[k]
				baseName := normalizeName(filepath.Base(ref.Path))
				compoundName := ref.Vault + ":" + baseName
				for _, candidate := range []string{baseName, compoundName} {
					for _, srcKey := range s.idx.inbound[candidate] {
						if srcRef, ok := allByKey[srcKey]; ok {
							sKey := vaultKey(srcRef.Vault, srcRef.Path)
							if _, seen := noteByKey[sKey]; !seen {
								noteByKey[sKey] = srcRef
								next = append(next, sKey)
							}
						}
					}
				}
			}
			frontier = next
		}
	} else {
		// Whole-vault or folder-scoped graph.
		for k, ref := range allByKey {
			if vaultFilter != "" && ref.Vault != vaultFilter {
				continue
			}
			if folder != "" {
				// Folder scope only meaningful with a vault filter — but if
				// folder is given without vault, apply against any vault.
				prefix := folder + "/"
				if ref.Path != folder && !strings.HasPrefix(ref.Path, prefix) {
					continue
				}
			}
			noteByKey[k] = ref
		}
	}

	nodes := make([]GraphNode, 0, len(noteByKey))
	for key, ref := range noteByKey {
		nodes = append(nodes, GraphNode{
			ID:       key,
			Label:    ref.Title,
			Vault:    ref.Vault,
			Path:     ref.Path,
			IsCenter: key == center,
		})
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	// Step 3: collect edges *between* nodes in the candidate set only.
	edges := []GraphEdge{}
	refCount := map[string]int{}
	for srcKey, targets := range s.idx.outbound {
		if _, ok := noteByKey[srcKey]; !ok {
			continue
		}
		for _, t := range targets {
			ref, ok := s.idx.allNotes[t]
			if !ok {
				continue
			}
			tKey := vaultKey(ref.Vault, ref.Path)
			if _, ok := noteByKey[tKey]; !ok {
				continue
			}
			edges = append(edges, GraphEdge{
				ID:     srcKey + "->" + tKey,
				Source: srcKey,
				Target: tKey,
			})
			refCount[tKey]++
		}
	}

	for i := range nodes {
		nodes[i].Refs = refCount[nodes[i].ID]
	}

	jsonResponse(w, map[string]any{
		"nodes":  nodes,
		"edges":  edges,
		"vault":  vaultFilter,
		"folder": folder,
		"center": center,
		"depth":  depth,
	})
}
