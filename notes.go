package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

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

// ─── Notes & folders ──────────────────────────────────────────────────────────

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
	type rekey struct{ oldPath, newPath string }
	var rekeyed []rekey
	for pk, ref := range s.idx.byPath {
		if ref.Vault == vault && strings.HasPrefix(ref.Path, from+"/") {
			rekeyed = append(rekeyed, rekey{ref.Path, to + ref.Path[len(from):]})
			_ = pk
		}
	}
	for _, rk := range rekeyed {
		nname := normalizeName(filepath.Base(rk.oldPath))
		s.idx.byName[nname] = removeRef(s.idx.byName[nname], vault, rk.oldPath)
		delete(s.idx.byPath, pathKey(vault, rk.oldPath))

		ref := NoteRef{Vault: vault, Path: rk.newPath, Title: strings.TrimSuffix(filepath.Base(rk.newPath), ".md")}
		s.idx.byName[normalizeName(filepath.Base(rk.newPath))] = append(s.idx.byName[normalizeName(filepath.Base(rk.newPath))], ref)
		s.idx.byPath[pathKey(vault, rk.newPath)] = ref
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
	for pk, ref := range s.idx.byPath {
		if ref.Vault == vault && (strings.HasPrefix(ref.Path, path+"/") || ref.Path == path) {
			s.idx.byName[normalizeName(filepath.Base(ref.Path))] = removeRef(s.idx.byName[normalizeName(filepath.Base(ref.Path))], vault, ref.Path)
			delete(s.idx.byPath, pk)
		}
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

	v, p, ok := s.idx.resolve(name, vault, "")
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
