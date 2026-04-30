package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ─── File tree ────────────────────────────────────────────────────────────────

func shouldSkip(name string) bool {
	return strings.HasPrefix(name, ".") ||
		strings.Contains(name, ".sync-conflict-") ||
		strings.HasSuffix(name, ".tmp-vaultreader")
}

func buildTree(root, current string) ([]*TreeNode, error) {
	entries, err := os.ReadDir(current)
	if err != nil {
		return nil, err
	}

	var nodes []*TreeNode
	// dirs first, then files. Files include any non-skipped entry (not
	// just .md) so the sidebar reflects the real folder contents — image
	// attachments, PDFs, plaintext, etc. The frontend dispatches on Ext
	// to render or download appropriately.
	var dirs, files []os.DirEntry
	for _, e := range entries {
		if shouldSkip(e.Name()) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	for _, e := range dirs {
		rel, _ := filepath.Rel(root, filepath.Join(current, e.Name()))
		children, _ := buildTree(root, filepath.Join(current, e.Name()))
		var mtime int64
		if info, err := e.Info(); err == nil {
			mtime = info.ModTime().Unix()
		}
		nodes = append(nodes, &TreeNode{
			Name:     e.Name(),
			Path:     rel,
			IsDir:    true,
			MTime:    mtime,
			Children: children,
		})
	}
	for _, e := range files {
		rel, _ := filepath.Rel(root, filepath.Join(current, e.Name()))
		var mtime, size int64
		if info, err := e.Info(); err == nil {
			mtime = info.ModTime().Unix()
			size = info.Size()
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		// Empty Ext for .md so existing frontend code paths that key on
		// "no ext means note" stay correct without touching them.
		jsonExt := ext
		if ext == ".md" {
			jsonExt = ""
		}
		nodes = append(nodes, &TreeNode{
			Name:  e.Name(),
			Path:  rel,
			IsDir: false,
			Ext:   jsonExt,
			MTime: mtime,
			Size:  size,
		})
	}
	return nodes, nil
}

// handleFile serves an arbitrary file from inside a vault. Used by image
// embeds (`![[...]]` rewritten to `/api/file?vault=X&path=Y`) but also
// available for downloading any tracked attachment.
func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		http.Error(w, "missing vault or path", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.NotFound(w, r)
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	if ct := mime.TypeByExtension(ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, full)
}

// handleVaultIcon serves a custom icon from appdata/icons/<vault>.(png|svg|jpg|webp)
// Falls back to a transparent 1x1 PNG if no custom icon exists.
func (s *server) handleVaultIcon(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	if vault == "" || strings.Contains(vault, "..") || strings.Contains(vault, "/") {
		http.NotFound(w, r)
		return
	}
	iconsDir := filepath.Join(s.appdataDir, "icons")
	for _, ext := range []string{".png", ".svg", ".jpg", ".webp"} {
		p := filepath.Join(iconsDir, vault+ext)
		if _, err := os.Stat(p); err == nil {
			switch ext {
			case ".svg":
				w.Header().Set("Content-Type", "image/svg+xml")
			case ".jpg":
				w.Header().Set("Content-Type", "image/jpeg")
			case ".webp":
				w.Header().Set("Content-Type", "image/webp")
			default:
				w.Header().Set("Content-Type", "image/png")
			}
			w.Header().Set("Cache-Control", "max-age=3600")
			http.ServeFile(w, r, p)
			return
		}
	}
	// No icon found — return 204 so frontend knows to use fallback
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) vaultPath(name string) (string, bool) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", false
	}
	cleanName := filepath.Clean(name)
	if strings.Contains(cleanName, "..") || strings.HasPrefix(cleanName, ".") {
		return "", false
	}
	p := filepath.Join(s.vaultsDir, cleanName)
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return p, true
}

func (s *server) safePath(vaultP, notePath string) (string, bool) {
	// Reject empty paths and absolute paths (leading separator or drive letter).
	if notePath == "" {
		return "", false
	}
	if strings.HasPrefix(notePath, "/") || strings.HasPrefix(notePath, "\\") {
		return "", false
	}
	// Canonicalise before any resolution to eliminate .., ., duplicate slashes.
	full := filepath.Clean(filepath.Join(vaultP, notePath))
	vaultBase := filepath.Clean(vaultP)
	// On Windows filepath.Clean("C:\foo") returns "C:\foo" while filepath.Clean("foo") stays relative.
	// Ensure the resolved path is still inside the vault directory.
	rel, err := filepath.Rel(vaultBase, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	if full != vaultBase && !strings.HasPrefix(full, vaultBase+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}
