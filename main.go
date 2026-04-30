package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
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
