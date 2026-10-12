// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"path/filepath"
	"time"
)

// Options are the startup settings cmd/vaultreader reads from its flags.
type Options struct {
	VaultsDir  string
	Port       string
	AppdataDir string
	Version    string // build stamp reported by /api/instance and /health
}

// ─── Run ──────────────────────────────────────────────────────────────────────

// Run builds the index, serves HTTP and blocks until a signal or an admin
// shutdown request stops the server.
func Run(o Options) {
	if o.Version != "" {
		version = o.Version
	}

	log.Printf("vaultreader starting — vaults=%s port=%s", o.VaultsDir, o.Port)

	if !officecliInstalled() {
		log.Printf("officecli not found on PATH — DOCX export disabled")
	}

	idx := newIndex()

	// Check if vaults dir exists, warn but don't fail (might be mounted later)
	if _, err := os.Stat(o.VaultsDir); err != nil {
		log.Printf("WARNING: vaults dir %s not found: %v", o.VaultsDir, err)
		// Create it to avoid startup failure
		_ = os.MkdirAll(o.VaultsDir, 0755)
	}

	// Snapshot file stamps BEFORE the build: a file changed while buildAll
	// walks then differs from its stamp and is re-read on the first rescan
	// pass (snapshotting after would bake the stale read in as "seen").
	rescan := newRescanner(o.VaultsDir, idx)

	// Build index
	t0 := time.Now()
	idx.buildAll(o.VaultsDir)
	log.Printf("index built in %v", time.Since(t0))

	// Ensure appdata/icons exists
	_ = os.MkdirAll(filepath.Join(o.AppdataDir, "icons"), 0755)

	srv := &server{
		vaultsDir:  o.VaultsDir,
		appdataDir: o.AppdataDir,
		idx:        idx,
		shares:     newShareStore(o.AppdataDir),
		auditLog:   newAuditLog(o.AppdataDir),
	}
	srv.loadConfig()
	srv.shutdown = make(chan struct{})
	go rescan.run(srv.shutdown, 30*time.Second)

	addr := ":" + o.Port
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv.handler(),
		ReadTimeout: 30 * time.Second,
		// Generous WriteTimeout because /api/note/export shells out to
		// officecli + processes 2 MB corporate templates: a 100-command batch
		// over a strip + reapply on a heavy template can take 30-90s
		// cold. Other endpoints respond in ms — they don't suffer from
		// the higher cap.
		WriteTimeout: 180 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

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
