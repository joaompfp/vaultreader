package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestServer builds a server over real temp dirs with one vault "jll".
func newTestServer(t *testing.T, cfg AdminConfig) *server {
	t.Helper()
	root := t.TempDir()
	vaults := filepath.Join(root, "vaults")
	appdata := filepath.Join(root, "appdata")
	for _, d := range []string{filepath.Join(vaults, "jll"), appdata} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	idx := newIndex()
	idx.buildAll(vaults)
	return &server{
		vaultsDir:  vaults,
		appdataDir: appdata,
		idx:        idx,
		cfg:        cfg,
		shares:     newShareStore(appdata),
		auditLog:   newAuditLog(appdata),
		shutdown:   make(chan struct{}),
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// do sends one request through h and returns the recorder.
func do(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
