// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry is one line of appdata/audit.jsonl. The host-side committer
// (docker repo: scripts/vaults/vault_commit_authors.py) parses this exact
// shape to attribute git commits — change both together.
type AuditEntry struct {
	TS     int64  `json:"ts"`
	User   string `json:"user"`
	Action string `json:"action"` // save|create|upload|move|delete|restore|share|revoke
	Vault  string `json:"vault"`
	Path   string `json:"path"`
	To     string `json:"to,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type auditLog struct {
	mu   sync.Mutex
	path string
}

func newAuditLog(appdataDir string) *auditLog {
	return &auditLog{path: filepath.Join(appdataDir, "audit.jsonl")}
}

func (a *auditLog) append(e AuditEntry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// audit records who changed what. Attribution is not security: a failed
// append is logged and the request carries on.
func (s *server) audit(r *http.Request, action, vault, path, to string, bytes int64) {
	if s.auditLog == nil {
		return
	}
	e := AuditEntry{TS: time.Now().Unix(), User: userFrom(r), Action: action,
		Vault: vault, Path: path, To: to, Bytes: bytes}
	if err := s.auditLog.append(e); err != nil {
		log.Printf("audit: %v", err)
	}
}
