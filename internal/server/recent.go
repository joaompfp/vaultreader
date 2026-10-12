// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"sort"
	"strconv"
)

// ─── Recent notes ─────────────────────────────────────────────────────────────

// RecentNote is one row of the sidebar's "Recent" view.
type RecentNote struct {
	Vault string `json:"vault"`
	Path  string `json:"path"`
	Title string `json:"title"`
	MTime int64  `json:"mtime"`
}

const (
	defaultRecentLimit = 50
	maxRecentLimit     = 200
)

// recent returns the limit most recently modified notes across all vaults,
// newest first. mtimes come from the index: the file's mtime at startup,
// then the time this process saw a change (its own saves, moves and the
// 30 s rescan), so it is never off by more than one rescan interval.
func (idx *NoteIndex) recent(limit int) []RecentNote {
	idx.mu.RLock()
	out := []RecentNote{}
	for vault, entries := range idx.searchByVault {
		for _, e := range entries {
			out = append(out, RecentNote{Vault: vault, Path: e.rel, Title: e.title, MTime: e.mtime})
		}
	}
	idx.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].MTime != out[j].MTime {
			return out[i].MTime > out[j].MTime
		}
		if out[i].Vault != out[j].Vault {
			return out[i].Vault < out[j].Vault
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// handleRecent: GET /api/recent[?limit=N] → []RecentNote, newest first.
func (s *server) handleRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	limit := defaultRecentLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			errResponse(w, 400, "invalid limit")
			return
		}
		limit = min(n, maxRecentLimit)
	}
	jsonResponse(w, s.idx.recent(limit))
}
