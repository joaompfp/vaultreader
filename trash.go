package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ─── Trash ────────────────────────────────────────────────────────────────────


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
