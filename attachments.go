package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ─── Attachments ──────────────────────────────────────────────────────────────

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
