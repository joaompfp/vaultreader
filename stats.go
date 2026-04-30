package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ─── Stats handler ────────────────────────────────────────────────────────────

type VaultStat struct {
	Name      string `json:"name"`
	NoteCount int    `json:"noteCount"`
}

type StatsResponse struct {
	TotalNotes int         `json:"totalNotes"`
	Vaults     []VaultStat `json:"vaults"`
}

func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// count notes per vault from the filesystem (not index, to avoid double-counting compound keys)
	var stats []VaultStat
	total := 0
	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		count := 0
		vp := filepath.Join(s.vaultsDir, e.Name())
		_ = filepath.Walk(vp, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(p, ".md") && !shouldSkip(info.Name()) {
				count++
			}
			return nil
		})
		// sort vaults by vaultOrder then alphabetically handled at display layer
		stats = append(stats, VaultStat{Name: e.Name(), NoteCount: count})
		total += count
	}

	// apply preferred vault order
	sort.SliceStable(stats, func(i, j int) bool {
		pi, pj := len(vaultOrder), len(vaultOrder)
		for k, v := range vaultOrder {
			if stats[i].Name == v {
				pi = k
			}
			if stats[j].Name == v {
				pj = k
			}
		}
		if pi != pj {
			return pi < pj
		}
		return stats[i].Name < stats[j].Name
	})

	jsonResponse(w, StatsResponse{TotalNotes: total, Vaults: stats})
}
