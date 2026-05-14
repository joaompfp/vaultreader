package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// ─── Export ────────────────────────────────────────────────────────────────────
//
// Markdown → .docx via officecli's batch JSON. Pipeline:
//   1. Read note from disk.
//   2. Strip frontmatter, pre-resolve `![[…]]` embeds to disk paths and
//      `[[…]]` wikilinks to plain text (recipient has no vault to navigate).
//   3. Parse with the shared goldmark parser (`md.Parser()`).
//   4. Walk the AST emitting officecli batch commands.
//   5. Exec `officecli create out.docx && officecli batch out.docx --input b.json`.
//   6. Stream the resulting .docx back to the client.
//
// officecli must be on $PATH inside the container.

const officecliBin = "officecli"

type batchCmd struct {
	Op     string         `json:"op"`
	Parent string         `json:"parent"`
	Type   string         `json:"type"`
	Props  map[string]any `json:"props,omitempty"`
}

type docxCtx struct {
	cmds     []batchCmd
	source   []byte
	paraIdx  int // ordinal of last /body/p created (1-based)
	tableIdx int // ordinal of last /body/tbl created (1-based)

	vault     string
	notePath  string
	idx       *NoteIndex
	vaultsDir string
}

type fmtState struct {
	bold     bool
	italic   bool
	code     bool
	strike   bool
	heading  int // 1-6, 0 = body. Drives run-level size+bold so headings
	             // render visibly even when the blank template's HeadingN
	             // style definitions are minimal (officecli's `create`).
}

// headingSizes is the run-level font size used inside a heading paragraph,
// keyed by 1-based heading level. Word's defaults for Heading1 are 16-20pt;
// we lean a hair larger so the visual hierarchy reads at a glance.
var headingSizes = map[int]string{
	1: "20pt",
	2: "16pt",
	3: "14pt",
	4: "12pt",
	5: "11pt",
	6: "11pt",
}

func (st fmtState) props() map[string]any {
	p := map[string]any{}
	bold := st.bold
	if st.heading > 0 {
		bold = true
		if sz, ok := headingSizes[st.heading]; ok {
			p["size"] = sz
		}
	}
	if bold {
		p["bold"] = "true"
	}
	if st.italic {
		p["italic"] = "true"
	}
	if st.strike {
		p["strike"] = "true"
	}
	if st.code {
		p["font"] = "Consolas"
		p["highlight"] = "lightGray"
	}
	return p
}

func (c *docxCtx) addParagraph(props map[string]any) int {
	c.paraIdx++
	if props == nil {
		props = map[string]any{}
	}
	c.cmds = append(c.cmds, batchCmd{Op: "add", Parent: "/body", Type: "paragraph", Props: props})
	return c.paraIdx
}

func (c *docxCtx) addRun(paraIdx int, txt string, props map[string]any) {
	if txt == "" {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	p := make(map[string]any, len(props)+1)
	for k, v := range props {
		p[k] = v
	}
	p["text"] = txt
	c.cmds = append(c.cmds, batchCmd{
		Op: "add", Parent: fmt.Sprintf("/body/p[%d]", paraIdx), Type: "run", Props: p,
	})
}

func (c *docxCtx) addHyperlink(paraIdx int, url, txt string) {
	if txt == "" {
		txt = url
	}
	c.cmds = append(c.cmds, batchCmd{
		Op: "add", Parent: fmt.Sprintf("/body/p[%d]", paraIdx), Type: "hyperlink",
		Props: map[string]any{"url": url, "text": txt, "rStyle": "Hyperlink"},
	})
}

func (c *docxCtx) addPicture(paraIdx int, src string) {
	c.cmds = append(c.cmds, batchCmd{
		Op: "add", Parent: fmt.Sprintf("/body/p[%d]", paraIdx), Type: "picture",
		Props: map[string]any{"src": src, "width": "12cm"},
	})
}

// resolveImageDest takes whatever appeared inside `![alt](here)` (or the
// pre-expanded `![[...]]` form) and returns an absolute disk path that
// officecli can open. Returns "" when the target is remote (http/https),
// an /api/file URL (we don't want to round-trip through HTTP), or simply
// doesn't exist.
func (c *docxCtx) resolveImageDest(dest string) string {
	if dest == "" {
		return ""
	}
	// file://abs — strip prefix, accept as-is.
	if strings.HasPrefix(dest, "file://") {
		return strings.TrimPrefix(dest, "file://")
	}
	// Remote URLs and the SPA's own /api/file proxy — skip.
	if strings.HasPrefix(dest, "http://") ||
		strings.HasPrefix(dest, "https://") ||
		strings.HasPrefix(dest, "/api/") {
		return ""
	}
	// Absolute path on disk (rare for vault content).
	if strings.HasPrefix(dest, "/") {
		if _, err := os.Stat(dest); err == nil {
			return dest
		}
		return ""
	}
	// Relative path — try (a) note's directory, (b) vault root, (c) the
	// attachment index keyed by basename.
	noteDir := filepath.Dir(c.notePath)
	if noteDir == "." {
		noteDir = ""
	}
	vaultRoot := filepath.Join(c.vaultsDir, c.vault)
	for _, base := range []string{filepath.Join(vaultRoot, noteDir), vaultRoot} {
		try := filepath.Join(base, dest)
		if _, err := os.Stat(try); err == nil {
			return try
		}
	}
	// Fall back to the attachment index by basename.
	if v, p, ok := resolveEmbed(filepath.Base(dest), c.vault, noteDir, c.idx, c.vaultsDir); ok {
		return filepath.Join(c.vaultsDir, v, p)
	}
	return ""
}

func (c *docxCtx) addEquation(latex string, display bool) {
	if display {
		c.cmds = append(c.cmds, batchCmd{
			Op: "add", Parent: "/body", Type: "equation",
			Props: map[string]any{"formula": latex, "mode": "display"},
		})
	}
}

// resolveWikilinksPlain rewrites `[[name|alias]]` to plain text (alias or
// name). Recipient of the .docx has no vault, so we don't create hyperlinks.
// Must be applied to the raw markdown BEFORE goldmark parses it — goldmark
// splits the `[[…]]` tokens across multiple Text nodes (its link parser
// snags the first `[abc]`), making a post-parse regex unreliable.
func resolveWikilinksPlain(s string) string {
	return wikilinkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := wikilinkRe.FindStringSubmatch(m)
		target, alias := sub[1], sub[2]
		if alias != "" {
			return alias
		}
		// strip section/block fragment: "Target#Heading" → "Target"
		if i := strings.IndexAny(target, "#^"); i >= 0 {
			return target[:i]
		}
		return target
	})
}

// expandEmbedsForDocx rewrites `![[target]]` to either:
//   - `![alt](file:///abs/disk/path)` for image-like attachments (so goldmark
//     produces an ast.Image with the disk path)
//   - plain placeholder text for non-image embeds (we don't transclude notes)
func expandEmbedsForDocx(raw, currentVault, currentNotePath string, idx *NoteIndex, vaultsDir string) string {
	noteDir := filepath.Dir(currentNotePath)
	if noteDir == "." {
		noteDir = ""
	}
	return embedRe.ReplaceAllStringFunc(raw, func(match string) string {
		sub := embedRe.FindStringSubmatch(match)
		target, alias := sub[1], sub[2]
		display := alias
		if display == "" {
			display = target
		}
		if !imageExtRe.MatchString(target) {
			return fmt.Sprintf("_[embed: %s]_", display)
		}
		v, p, ok := resolveEmbed(target, currentVault, noteDir, idx, vaultsDir)
		if !ok {
			return fmt.Sprintf("_[image missing: %s]_", display)
		}
		abs := filepath.Join(vaultsDir, v, p)
		return fmt.Sprintf("![%s](file://%s)", display, abs)
	})
}

// stripFrontmatter removes a leading `---\n…\n---\n` block.
func stripFrontmatter(raw string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	// look for closing fence
	rest := raw[3:]
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return raw
	}
	after := rest[end+4:]
	after = strings.TrimLeft(after, "\r\n")
	return after
}

// ─── Walker ────────────────────────────────────────────────────────────────────

func (c *docxCtx) walkBlock(node ast.Node) {
	switch n := node.(type) {
	case *ast.Heading:
		level := n.Level
		if level > 6 {
			level = 6
		}
		// Paragraph also gets the HeadingN style for any template that
		// actually defines it, and a roomier spaceBefore so headings
		// breathe.
		props := map[string]any{
			"style":       fmt.Sprintf("Heading%d", level),
			"spaceBefore": "12pt",
			"spaceAfter":  "4pt",
		}
		idx := c.addParagraph(props)
		c.walkInline(n, idx, fmtState{heading: level})

	case *ast.Paragraph:
		idx := c.addParagraph(nil)
		c.walkInline(n, idx, fmtState{})

	case *ast.List:
		listStyle := "bullet"
		if n.IsOrdered() {
			listStyle = "ordered"
		}
		c.walkListItems(n, listStyle, 0)

	case *ast.Blockquote:
		// Detect Obsidian callout: first paragraph starts with `[!type]`
		isCallout := false
		first := n.FirstChild()
		if p, ok := first.(*ast.Paragraph); ok && p.FirstChild() != nil {
			if t, ok := p.FirstChild().(*ast.Text); ok {
				if strings.HasPrefix(string(t.Segment.Value(c.source)), "[!") {
					isCallout = true
				}
			}
		}
		_ = isCallout
		// Render each inner block as Quote-styled paragraph.
		for child := first; child != nil; child = child.NextSibling() {
			if p, ok := child.(*ast.Paragraph); ok {
				idx := c.addParagraph(map[string]any{"style": "Quote"})
				c.walkInline(p, idx, fmtState{})
				continue
			}
			c.walkBlock(child)
		}

	case *ast.FencedCodeBlock:
		info := ""
		if n.Info != nil {
			info = string(n.Info.Segment.Value(c.source))
		}
		// Mermaid placeholder (v1 ships without server-side mermaid render)
		if strings.HasPrefix(strings.ToLower(info), "mermaid") {
			idx := c.addParagraph(map[string]any{"style": "Quote"})
			c.addRun(idx, "[Mermaid diagram — rendering not yet supported]", map[string]any{"italic": "true"})
			return
		}
		c.emitCodeLines(n.Lines())

	case *ast.CodeBlock:
		c.emitCodeLines(n.Lines())

	case *ast.ThematicBreak:
		idx := c.addParagraph(map[string]any{"align": "center"})
		c.addRun(idx, "———", nil)

	case *east.Table:
		c.emitTable(n)

	default:
		// Document, lists, generic containers — recurse.
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			c.walkBlock(child)
		}
	}
}

func (c *docxCtx) walkListItems(list *ast.List, listStyle string, depth int) {
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		li, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		for sub := li.FirstChild(); sub != nil; sub = sub.NextSibling() {
			switch s := sub.(type) {
			case *ast.TextBlock, *ast.Paragraph:
				props := map[string]any{"listStyle": listStyle}
				if depth > 0 {
					props["numLevel"] = strconv.Itoa(depth)
				}
				idx := c.addParagraph(props)
				c.walkInline(s, idx, fmtState{})
			case *ast.List:
				nextStyle := listStyle
				if s.IsOrdered() {
					nextStyle = "ordered"
				} else {
					nextStyle = "bullet"
				}
				c.walkListItems(s, nextStyle, depth+1)
			default:
				c.walkBlock(s)
			}
		}
	}
}

func (c *docxCtx) emitCodeLines(lines *text.Segments) {
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		line := seg.Value(c.source)
		s := strings.TrimRight(string(line), "\r\n")
		idx := c.addParagraph(map[string]any{
			"font":        "Consolas",
			"size":        "10pt",
			"spaceBefore": "0pt",
			"spaceAfter":  "0pt",
		})
		if s == "" {
			s = " "
		}
		c.addRun(idx, s, nil)
	}
}

// emitTable adds a proper docx table. Pre-allocates the column grid via
// colWidths so officecli auto-creates the right number of cells per row;
// then each cell's content is emitted as a run on its auto-created
// paragraph. Adding cells manually causes officecli to APPEND beyond the
// table grid (rows grow exponentially) — don't.
func (c *docxCtx) emitTable(tbl *east.Table) {
	// Count columns from the first row.
	colCount := 0
	if first := tbl.FirstChild(); first != nil {
		for cell := first.FirstChild(); cell != nil; cell = cell.NextSibling() {
			colCount++
		}
	}
	if colCount == 0 {
		return
	}
	c.tableIdx++
	tblPath := fmt.Sprintf("/body/tbl[%d]", c.tableIdx)

	// Equal-width columns sized to fit a Letter/A4 content area
	// (~9000 twips ≈ 15.9cm). Cap per-column at 3000 so 2- or 3-column
	// tables don't sprawl, but shrink for wider tables so the table
	// doesn't overflow the page edge. `layout: autofit` lets Word
	// further redistribute based on content.
	const maxRowTwips = 9000
	const maxColTwips = 3000
	perCol := maxRowTwips / colCount
	if perCol > maxColTwips {
		perCol = maxColTwips
	}
	colW := fmt.Sprintf("%d", perCol)
	widths := make([]string, colCount)
	for i := range widths {
		widths[i] = colW
	}
	c.cmds = append(c.cmds, batchCmd{
		Op: "add", Parent: "/body", Type: "table",
		Props: map[string]any{
			"align":     "left",
			"colWidths": strings.Join(widths, ","),
			"layout":    "autofit",
		},
	})

	rowIdx := 0
	for row := tbl.FirstChild(); row != nil; row = row.NextSibling() {
		rowIdx++
		_, isHeader := row.(*east.TableHeader)
		rowProps := map[string]any{}
		if isHeader {
			rowProps["header"] = "true"
		}
		// officecli's `add table` auto-creates the first row to match
		// colWidths. Don't add it again — just populate it. The
		// `header=true` attribute (used for repeat-on-page-break) isn't
		// settable on the auto-created row from a batch JSON, but the
		// header content is still bolded via the per-run props below.
		if rowIdx > 1 {
			c.cmds = append(c.cmds, batchCmd{
				Op: "add", Parent: tblPath, Type: "row", Props: rowProps,
			})
		}

		// Cells pre-exist (table grid auto-populates them). Address each
		// cell's auto-created paragraph and add a run.
		colIdx := 0
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			colIdx++
			if colIdx > colCount {
				break
			}
			text := c.inlineText(cell)
			runProps := map[string]any{"text": text}
			if isHeader {
				runProps["bold"] = "true"
			}
			cellParaPath := fmt.Sprintf("%s/tr[%d]/tc[%d]/p[1]", tblPath, rowIdx, colIdx)
			c.cmds = append(c.cmds, batchCmd{
				Op: "add", Parent: cellParaPath, Type: "run", Props: runProps,
			})
		}
	}
}

// inlineText flattens an inline subtree to plain text (no formatting).
func (c *docxCtx) inlineText(node ast.Node) string {
	var sb strings.Builder
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch x := n.(type) {
		case *ast.Text:
			sb.Write(x.Segment.Value(c.source))
		case *ast.String:
			sb.Write(x.Value)
		}
		return ast.WalkContinue, nil
	})
	return resolveWikilinksPlain(sb.String())
}

func (c *docxCtx) walkInline(parent ast.Node, paraIdx int, st fmtState) {
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch n := child.(type) {
		case *ast.Text:
			c.addRun(paraIdx, string(n.Segment.Value(c.source)), st.props())
			if n.HardLineBreak() {
				c.addRun(paraIdx, "\n", st.props())
			} else if n.SoftLineBreak() {
				// Markdown wrap → space (HTML semantics).
				c.addRun(paraIdx, " ", st.props())
			}
		case *ast.String:
			c.addRun(paraIdx, string(n.Value), st.props())
		case *ast.Emphasis:
			ns := st
			if n.Level >= 2 {
				ns.bold = true
			} else {
				ns.italic = true
			}
			c.walkInline(n, paraIdx, ns)
		case *east.Strikethrough:
			ns := st
			ns.strike = true
			c.walkInline(n, paraIdx, ns)
		case *ast.CodeSpan:
			ns := st
			ns.code = true
			c.walkInline(n, paraIdx, ns)
		case *ast.Link:
			url := string(n.Destination)
			c.addHyperlink(paraIdx, url, c.inlineText(n))
		case *ast.AutoLink:
			url := string(n.URL(c.source))
			c.addHyperlink(paraIdx, url, url)
		case *ast.Image:
			dest := string(n.Destination)
			src := c.resolveImageDest(dest)
			if src == "" {
				// Couldn't resolve to a disk file (remote URL, /api/file, or
				// path not found). Emit a placeholder so officecli doesn't
				// abort on a missing file.
				c.addRun(paraIdx, "[image: "+filepath.Base(dest)+"]", map[string]any{"italic": "true"})
				continue
			}
			c.addPicture(paraIdx, src)
		default:
			c.walkInline(n, paraIdx, st)
		}
	}
}

// ─── HTTP handler ──────────────────────────────────────────────────────────────

func (s *server) handleExportNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errResponse(w, 405, "method not allowed")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "docx"
	}
	if format != "docx" {
		errResponse(w, 400, "unsupported format: "+format)
		return
	}

	vault := r.URL.Query().Get("vault")
	path := r.URL.Query().Get("path")
	if vault == "" || path == "" {
		errResponse(w, 400, "vault and path required")
		return
	}
	vp, ok := s.vaultPath(vault)
	if !ok {
		errResponse(w, 400, "invalid vault")
		return
	}
	full, ok := s.safePath(vp, path)
	if !ok {
		errResponse(w, 400, "invalid path")
		return
	}
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		errResponse(w, 400, "only .md notes are exportable")
		return
	}

	raw, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			errResponse(w, 404, "note not found")
			return
		}
		errResponse(w, 500, "read failed")
		return
	}

	docxBytes, err := s.renderDocx(string(raw), vault, path)
	if err != nil {
		errResponse(w, 500, "export failed: "+err.Error())
		return
	}

	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".md") + ".docx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeDocxFilename(base)+`"`)
	// Don't set Content-Length: docx is already a zip; the gzip middleware
	// re-compresses (gaining ~nothing) but our Content-Length would describe
	// the uncompressed bytes, leaving the client waiting for missing data.
	w.Write(docxBytes)
}

var docxFilenameSanRe = regexp.MustCompile(`[^A-Za-z0-9._ \-]+`)

func sanitizeDocxFilename(s string) string {
	s = docxFilenameSanRe.ReplaceAllString(s, "_")
	if s == "" {
		s = "note.docx"
	}
	return s
}

// renderDocx is the core pipeline. Returns the .docx bytes.
func (s *server) renderDocx(raw, vault, notePath string) ([]byte, error) {
	raw = stripFrontmatter(raw)
	raw = expandEmbedsForDocx(raw, vault, notePath, s.idx, s.vaultsDir)
	// Pre-resolve wikilinks now so goldmark doesn't split [[…]] into three
	// adjacent Text nodes (it does, because its link parser snags [abc] first).
	raw = resolveWikilinksPlain(raw)

	// Parse with shared goldmark parser.
	reader := text.NewReader([]byte(raw))
	root := md.Parser().Parse(reader)

	ctx := &docxCtx{
		source:    []byte(raw),
		vault:     vault,
		notePath:  notePath,
		idx:       s.idx,
		vaultsDir: s.vaultsDir,
	}
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		ctx.walkBlock(n)
	}

	// Empty doc fallback so officecli batch has something to attach to.
	if len(ctx.cmds) == 0 {
		ctx.addParagraph(nil)
	}

	cmdJSON, err := json.Marshal(ctx.cmds)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "vrexport-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	outFile := filepath.Join(tmp, "out.docx")
	jsonFile := filepath.Join(tmp, "batch.json")
	if err := os.WriteFile(jsonFile, cmdJSON, 0o600); err != nil {
		return nil, err
	}

	// 1. Use the vault's branded template if present, otherwise create blank.
	tplPath := filepath.Join(s.vaultsDir, vault, ".vr", "template.docx")
	if data, err := os.ReadFile(tplPath); err == nil {
		if err := os.WriteFile(outFile, data, 0o600); err != nil {
			return nil, fmt.Errorf("write template copy: %w", err)
		}
	} else {
		if out, err := exec.Command(officecliBin, "create", outFile).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("officecli create: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	// 2. batch-apply commands. Run in continue-on-error mode so a single bad
	// command (unsupported style, malformed inline equation, missing image)
	// doesn't abandon the whole export — the user still gets a docx with the
	// rest of their content.
	//
	// officecli exits 1 when any command failed even though `Batch complete:`
	// is printed and the docx is saved. Treat presence of that string as
	// success and log the failure line; only bail when batch never ran.
	bcmd := exec.Command(officecliBin, "batch", outFile, "--input", jsonFile)
	bcmd.Env = append(os.Environ(), "OFFICECLI_BATCH_ALLOW_STDIN_REDIRECT=1")
	out, err := bcmd.CombinedOutput()
	completed := strings.Contains(string(out), "Batch complete:")
	if err != nil && !completed {
		fmt.Fprintf(os.Stderr, "officecli batch hard error for %s/%s: %v\n%s\n", vault, notePath, err, string(out))
		return nil, fmt.Errorf("officecli batch: %w (%s)", err, errorLine(string(out)))
	}
	if strings.Contains(strings.ToLower(string(out)), "error") || strings.Contains(string(out), "failed") {
		fmt.Fprintf(os.Stderr, "officecli batch partial-fail for %s/%s:\n%s\n", vault, notePath, string(out))
	}
	// 3. close to flush
	_ = exec.Command(officecliBin, "close", outFile).Run()

	return os.ReadFile(outFile)
}

// errorLine scans officecli output for a line that looks like a failure
// (officecli batch prints "[N] Added …" for successes and "[N] Error: …" /
// "Failed: …" for failures). Falls back to the first non-empty line.
func errorLine(s string) string {
	s = strings.TrimSpace(s)
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		lo := strings.ToLower(l)
		if strings.Contains(lo, "error") || strings.Contains(lo, "fail") {
			if len(l) > 240 {
				l = l[:240] + "…"
			}
			return l
		}
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

