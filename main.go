package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

var (
	vaultsDir   = flag.String("vaults", "/vaults", "path to vaults directory")
	port        = flag.String("port", "8080", "port to listen on")
	appdataDir  = flag.String("appdata", "/appdata", "path to appdata directory (vault icons, customisations)")
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

// ─── Save ─────────────────────────────────────────────────────────────────────

func saveNote(vaultPath, notePath, content string) error {
	full := filepath.Join(vaultPath, notePath)
	// ensure directory exists
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return err
	}
	// Normalize: strip trailing whitespace per line, ensure exactly one
	// trailing newline. Reduces noise in git diffs when the same file is
	// edited from different tools (Obsidian, vim, VaultReader).
	content = normalizeMarkdown(content)
	tmp := full + ".tmp-vaultreader"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

// normalizeMarkdown applies whitespace normalization that is uncontroversial
// across markdown editors (Obsidian, vim, VS Code with default settings):
//   - strip trailing spaces/tabs from every line
//   - end the file with exactly one newline (no trailing blanks; no missing)
// Does NOT touch line endings beyond what os.WriteFile does, and does NOT
// collapse internal blank lines (markdown's blank-line semantics matter).
func normalizeMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		// rstrip space + tab; preserve other whitespace (none should appear
		// at end of a markdown line anyway).
		lines[i] = strings.TrimRight(ln, " \t")
	}
	out := strings.Join(lines, "\n")
	// Trim ALL trailing newlines, then add exactly one back.
	out = strings.TrimRight(out, "\n")
	if out != "" {
		out += "\n"
	}
	return out
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

// preferred vault display order — vaults not listed here appear alphabetically after
var vaultOrder = []string{"pessoal", "work", "pcp", "sosracismo", "projects"}

func (s *server) handleVaults(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			found[e.Name()] = true
		}
	}
	// ordered first, then any extras alphabetically
	var vaults []string
	for _, v := range vaultOrder {
		if found[v] {
			vaults = append(vaults, v)
			delete(found, v)
		}
	}
	var extra []string
	for v := range found {
		extra = append(extra, v)
	}
	sort.Strings(extra)
	vaults = append(vaults, extra...)
	jsonResponse(w, vaults)
}

func (s *server) handleTree(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	nodes, err := buildTree(vp, vp)
	if err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	jsonResponse(w, nodes)
}

func (s *server) handleNote(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetNote(w, r)
	case http.MethodPut:
		s.handlePutNote(w, r)
	case http.MethodPost:
		s.handleCreateNote(w, r)
	case http.MethodDelete:
		s.handleDeleteNote(w, r)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

func (s *server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if path == "" {
		errResponse(w, 400, "path required")
		return
	}
	// ensure .md extension
	if !strings.HasSuffix(path, ".md") {
		path = path + ".md"
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
		return
	}
	// don't overwrite existing notes
	if _, err := os.Stat(full); err == nil {
		errResponse(w, 409, "note already exists")
		return
	}

	var body struct {
		Raw string `json:"raw"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := saveNote(vp, path, body.Raw); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	s.idx.updateNote(vault, path, body.Raw)

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"status": "created", "path": path})
}

func (s *server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
		return
	}
	if _, err := os.Stat(full); os.IsNotExist(err) {
		errResponse(w, 404, "note not found")
		return
	}

	// soft-delete: move to .trash/ inside vault
	trashDir := filepath.Join(vp, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		errResponse(w, 500, "cannot create trash: "+err.Error())
		return
	}

	// Trash filename: encode the original path in base64-url so restore is
	// unambiguous. Format: VRTRASH_<b64>_<unix>.<ext>. The legacy `__`-flat
	// scheme conflicted with files whose names contained double underscores.
	trashPath := filepath.Join(trashDir, makeTrashName(path, time.Now().Unix()))
	if err := os.Rename(full, trashPath); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	s.idx.removeNote(vault, path)

	jsonResponse(w, map[string]string{"status": "deleted", "movedTo": ".trash/" + filepath.Base(trashPath)})
}

func (s *server) handleFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateFolder(w, r)
	case http.MethodDelete:
		s.handleDeleteFolder(w, r)
	case http.MethodPatch:
		s.handleRenameFolder(w, r)
	default:
		errResponse(w, 405, "method not allowed")
	}
}

func (s *server) handleRenameFolder(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if from == "" || to == "" {
		errResponse(w, 400, "from and to required")
		return
	}

	fullFrom, ok := s.safePath(vp, from)
	if !ok {
		errResponse(w, 400, "invalid from path")
		return
	}
	fullTo, ok := s.safePath(vp, to)
	if !ok {
		errResponse(w, 400, "invalid to path")
		return
	}

	info, err := os.Stat(fullFrom)
	if os.IsNotExist(err) {
		errResponse(w, 404, "folder not found")
		return
	}
	if !info.IsDir() {
		errResponse(w, 400, "path is not a folder")
		return
	}
	if _, err := os.Stat(fullTo); err == nil {
		errResponse(w, 409, "destination already exists")
		return
	}

	if err := os.MkdirAll(filepath.Dir(fullTo), 0755); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	if err := os.Rename(fullFrom, fullTo); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// update index: rekey all notes under renamed folder
	s.idx.mu.Lock()
	updates := map[string]string{} // oldPath -> newPath
	for key, ref := range s.idx.allNotes {
		if ref.Vault == vault && strings.HasPrefix(ref.Path, from+"/") {
			newPath := to + ref.Path[len(from):]
			updates[key] = newPath
		}
	}
	for oldKey, newPath := range updates {
		ref := s.idx.allNotes[oldKey]
		delete(s.idx.allNotes, oldKey)
		ref.Path = newPath
		newKey := vault + ":" + newPath
		s.idx.allNotes[newKey] = ref
	}
	s.idx.mu.Unlock()

	jsonResponse(w, map[string]string{"status": "renamed", "newPath": to})
}

func (s *server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if path == "" {
		errResponse(w, 400, "path required")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
		return
	}
	if _, err := os.Stat(full); err == nil {
		errResponse(w, 409, "folder already exists")
		return
	}
	if err := os.MkdirAll(full, 0755); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, map[string]string{"status": "created", "path": path})
}

func (s *server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if path == "" {
		errResponse(w, 400, "path required")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
		return
	}
	info, err := os.Stat(full)
	if os.IsNotExist(err) {
		errResponse(w, 404, "folder not found")
		return
	}
	if !info.IsDir() {
		errResponse(w, 400, "path is not a folder")
		return
	}

	// soft-delete: move to .trash/ inside vault
	trashDir := filepath.Join(vp, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		errResponse(w, 500, "cannot create trash: "+err.Error())
		return
	}
	trashPath := filepath.Join(trashDir, makeTrashName(path, time.Now().Unix()))
	if err := os.Rename(full, trashPath); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// remove all notes in this folder from the index
	s.idx.mu.Lock()
	prefix := vault + ":" // walk allNotes to find matching paths
	for key := range s.idx.allNotes {
		ref := s.idx.allNotes[key]
		if ref.Vault == vault && (strings.HasPrefix(ref.Path, path+"/") || ref.Path == path) {
			delete(s.idx.allNotes, key)
		}
		_ = prefix
	}
	s.idx.mu.Unlock()

	jsonResponse(w, map[string]string{"status": "deleted", "movedTo": ".trash/" + filepath.Base(trashPath)})
}

func (s *server) handleMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, 405, "method not allowed")
		return
	}
	vault := r.URL.Query().Get("vault")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if from == "" || to == "" {
		errResponse(w, 400, "from and to required")
		return
	}
	// Only enforce .md extension for files (not directories)
	fullFromCheck, okCheck := s.safePath(vp, from)
	if okCheck {
		if info, err := os.Stat(fullFromCheck); err == nil && !info.IsDir() {
			if !strings.HasSuffix(to, ".md") {
				to = to + ".md"
			}
		}
	}

	fullFrom, ok := s.safePath(vp, from)
	if !ok {
		errResponse(w, 400, "invalid from path")
		return
	}
	if !s.isWritable(vault, from) {
		errResponse(w, 403, "source path is not writable")
		return
	}
	fullTo, ok := s.safePath(vp, to)
	if !ok {
		errResponse(w, 400, "invalid to path")
		return
	}
	if !s.isWritable(vault, to) {
		errResponse(w, 403, "destination path is not writable")
		return
	}
	if _, err := os.Stat(fullFrom); os.IsNotExist(err) {
		errResponse(w, 404, "source not found")
		return
	}
	if _, err := os.Stat(fullTo); err == nil {
		errResponse(w, 409, "destination already exists")
		return
	}

	// ensure target dir exists
	if err := os.MkdirAll(filepath.Dir(fullTo), 0755); err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	if err := os.Rename(fullFrom, fullTo); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// update index: remove old, add new
	data, _ := os.ReadFile(fullTo)
	s.idx.removeNote(vault, from)
	s.idx.updateNote(vault, to, string(data))

	jsonResponse(w, map[string]string{"status": "moved", "newPath": to})
}

func (s *server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}

	data, err := os.ReadFile(full)
	if err != nil {
		errResponse(w, 404, "note not found")
		return
	}

	info, _ := os.Stat(full)
	var mtime, size int64
	if info != nil {
		mtime = info.ModTime().Unix()
		size = info.Size()
	}

	raw := string(data)
	fm, body := parseFrontmatter(raw)
	if fm == nil {
		fm = map[string]any{}
	}

	body = expandEmbeds(body, vault, path, s.idx, s.vaultsDir)
	rendered := renderMarkdown(body)
	rendered = renderCallouts(rendered)
	rendered = renderWikilinks(rendered, vault, path, s.idx, s.vaultsDir)

	backlinks := s.idx.getBacklinks(vault, path)
	if backlinks == nil {
		backlinks = []BacklinkRef{}
	}

	jsonResponse(w, NoteResponse{
		Raw:         raw,
		HTML:        rendered,
		Frontmatter: fm,
		Backlinks:   backlinks,
		MTime:       mtime,
		Size:        size,
	})
}

func (s *server) handlePutNote(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	ifMTime := r.URL.Query().Get("ifMTime") // optional: client's last-known mtime

	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if !s.isWritable(vault, path) {
		errResponse(w, 403, "path is not writable")
		return
	}

	// Conflict detection: if client supplied ifMTime, compare against on-disk.
	// 409 with the current file's content + mtime so the client can resolve.
	if ifMTime != "" {
		want, err := strconv.ParseInt(ifMTime, 10, 64)
		if err == nil && want > 0 {
			if info, errStat := os.Stat(full); errStat == nil {
				cur := info.ModTime().Unix()
				// Allow a 1-second slop for filesystems with second-precision mtime.
				if cur > want+1 {
					data, _ := os.ReadFile(full)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]any{
						"error":      "file changed on disk",
						"diskMtime":  cur,
						"diskRaw":    string(data),
					})
					return
				}
			}
		}
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		errResponse(w, 400, "cannot read body")
		return
	}
	content := buf.String()

	if err := saveNote(vp, path, content); err != nil {
		errResponse(w, 500, err.Error())
		return
	}

	// update index
	s.idx.updateNote(vault, path, content)

	// Return the new mtime so client can stay in sync without an extra GET.
	var newMTime int64
	if info, err := os.Stat(full); err == nil {
		newMTime = info.ModTime().Unix()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]int64{"mtime": newMTime})
}

// handleUpload accepts a multipart image upload tied to a note and writes
// the file under <note-dir>/attachments/<note-base>-<unix>.<ext>. Body cap
// 10MB. Same isWritable + safePath guards as handlePutNote.
func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errResponse(w, 405, "POST only")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB cap
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		errResponse(w, 400, "cannot parse form: "+err.Error())
		return
	}
	vault := r.FormValue("vault")
	notePath := r.FormValue("notePath")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	if _, ok := s.safePath(vp, notePath); !ok {
		errResponse(w, 400, "invalid notePath")
		return
	}
	if !s.isWritable(vault, notePath) {
		errResponse(w, 403, "path is not writable")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		errResponse(w, 400, "missing file")
		return
	}
	defer file.Close()

	ct := hdr.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		errResponse(w, 400, "file must be an image")
		return
	}
	ext := ""
	switch ct {
	case "image/png":
		ext = "png"
	case "image/jpeg", "image/jpg":
		ext = "jpg"
	case "image/gif":
		ext = "gif"
	case "image/webp":
		ext = "webp"
	case "image/svg+xml":
		ext = "svg"
	default:
		errResponse(w, 400, "unsupported image type: "+ct)
		return
	}

	// Compute target dir: <note-dir>/attachments
	noteDir := filepath.Dir(notePath)
	if noteDir == "." {
		noteDir = ""
	}
	relAttachDir := filepath.Join(noteDir, "attachments")
	fullAttachDir, ok := s.safePath(vp, relAttachDir)
	if !ok {
		errResponse(w, 400, "invalid attachment dir")
		return
	}
	if err := os.MkdirAll(fullAttachDir, 0755); err != nil {
		errResponse(w, 500, "cannot create attachments dir: "+err.Error())
		return
	}

	noteBase := strings.TrimSuffix(filepath.Base(notePath), ".md")
	// Sanitize: strip anything that isn't [a-zA-Z0-9_-]
	noteBase = sanitizeFilename(noteBase)
	if noteBase == "" {
		noteBase = "image"
	}
	filename := fmt.Sprintf("%s-%d.%s", noteBase, time.Now().Unix(), ext)
	relFilePath := filepath.Join(relAttachDir, filename)
	fullFilePath, ok := s.safePath(vp, relFilePath)
	if !ok {
		errResponse(w, 400, "invalid file path")
		return
	}

	out, err := os.Create(fullFilePath)
	if err != nil {
		errResponse(w, 500, "cannot create file: "+err.Error())
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		errResponse(w, 500, "cannot write file: "+err.Error())
		return
	}

	// Return path *relative to the note's directory* so the client can embed
	// it as ![[attachments/foo.png]] regardless of where the note lives.
	jsonResponse(w, map[string]string{
		"path": filepath.Join("attachments", filename),
	})
}

// sanitizeFilename keeps [a-zA-Z0-9_-], replaces anything else with '-'.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	return b.String()
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	vault := r.URL.Query().Get("vault")

	if q == "" {
		jsonResponse(w, []SearchResult{})
		return
	}

	var results []SearchResult
	if vault != "" {
		vp, ok := s.vaultPath(vault)
		if ok {
			results = searchVault(vp, vault, q)
		}
	} else {
		entries, _ := os.ReadDir(s.vaultsDir)
		for _, e := range entries {
			if e.IsDir() && !shouldSkip(e.Name()) {
				vp := filepath.Join(s.vaultsDir, e.Name())
				results = append(results, searchVault(vp, e.Name(), q)...)
				if len(results) >= 20 {
					break
				}
			}
		}
	}

	if results == nil {
		results = []SearchResult{}
	}
	jsonResponse(w, results)
}

func (s *server) handleResolve(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	vault := r.URL.Query().Get("vault")

	v, p, ok := s.idx.resolve(name, vault)
	if !ok {
		errResponse(w, 404, "not found")
		return
	}
	jsonResponse(w, ResolveResult{Vault: v, Path: p})
}

// handleTemplates lists `.md` files under <vault>/templates/. Each entry
// returns its path, display name (basename without .md), and body. The
// frontend reads the body, runs variable expansion ({{date}}, etc.), and
// POSTs a new note with the result.
func (s *server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vault := r.URL.Query().Get("vault")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	tplDir := filepath.Join(vp, "templates")
	type tplEntry struct {
		Path string `json:"path"`
		Name string `json:"name"`
		Body string `json:"body"`
	}
	out := []tplEntry{}
	_ = filepath.Walk(tplDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vp, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		out = append(out, tplEntry{
			Path: rel,
			Name: strings.TrimSuffix(info.Name(), ".md"),
			Body: string(data),
		})
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	jsonResponse(w, map[string]any{"templates": out})
}

// handleBacklinks returns just the backlinks for a note, cheaply (no
// disk read of the note itself). Used by rename/move flows to warn
// about wikilinks that would break before the user confirms.
func (s *server) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		errResponse(w, 400, "vault and path required")
		return
	}
	backlinks := s.idx.getBacklinks(vault, path)
	if backlinks == nil {
		backlinks = []BacklinkRef{}
	}
	jsonResponse(w, map[string]any{"backlinks": backlinks})
}

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

// ─── Sync status handler ───────────────────────────────────────────────────────

type SyncStatus struct {
	Available bool   `json:"available"`
	State     string `json:"state"` // "synced", "syncing", "error", "unknown"
	Message   string `json:"message,omitempty"`
}

var syncHTTPClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

func (s *server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("SYNCTHING_API_KEY")
	apiURL := os.Getenv("SYNCTHING_API_URL") // e.g. https://172.10.0.5:8384
	if apiKey == "" || apiURL == "" {
		jsonResponse(w, SyncStatus{Available: false, State: "unknown"})
		return
	}

	req, err := http.NewRequest("GET", apiURL+"/rest/db/completion", nil)
	if err != nil {
		jsonResponse(w, SyncStatus{Available: false, State: "error", Message: err.Error()})
		return
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := syncHTTPClient.Do(req)
	if err != nil {
		jsonResponse(w, SyncStatus{Available: false, State: "error", Message: "unreachable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		jsonResponse(w, SyncStatus{Available: true, State: "error", Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}

	var completion struct {
		Completion float64 `json:"completion"`
		NeedBytes  int64   `json:"needBytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		jsonResponse(w, SyncStatus{Available: true, State: "error", Message: "parse error"})
		return
	}

	if completion.NeedBytes == 0 {
		jsonResponse(w, SyncStatus{Available: true, State: "synced", Message: "Up to date"})
	} else {
		jsonResponse(w, SyncStatus{Available: true, State: "syncing",
			Message: fmt.Sprintf("%.0f%%", completion.Completion)})
	}
}

// ─── Static files ─────────────────────────────────────────────────────────────


// Trash naming: VRTRASH_<base64url(path)>_<unix><ext>. Encoding the full
// vault-relative path eliminates the ambiguity of the legacy `__`-flatten
// scheme (where files containing `__` round-tripped wrong) and supports
// arbitrary characters in original paths. The .ext is preserved at the
// end so OS file pickers / `file` command still recognize the type.
const trashSentinel = "VRTRASH_"

func makeTrashName(originalPath string, unix int64) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(originalPath))
	ext := filepath.Ext(originalPath)
	return fmt.Sprintf("%s%s_%d%s", trashSentinel, enc, unix, ext)
}

// decodeTrashName: trash basename → (originalPath, true) or ("", false).
// Falls back to the legacy `__` scheme when the sentinel isn't present so
// already-trashed files keep working.
func decodeTrashName(base string) (string, bool) {
	if !strings.HasPrefix(base, trashSentinel) {
		return "", false
	}
	rest := base[len(trashSentinel):]
	// Expected: <b64>_<unix><ext>. Last `_` separates b64 from unix+ext.
	cut := strings.LastIndex(rest, "_")
	if cut <= 0 {
		return "", false
	}
	enc := rest[:cut]
	data, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// legacyDecodeTrashName: best-effort decode for entries created before the
// sentinel scheme. Mirrors the old logic; ambiguous for files with `__` in
// real names but the old scheme can't do better.
func legacyDecodeTrashName(base string) string {
	// Strip optional `_<unix>` collision suffix.
	if idx := strings.LastIndex(base, "_"); idx > 0 {
		if _, err := strconv.ParseInt(base[idx+1:], 10, 64); err == nil {
			base = base[:idx]
		} else {
			// Maybe `_<unix>.<ext>`
			ext := filepath.Ext(base)
			noext := strings.TrimSuffix(base, ext)
			if i2 := strings.LastIndex(noext, "_"); i2 > 0 {
				if _, err := strconv.ParseInt(noext[i2+1:], 10, 64); err == nil {
					base = noext[:i2] + ext
				}
			}
		}
	}
	return strings.ReplaceAll(base, "__", "/")
}

// handleTrashList returns items in .trash/ for a vault.
func (s *server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vault := r.URL.Query().Get("vault")
	if vault == "" {
		http.Error(w, "missing vault", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	trashDir := filepath.Join(vp, ".trash")
	items := []map[string]string{}
	entries, err := os.ReadDir(trashDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				items = append(items, map[string]string{"name": e.Name() + "/", "path": ".trash/" + e.Name(), "isDir": "true"})
			} else {
				// Display name = the original vault-relative path. Decode via
				// the sentinel scheme; fall back to legacy for older entries.
				display, ok := decodeTrashName(e.Name())
				if !ok {
					display = legacyDecodeTrashName(e.Name())
				}
				items = append(items, map[string]string{"name": display, "path": ".trash/" + e.Name(), "isDir": "false"})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]interface{}{"items": items})
}

// handleTrashRestore moves a .trash/ item back to its original location.
func (s *server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		http.Error(w, "missing params", http.StatusBadRequest)
		return
	}
	// path is relative to vault root, e.g. ".trash/filename.md"
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	trashFull := filepath.Join(vp, strings.ReplaceAll(path, "/", string(os.PathSeparator)))
	// Decode the trash filename back to its original vault-relative path.
	// New scheme uses VRTRASH_<b64> and is exact; legacy `__`-flatten scheme
	// kicks in for entries created before the sentinel was introduced.
	base := filepath.Base(trashFull)
	originalBase, ok := decodeTrashName(base)
	if !ok {
		originalBase = legacyDecodeTrashName(base)
	}
	dest := filepath.Join(vp, originalBase)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		http.Error(w, "mkdir failed", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(trashFull, dest); err != nil {
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}
	// Reindex if it was a note
	if strings.HasSuffix(dest, ".md") {
		rel, _ := filepath.Rel(vp, dest)
		data, _ := os.ReadFile(dest)
		s.idx.updateNote(vault, rel, string(data))
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTrashEmpty permanently deletes .trash/ items.
func (s *server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	vault := r.URL.Query().Get("vault")
	if vault == "" {
		http.Error(w, "missing vault", http.StatusBadRequest)
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}
	trashDir := filepath.Join(vp, ".trash")
	path := r.URL.Query().Get("path")
	if path != "" {
		// Delete single item
		full := filepath.Join(vp, strings.ReplaceAll(path, "/", string(os.PathSeparator)))
		// safety check
		if !strings.HasPrefix(full, trashDir) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		os.RemoveAll(full)
	} else {
		// Empty entire trash
		os.RemoveAll(trashDir)
		os.MkdirAll(trashDir, 0755)
	}
	w.WriteHeader(http.StatusNoContent)
}

// AttachmentItem is one image file in a vault, with metadata + reference count.
type AttachmentItem struct {
	Vault    string             `json:"vault"`
	Path     string             `json:"path"`
	Name     string             `json:"name"`
	Size     int64              `json:"size"`
	MTime    int64              `json:"mtime"`
	Ext      string             `json:"ext"`
	RefCount int                `json:"refCount"`
	Refs     []AttachmentRef    `json:"refs,omitempty"` // referencing notes (cap 8)
}

type AttachmentRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".svg": true,
}

// handleAttachments lists all image files in a vault with reference counts.
// GET /api/attachments?vault=X
// DELETE /api/attachments?vault=X&path=Y → moves to .trash/
func (s *server) handleAttachments(w http.ResponseWriter, r *http.Request) {
	vault := r.URL.Query().Get("vault")
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}

	if r.Method == http.MethodDelete {
		path := r.URL.Query().Get("path")
		full, ok := s.safePath(vp, path)
		if !ok {
			errResponse(w, 400, "invalid path")
			return
		}
		if !s.isWritable(vault, path) {
			errResponse(w, 403, "path is not writable")
			return
		}
		// Move to .trash/ following the existing trash convention:
		// flatten path with `__` and append `_<unix>`.
		trashDir := filepath.Join(vp, ".trash")
		if err := os.MkdirAll(trashDir, 0755); err != nil {
			errResponse(w, 500, "mkdir failed")
			return
		}
		trashName := makeTrashName(path, time.Now().Unix())
		if err := os.Rename(full, filepath.Join(trashDir, trashName)); err != nil {
			errResponse(w, 500, "rename failed: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}

	// Walk the vault, collect images, and tally references.
	items := []AttachmentItem{}
	imagePaths := []string{}
	imageInfo := map[string]os.FileInfo{}
	mdContents := map[string]string{}

	_ = filepath.Walk(vp, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip .trash, .obsidian, hidden dirs
		rel, _ := filepath.Rel(vp, p)
		first := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
		if strings.HasPrefix(first, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if imageExtensions[ext] {
			imagePaths = append(imagePaths, rel)
			imageInfo[rel] = info
		} else if ext == ".md" {
			data, err := os.ReadFile(p)
			if err == nil {
				mdContents[rel] = string(data)
			}
		}
		return nil
	})

	// Count references. Obsidian's ![[...]] syntax can use:
	//   - basename ("foo.png" or "foo")
	//   - any suffix of the vault-relative path ("subdir/foo.png", "full/path/foo.png")
	// We try all suffixes plus also ![alt](path) for standard markdown images.
	refByImage := map[string]int{}
	// Per-image: count + a list of referencing note paths (capped). We
	// also need note titles for display — extract first-H1 (or fall back
	// to the basename) from each matched note's content.
	refsByImage := map[string][]AttachmentRef{}
	const refsCap = 8

	for imgRel := range imageInfo {
		segs := strings.Split(imgRel, string(os.PathSeparator))
		base := segs[len(segs)-1]
		baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
		patterns := []string{
			"![[" + base + "]]",
			"![[" + baseNoExt + "]]",
		}
		// Add every suffix of the path: "foo.png", "subdir/foo.png", ...
		for i := len(segs) - 1; i >= 0; i-- {
			suffix := strings.Join(segs[i:], "/")
			patterns = append(patterns,
				"![["+suffix+"]]",
				"![["+strings.TrimSuffix(suffix, filepath.Ext(suffix))+"]]",
				"]("+suffix+")",  // standard markdown ![alt](path)
			)
		}
		count := 0
		var refs []AttachmentRef
		for notePath, content := range mdContents {
			for _, pat := range patterns {
				if strings.Contains(content, pat) {
					count++
					if len(refs) < refsCap {
						_, body := parseFrontmatter(content)
						title := extractTitle(body, notePath)
						refs = append(refs, AttachmentRef{Path: notePath, Title: title})
					}
					break
				}
			}
		}
		refByImage[imgRel] = count
		if len(refs) > 0 {
			refsByImage[imgRel] = refs
		}
	}

	for _, rel := range imagePaths {
		info := imageInfo[rel]
		items = append(items, AttachmentItem{
			Vault:    vault,
			Path:     rel,
			Name:     filepath.Base(rel),
			Size:     info.Size(),
			MTime:    info.ModTime().Unix(),
			Ext:      strings.ToLower(filepath.Ext(rel)),
			RefCount: refByImage[rel],
			Refs:     refsByImage[rel],
		})
	}

	// Sort: orphans first (refCount==0), then by mtime desc.
	sort.SliceStable(items, func(i, j int) bool {
		if (items[i].RefCount == 0) != (items[j].RefCount == 0) {
			return items[i].RefCount == 0
		}
		return items[i].MTime > items[j].MTime
	})

	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  len(items),
		"orphan": len(items) - countWithRefs(items),
	})
}

func countWithRefs(items []AttachmentItem) int {
	n := 0
	for _, it := range items {
		if it.RefCount > 0 {
			n++
		}
	}
	return n
}

// handleTags returns frontmatter tags aggregated across all notes.
// GET /api/tags[?vault=X] → [{ tag: "...", count: N, vaults: [...] }, ...]
func (s *server) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	vaultFilter := r.URL.Query().Get("vault")

	type TagEntry struct {
		Tag    string   `json:"tag"`
		Count  int      `json:"count"`
		Vaults []string `json:"vaults"`
	}

	tagCount := map[string]int{}
	tagVaults := map[string]map[string]bool{}

	entries, err := os.ReadDir(s.vaultsDir)
	if err != nil {
		errResponse(w, 500, err.Error())
		return
	}
	for _, e := range entries {
		if !e.IsDir() || shouldSkip(e.Name()) {
			continue
		}
		vaultName := e.Name()
		if vaultFilter != "" && vaultName != vaultFilter {
			continue
		}
		vp := filepath.Join(s.vaultsDir, vaultName)
		_ = filepath.Walk(vp, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(vp, p)
			first := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
			if strings.HasPrefix(first, ".") {
				return nil
			}
			if !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			fm, _ := parseFrontmatter(string(data))
			if fm == nil {
				return nil
			}
			// tags: [a, b]  OR  tags: a (single string)  OR  tag: x
			collect := func(v any) {
				switch t := v.(type) {
				case string:
					if t != "" {
						tagCount[t]++
						if tagVaults[t] == nil {
							tagVaults[t] = map[string]bool{}
						}
						tagVaults[t][vaultName] = true
					}
				case []any:
					for _, item := range t {
						if s, ok := item.(string); ok && s != "" {
							tagCount[s]++
							if tagVaults[s] == nil {
								tagVaults[s] = map[string]bool{}
							}
							tagVaults[s][vaultName] = true
						}
					}
				}
			}
			if v, ok := fm["tags"]; ok {
				collect(v)
			}
			if v, ok := fm["tag"]; ok {
				collect(v)
			}
			return nil
		})
	}

	tags := make([]TagEntry, 0, len(tagCount))
	for tag, count := range tagCount {
		vaultsList := make([]string, 0, len(tagVaults[tag]))
		for v := range tagVaults[tag] {
			vaultsList = append(vaultsList, v)
		}
		sort.Strings(vaultsList)
		tags = append(tags, TagEntry{Tag: tag, Count: count, Vaults: vaultsList})
	}
	sort.SliceStable(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Tag < tags[j].Tag
	})

	jsonResponse(w, map[string]any{
		"tags":  tags,
		"total": len(tags),
	})
}

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

// newWebDAVHandler returns a read-only WebDAV handler over the vaults dir.
// Mounted at /webdav/. Only GET, HEAD, OPTIONS, PROPFIND are allowed; all
// mutating verbs (PUT, DELETE, MKCOL, COPY, MOVE, LOCK) get 405.
func (s *server) newWebDAVHandler() http.Handler {
	dav := &webdav.Handler{
		Prefix:     "/webdav",
		FileSystem: webdav.Dir(s.vaultsDir),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("webdav %s %s: %v", r.Method, r.URL.Path, err)
			}
		},
	}
	readOnlyMethods := map[string]bool{
		"GET": true, "HEAD": true, "OPTIONS": true, "PROPFIND": true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !readOnlyMethods[r.Method] {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS, PROPFIND")
			http.Error(w, "method not allowed (read-only WebDAV)", http.StatusMethodNotAllowed)
			return
		}
		dav.ServeHTTP(w, r)
	})
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(staticFiles, "static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	log.Printf("vaultreader starting — vaults=%s port=%s", *vaultsDir, *port)

	idx := newIndex()

	// Check if vaults dir exists, warn but don't fail (might be mounted later)
	if _, err := os.Stat(*vaultsDir); err != nil {
		log.Printf("WARNING: vaults dir %s not found: %v", *vaultsDir, err)
		// Create it to avoid startup failure
		_ = os.MkdirAll(*vaultsDir, 0755)
	}

	// Build index
	t0 := time.Now()
	idx.buildAll(*vaultsDir)
	log.Printf("index built in %v", time.Since(t0))

	// Ensure appdata/icons exists
	_ = os.MkdirAll(filepath.Join(*appdataDir, "icons"), 0755)

	srv := &server{
		vaultsDir:  *vaultsDir,
		appdataDir: *appdataDir,
		idx:        idx,
		shares:     newShareStore(*appdataDir),
	}
	srv.loadConfig()

	// Static files sub-FS
	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("static sub: %v", err)
	}
	staticHandler := http.FileServer(http.FS(subFS))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			srv.handleIndex(w, r)
			return
		}
		staticHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/vaults", srv.handleVaults)
	mux.HandleFunc("/api/tree", srv.handleTree)
	mux.HandleFunc("/api/note", srv.handleNote)
	mux.HandleFunc("/api/upload", srv.handleUpload)
	mux.HandleFunc("/api/move", srv.handleMove)
	mux.HandleFunc("/api/folder", srv.handleFolder)
	mux.HandleFunc("/api/search", srv.handleSearch)
	mux.HandleFunc("/api/resolve", srv.handleResolve)
	mux.HandleFunc("/api/backlinks", srv.handleBacklinks)
	mux.HandleFunc("/api/templates", srv.handleTemplates)
	mux.HandleFunc("/api/stats", srv.handleStats)
	mux.HandleFunc("/api/sync-status", srv.handleSyncStatus)
	mux.HandleFunc("/api/vault-icon", srv.handleVaultIcon)
	mux.HandleFunc("/api/file", srv.handleFile)
	// Clean note URLs: /n/<vault>/<path-with-extension>. The shell is the
	// SPA — the frontend reads location.pathname on load and fetches the
	// note via /api/note. Real URLs make right-click→new-tab, bookmarks,
	// browser back/forward, and link sharing work natively.
	mux.HandleFunc("/n/", srv.handleIndex)
	mux.HandleFunc("/api/admin/config", srv.handleAdminConfig)
	mux.HandleFunc("/api/writable-paths", srv.handleWritablePaths)
	mux.HandleFunc("/api/shares", srv.handleShareList)
	mux.HandleFunc("/api/shares/create", srv.handleShareCreate)
	mux.HandleFunc("/api/shares/revoke", srv.handleShareRevoke)
	mux.HandleFunc("/api/shares/revoke-all", srv.handleShareRevokeAll)
	mux.HandleFunc("/share/", srv.handleShareView)
	mux.HandleFunc("/api/admin/restart", srv.handleAdminRestart)
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/api/trash", srv.handleTrashList)
	mux.HandleFunc("/api/trash/restore", srv.handleTrashRestore)
	mux.HandleFunc("/api/trash/empty", srv.handleTrashEmpty)
	mux.HandleFunc("/api/attachments", srv.handleAttachments)
	mux.HandleFunc("/api/graph", srv.handleGraph)
	mux.HandleFunc("/api/tags", srv.handleTags)
	mux.Handle("/webdav/", srv.newWebDAVHandler())

	addr := ":" + *port
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      gzipMiddleware(newRateLimiter(mux, 240, time.Minute)),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	srv.shutdown = make(chan struct{})

	go func() {
		log.Printf("listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case <-sigCtx.Done():
		log.Println("shutting down (signal)")
	case <-srv.shutdown:
		log.Println("shutting down (admin)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}
