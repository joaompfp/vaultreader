// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry is one line of appdata/audit.jsonl. External tools (e.g. a
// host-side git committer, see docs/team-instance.md) parse this exact
// shape — treat it as a public contract.
type AuditEntry struct {
	TS     int64  `json:"ts"`
	User   string `json:"user"`
	Action string `json:"action"` // save|create|upload|move|delete|restore|share|revoke
	Vault  string `json:"vault"`
	Path   string `json:"path"`
	To     string `json:"to,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

// shareActor is the audit user for changes made through a /share/ link.
// Those routes run without forward-auth, so any Remote-User header there
// is client-supplied and must not be recorded.
const shareActor = "share"

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
	s.auditAs(userFrom(r), action, vault, path, to, bytes)
}

// auditAs records a change under a fixed user (e.g. shareActor) instead
// of the request's Remote-User.
func (s *server) auditAs(user, action, vault, path, to string, bytes int64) {
	if s.auditLog == nil {
		return
	}
	e := AuditEntry{TS: time.Now().Unix(), User: user, Action: action,
		Vault: vault, Path: path, To: to, Bytes: bytes}
	if err := s.auditLog.append(e); err != nil {
		log.Printf("audit: %v", err)
	}
}
