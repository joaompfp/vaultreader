// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeDocx builds a minimal .docx whose word/document.xml has the given body.
func writeDocx(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body + `</w:body></w:document>`
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func replaceCmds(idx, txt string) []batchCmd {
	var c []batchCmd
	for k := 0; k < 8; k++ {
		c = append(c, batchCmd{Op: "remove", Path: "/body/p[" + idx + "]/r[1]"})
	}
	return append(c, batchCmd{Op: "add", Parent: "/body/p[" + idx + "]", Type: "run", Props: map[string]any{"text": txt}})
}

// Any template with a <w:titlePg/> title page keeps it: a paragraph whose
// style or content-control tag starts with CoverTitle/Title (any case)
// gets the note title, one starting with Date gets today; the body after
// the break is stripped.
func TestTemplateStripTitlePage(t *testing.T) {
	path := writeDocx(t,
		`<w:p><w:sdt><w:sdtPr><w:tag w:val="CoverTitle"/></w:sdtPr><w:sdtContent><w:r><w:t>Title here</w:t></w:r></w:sdtContent></w:sdt></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="dateAcme"/></w:pPr><w:r><w:t>1 Jan</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="Subtitle"/></w:pPr><w:r><w:t>keep</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:sectPr><w:titlePg/></w:sectPr></w:pPr></w:p>`+
			`<w:p><w:r><w:t>template body</w:t></w:r></w:p>`+
			`<w:tbl><w:tr><w:tc><w:p/></w:tc></w:tr></w:tbl>`)
	paras, tables, ok := scanBody(path)
	if !ok {
		t.Fatal("scanBody failed")
	}
	cmds, pIdx, tIdx := templateStripCmds(paras, tables, "My Note", "9 October 2026")
	want := append(replaceCmds("1", "My Note"), replaceCmds("2", "9 October 2026")...)
	want = append(want, batchCmd{Op: "remove", Path: "/body/p[5]"}, batchCmd{Op: "remove", Path: "/body/tbl[1]"})
	if !reflect.DeepEqual(cmds, want) {
		t.Fatalf("cmds:\n got %+v\nwant %+v", cmds, want)
	}
	if pIdx != 4 || tIdx != 0 {
		t.Fatalf("indices = %d,%d want 4,0", pIdx, tIdx)
	}
}

// Style-based placeholders (e.g. "CoverTitleAcme", "TitleX") match by
// prefix, case-insensitively.
func TestTemplateStripStylePrefix(t *testing.T) {
	path := writeDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="CoverTitleAcme"/></w:pPr><w:r><w:t>x</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="TITLEx"/></w:pPr><w:r><w:t>x</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="CoverSubtitleAcme"/><w:sectPr><w:titlePg/></w:sectPr></w:pPr></w:p>`)
	paras, tables, _ := scanBody(path)
	cmds, pIdx, _ := templateStripCmds(paras, tables, "N", "D")
	want := append(replaceCmds("1", "N"), replaceCmds("2", "N")...)
	if !reflect.DeepEqual(cmds, want) || pIdx != 3 {
		t.Fatalf("got %+v idx %d", cmds, pIdx)
	}
}

// Without a title page everything in the body is stripped.
func TestTemplateStripNoTitlePage(t *testing.T) {
	path := writeDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="CoverTitle"/></w:pPr></w:p><w:p><w:pPr><w:pStyle w:val="Date"/></w:pPr><w:r><w:t>Date</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p/></w:tc></w:tr></w:tbl>`)
	paras, tables, _ := scanBody(path)
	cmds, pIdx, tIdx := templateStripCmds(paras, tables, "N", "D")
	// No substitution at all: only the strip commands.
	want := []batchCmd{{Op: "remove", Path: "/body/p[1]"}, {Op: "remove", Path: "/body/p[1]"}, {Op: "remove", Path: "/body/tbl[1]"}}
	if !reflect.DeepEqual(cmds, want) || pIdx != 0 || tIdx != 0 {
		t.Fatalf("got %+v %d %d", cmds, pIdx, tIdx)
	}
}

// Word's built-in "Title" and "Date" paragraph styles are ordinary
// formatting, not placeholders: a title page using them is kept as-is
// (a content-control tag "Date"/"Title" still counts).
func TestTemplateStripBuiltinStylesUntouched(t *testing.T) {
	path := writeDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr><w:r><w:t>Proposal</w:t></w:r></w:p>`+
			`<w:p><w:pPr><w:pStyle w:val="Date"/></w:pPr><w:r><w:t>Date </w:t></w:r></w:p>`+
			`<w:p><w:sdt><w:sdtPr><w:tag w:val="Date"/></w:sdtPr><w:sdtContent><w:r><w:t>d</w:t></w:r></w:sdtContent></w:sdt></w:p>`+
			`<w:p><w:pPr><w:sectPr><w:titlePg/></w:sectPr></w:pPr></w:p>`+
			`<w:p><w:r><w:t>body</w:t></w:r></w:p>`)
	paras, tables, _ := scanBody(path)
	cmds, pIdx, _ := templateStripCmds(paras, tables, "N", "D")
	want := append(replaceCmds("3", "D"), batchCmd{Op: "remove", Path: "/body/p[5]"})
	if !reflect.DeepEqual(cmds, want) || pIdx != 4 {
		t.Fatalf("got %+v idx %d", cmds, pIdx)
	}
}

// export_docx is on only when officecli is installed: without it the
// feature reports off and both export routes 404.
func TestExportDocxFeatureFollowsOfficecli(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no officecli here
	s := newTestServer(t, AdminConfig{})
	if s.resolvedFeatures()["export_docx"] {
		t.Fatal("export_docx enabled without officecli on PATH")
	}
	for _, p := range []string{"/api/note/export?vault=team&path=a.md&format=docx", "/api/templates/docx?vault=team&path=a.md"} {
		if rec := do(t, s.handler(), "GET", p, "", nil); rec.Code != 404 {
			t.Fatalf("%s: code %d, want 404", p, rec.Code)
		}
	}

	withFakeOfficecli(t)
	if !s.resolvedFeatures()["export_docx"] {
		t.Fatal("export_docx disabled although officecli is on PATH")
	}
	off := newTestServer(t, AdminConfig{Features: map[string]bool{"export_docx": false}})
	if off.resolvedFeatures()["export_docx"] {
		t.Fatal("config export_docx=false ignored")
	}
}
