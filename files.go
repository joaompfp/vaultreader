// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

// ─── File tree ────────────────────────────────────────────────────────────────

func shouldSkip(name string) bool {
	return strings.HasPrefix(name, ".") ||
		name == "node_modules" ||
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
	setRawFileHeaders(w, strings.ToLower(filepath.Ext(full)))
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, full)
}

// passiveFileTypes are the only types served as themselves: media and PDF
// that a browser renders without running vault-supplied script. Hardcoded
// so the result never depends on the host's /etc/mime.types.
var passiveFileTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".avif": "image/avif", ".ico": "image/x-icon",
	".pdf": "application/pdf",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".flac": "audio/flac", ".m4a": "audio/mp4", ".aac": "audio/aac",
	".opus": "audio/ogg",
	".mp4":  "video/mp4", ".webm": "video/webm", ".mkv": "video/x-matroska",
	".mov": "video/quicktime", ".avi": "video/x-msvideo",
}

// textFileExts are served as text/plain so the SPA's text viewer can read
// them; markup (.html, .xml, …) is deliberately included so it shows as
// source instead of rendering.
var textFileExts = map[string]bool{
	".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
	".csv": true, ".tsv": true, ".log": true, ".ini": true, ".conf": true,
	".toml": true, ".sh": true, ".py": true, ".js": true, ".ts": true,
	".go": true, ".rs": true, ".css": true, ".sql": true, ".env": true,
	".tsx": true, ".jsx": true, ".java": true, ".c": true, ".cpp": true,
	".h": true, ".hpp": true, ".scss": true,
	".xml": true, ".html": true, ".htm": true, ".xhtml": true,
}

// setRawFileHeaders hardens every response that streams a vault file's
// bytes. Vault files are untrusted (the team instance lets a colleague
// write them), so the Content-Type is always ours — never sniffed by
// http.ServeFile — and anything outside passiveFileTypes is served under
// a sandbox CSP: an opaque origin that cannot reach the API with the
// viewer's session. SVG keeps image/svg+xml so <img> embeds work; the
// sandbox only matters when it is opened directly. ext is lower-case
// with the leading dot.
func setRawFileHeaders(w http.ResponseWriter, ext string) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if ct, ok := passiveFileTypes[ext]; ok {
		h.Set("Content-Type", ct)
		return
	}
	h.Set("Content-Security-Policy", "sandbox")
	switch {
	case ext == ".svg":
		h.Set("Content-Type", "image/svg+xml")
	case textFileExts[ext]:
		h.Set("Content-Type", "text/plain; charset=utf-8")
	default:
		h.Set("Content-Type", "application/octet-stream")
	}
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
			w.Header().Set("X-Content-Type-Options", "nosniff")
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

// cleanRel canonicalises a vault-relative path taken from a request:
// slash form, path.Clean, never empty, the vault root, absolute or
// escaping the vault. Mutating handlers apply it before anything is
// stored, so "./rw/a.md" and "rw//a.md" are both "rw/a.md".
func cleanRel(p string) (string, bool) {
	c := pathpkg.Clean(filepath.ToSlash(p))
	if p == "" || c == "." || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
		return "", false
	}
	return c, true
}

// hasDotSegment reports whether a vault-relative path (cleaned, slash
// form) has any segment starting with "." — .git, .obsidian, .trash and
// the like. Such paths are never writable through the API: they hold code
// that git hooks or desktop plugins run. The trash code writes .trash
// itself, by its own paths, never through a request path.
func hasDotSegment(rel string) bool {
	if rel == "." {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// hiddenPathMsg is the 403 message for a refused write: why a hidden path
// was refused, or fallback for an ordinary rw_paths refusal.
func hiddenPathMsg(path, fallback string) string {
	if hasDotSegment(pathpkg.Clean(filepath.ToSlash(path))) {
		return "hidden paths (a segment starting with \".\") are not writable"
	}
	return fallback
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
