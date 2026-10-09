// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// walkVaultFiles visits every regular file in every vault, never
// descending into skipped directories (.trash, .git, .obsidian, .vr, …).
// buildAll and the rescanner share it so they always agree on what
// belongs in the index.
func walkVaultFiles(vaultsDir string, fn func(vault, rel string, info os.FileInfo)) {
	entries, err := os.ReadDir(vaultsDir)
	if err != nil {
		log.Printf("walk: cannot read vaults dir %s: %v", vaultsDir, err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		vault := e.Name()
		root := filepath.Join(vaultsDir, vault)
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if p != root && shouldSkip(info.Name()) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				// Index symlinked files, but never follow symlinked dirs.
				target, serr := os.Stat(p)
				if serr != nil || !target.Mode().IsRegular() {
					return nil
				}
				info = target
			} else if info.IsDir() || !info.Mode().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			fn(vault, rel, info)
			return nil
		})
	}
}

type fileStamp struct {
	mtime int64
	size  int64
}

// rescanner keeps the in-memory index in step with edits made outside
// this process (the other instance, Obsidian, Syncthing) by comparing
// stat() results between passes. Only changed .md files are read.
type rescanner struct {
	vaultsDir string
	idx       *NoteIndex
	seen      map[string]fileStamp // vault + "\x00" + rel
}

func newRescanner(vaultsDir string, idx *NoteIndex) *rescanner {
	rs := &rescanner{vaultsDir: vaultsDir, idx: idx}
	rs.seen = rs.snapshot()
	return rs
}

func (rs *rescanner) snapshot() map[string]fileStamp {
	out := map[string]fileStamp{}
	walkVaultFiles(rs.vaultsDir, func(vault, rel string, info os.FileInfo) {
		if strings.HasSuffix(rel, ".md") {
			out[vault+"\x00"+rel] = fileStamp{info.ModTime().UnixNano(), info.Size()}
		}
	})
	return out
}

func (rs *rescanner) pass() (changed, removed int) {
	now := rs.snapshot()
	for key, st := range now {
		if old, ok := rs.seen[key]; ok && old == st {
			continue
		}
		vault, rel, _ := strings.Cut(key, "\x00")
		data, err := os.ReadFile(filepath.Join(rs.vaultsDir, vault, rel))
		if err != nil {
			log.Printf("rescan: %s/%s: %v", vault, rel, err)
			// Retry next pass: keep the old stamp for a known file (so it
			// is neither skipped nor evicted); drop a never-seen one.
			if old, ok := rs.seen[key]; ok {
				now[key] = old
			} else {
				delete(now, key)
			}
			continue
		}
		rs.idx.updateNote(vault, rel, string(data))
		changed++
	}
	for key := range rs.seen {
		if _, ok := now[key]; !ok {
			vault, rel, _ := strings.Cut(key, "\x00")
			rs.idx.removeNote(vault, rel)
			removed++
		}
	}
	rs.seen = now
	return changed, removed
}

func (rs *rescanner) run(stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if c, r := rs.pass(); c+r > 0 {
				log.Printf("rescan: %d changed, %d removed", c, r)
			}
		}
	}
}
