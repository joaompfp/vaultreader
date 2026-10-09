package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"path/filepath"
	"time"
)

var (
	vaultsDir   = flag.String("vaults", "/vaults", "path to vaults directory")
	port        = flag.String("port", "8080", "port to listen on")
	appdataDir  = flag.String("appdata", "/appdata", "path to appdata directory (vault icons, customisations)")
)

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

	// Snapshot file stamps BEFORE the build: a file changed while buildAll
	// walks then differs from its stamp and is re-read on the first rescan
	// pass (snapshotting after would bake the stale read in as "seen").
	rescan := newRescanner(*vaultsDir, idx)

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
		auditLog:   newAuditLog(*appdataDir),
	}
	srv.loadConfig()
	srv.shutdown = make(chan struct{})
	go rescan.run(srv.shutdown, 30*time.Second)

	addr := ":" + *port
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv.handler(),
		ReadTimeout: 30 * time.Second,
		// Generous WriteTimeout because /api/note/export shells out to
		// officecli + processes 2 MB JLL templates: a 100-command batch
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
