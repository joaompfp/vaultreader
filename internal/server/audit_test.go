// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
)

func readAudit(t *testing.T, s *server) []AuditEntry {
	t.Helper()
	f, err := os.Open(filepath.Join(s.appdataDir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []AuditEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func TestAuditRecordsEachWrite(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	h := s.handler()
	u := map[string]string{"Remote-User": "alice"}

	steps := []struct{ method, target, body, action string }{
		{"POST", "/api/note?vault=team&path=a.md", `{"raw":"# A"}`, "create"},
		{"PUT", "/api/note?vault=team&path=a.md", "# A2", "save"},
		{"POST", "/api/move?vault=team&from=a.md&to=sub/b.md", "", "move"},
		{"POST", "/api/folder?vault=team&path=newdir", "", "create"},
		{"DELETE", "/api/note?vault=team&path=sub/b.md", "", "delete"},
		{"POST", "/api/shares/create", `{"vault":"team","path":"x.md"}`, "share"},
	}
	for _, st := range steps {
		if rec := do(t, h, st.method, st.target, st.body, u); rec.Code >= 300 {
			t.Fatalf("%s %s = %d %s", st.method, st.target, rec.Code, rec.Body.String())
		}
	}
	got := readAudit(t, s)
	if len(got) != len(steps) {
		t.Fatalf("audit lines = %d, want %d: %+v", len(got), len(steps), got)
	}
	for i, st := range steps {
		if got[i].Action != st.action || got[i].User != "alice" || got[i].Vault != "team" || got[i].TS == 0 {
			t.Errorf("line %d = %+v, want action %s by alice", i, got[i], st.action)
		}
	}
	if got[2].Path != "a.md" || got[2].To != "sub/b.md" {
		t.Errorf("move line = %+v", got[2])
	}
	if got[1].Bytes != int64(len("# A2\n")) {
		t.Errorf("save bytes = %d", got[1].Bytes)
	}
}

func TestAuditFailureDoesNotFailRequest(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	// Make audit.jsonl a directory so the append fails.
	if err := os.MkdirAll(filepath.Join(s.appdataDir, "audit.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := do(t, s.handler(), "POST", "/api/note?vault=team&path=a.md", `{"raw":"x"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("create = %d, want 201 despite audit failure", rec.Code)
	}
}

func TestShareCreatedBy(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	rec := do(t, s.handler(), "POST", "/api/shares/create", `{"vault":"team","path":"x.md"}`,
		map[string]string{"Remote-User": "alice"})
	var e ShareEntry
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.CreatedBy != "alice" {
		t.Fatalf("created_by = %q", e.CreatedBy)
	}
}

func mpUpload(t *testing.T, vault, notePath string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("vault", vault)
	_ = mw.WriteField("notePath", notePath)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="p.png"`)
	h.Set("Content-Type", "image/png")
	pw, _ := mw.CreatePart(h)
	_, _ = pw.Write([]byte("12345"))
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

func TestAuditRemainingSites(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	h := s.handler()
	vp := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(vp, "n.md"), "# n\n")
	writeFile(t, filepath.Join(vp, "d1", "x.md"), "x\n")
	writeFile(t, filepath.Join(vp, "d2", "y.md"), "y\n")
	writeFile(t, filepath.Join(vp, "img.png"), "png")
	u := map[string]string{"Remote-User": "bob"}

	// upload
	body, ct := mpUpload(t, "team", "n.md")
	rec := do(t, h, "POST", "/api/upload", body, map[string]string{"Remote-User": "bob", "Content-Type": ct})
	if rec.Code != 200 {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}
	var up map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &up)

	// delete note then restore it
	if rec := do(t, h, "DELETE", "/api/note?vault=team&path=n.md", "", u); rec.Code >= 300 {
		t.Fatalf("delete = %d", rec.Code)
	}
	ents, _ := os.ReadDir(filepath.Join(vp, ".trash"))
	if len(ents) != 1 {
		t.Fatalf("trash entries = %d", len(ents))
	}
	trashName := ents[0].Name()
	steps := []struct{ method, target, body string }{
		{"POST", "/api/trash/restore?vault=team&path=.trash/" + trashName, ""},
		{"DELETE", "/api/attachments?vault=team&path=img.png", ""},
		{"PATCH", "/api/folder?vault=team&from=d1&to=d1b", ""},
		{"DELETE", "/api/folder?vault=team&path=d2", ""},
	}
	for _, st := range steps {
		if rec := do(t, h, st.method, st.target, st.body, u); rec.Code >= 300 {
			t.Fatalf("%s %s = %d %s", st.method, st.target, rec.Code, rec.Body.String())
		}
	}

	// shares: create, revoke, revoke-all, and a writable share-view PUT
	var e1, e2 ShareEntry
	rec = do(t, h, "POST", "/api/shares/create", `{"vault":"team","path":"n.md","writable":true}`, u)
	_ = json.Unmarshal(rec.Body.Bytes(), &e1)
	rec = do(t, h, "POST", "/api/shares/create", `{"vault":"team","path":"n.md"}`, u)
	_ = json.Unmarshal(rec.Body.Bytes(), &e2)
	if rec := do(t, h, "PUT", "/share/"+e1.Token, `{"raw":"hi"}`, nil); rec.Code != 200 {
		t.Fatalf("share put = %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/shares/revoke?token="+e2.Token, "", u); rec.Code != 200 {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if rec := do(t, h, "DELETE", "/api/shares/revoke-all", "", u); rec.Code != 200 {
		t.Fatalf("revoke-all = %d", rec.Code)
	}

	want := []AuditEntry{
		{User: "bob", Action: "upload", Vault: "team", Path: filepath.Join("attachments", "")},
		{User: "bob", Action: "delete", Vault: "team", Path: "n.md"},
		{User: "bob", Action: "restore", Vault: "team", Path: "n.md"},
		{User: "bob", Action: "delete", Vault: "team", Path: "img.png"},
		{User: "bob", Action: "move", Vault: "team", Path: "d1", To: "d1b"},
		{User: "bob", Action: "delete", Vault: "team", Path: "d2"},
		{User: "bob", Action: "share", Vault: "team", Path: "n.md"},
		{User: "bob", Action: "share", Vault: "team", Path: "n.md"},
		{User: shareActor, Action: "save", Vault: "team", Path: "n.md", Bytes: 3},
		{User: "bob", Action: "revoke", Path: e2.Token},
		{User: "bob", Action: "revoke", Path: "*"},
	}
	got := readAudit(t, s)
	if len(got) != len(want) {
		t.Fatalf("audit lines = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if i == 0 {
			if g.Action != "upload" || g.User != "bob" || g.Vault != "team" || g.Bytes != 5 ||
				g.Path != filepath.Join("attachments", filepath.Base(up["path"])) {
				t.Errorf("upload line = %+v (resp %v)", g, up)
			}
			continue
		}
		if g.User != w.User || g.Action != w.Action || g.Vault != w.Vault || g.Path != w.Path || g.To != w.To || g.Bytes != w.Bytes {
			t.Errorf("line %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestAuditNotWrittenOnFailure(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team/open"}})
	h := s.handler()
	vp := filepath.Join(s.vaultsDir, "team")
	writeFile(t, filepath.Join(vp, "open", "a.md"), "a\n")
	writeFile(t, filepath.Join(vp, "locked.md"), "l\n")

	cases := []struct {
		method, target, body string
		want                 int
	}{
		{"POST", "/api/note?vault=team&path=open/a.md", `{"raw":"x"}`, 409},
		{"POST", "/api/note?vault=team&path=locked2.md", `{"raw":"x"}`, 403},
		{"PUT", "/api/note?vault=team&path=locked.md", "x", 403},
		{"DELETE", "/api/note?vault=team&path=locked.md", "", 403},
		{"POST", "/api/move?vault=team&from=locked.md&to=open/m.md", "", 403},
		{"POST", "/api/folder?vault=team&path=nope", "", 403},
		{"POST", "/api/note?vault=nosuch&path=a.md", `{"raw":"x"}`, 400},
	}
	for _, c := range cases {
		if rec := do(t, h, c.method, c.target, c.body, nil); rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.target, rec.Code, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(s.appdataDir, "audit.jsonl")); err == nil {
		t.Fatalf("audit lines written for failed requests: %+v", readAudit(t, s))
	}
}

func TestAuditCreateBytes(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	do(t, s.handler(), "POST", "/api/note?vault=team&path=a.md", `{"raw":"# A  \n\n"}`, nil)
	got := readAudit(t, s)
	if len(got) != 1 || got[0].Bytes != int64(len("# A\n")) || got[0].User != "unknown" {
		t.Fatalf("got %+v", got)
	}
}

// /share/ routes run without forward-auth, so a client-supplied
// Remote-User there must not reach the audit log.
func TestShareSaveIgnoresRemoteUser(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"team"}})
	h := s.handler()
	writeFile(t, filepath.Join(s.vaultsDir, "team", "n.md"), "# n\n")
	var e ShareEntry
	rec := do(t, h, "POST", "/api/shares/create", `{"vault":"team","path":"n.md","writable":true}`,
		map[string]string{"Remote-User": "alice"})
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec := do(t, h, "PUT", "/share/"+e.Token, `{"raw":"hi"}`,
		map[string]string{"Remote-User": "mallory"}); rec.Code != 200 {
		t.Fatalf("share put = %d", rec.Code)
	}
	got := readAudit(t, s)
	last := got[len(got)-1]
	if last.Action != "save" || last.User != shareActor {
		t.Fatalf("share save line = %+v, want user %q", last, shareActor)
	}
}
