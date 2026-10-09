package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRewriteNoteImageURLs(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		vault    string
		notePath string
		want     string
	}{
		{
			name:     "relative image in same dir",
			html:     `<p><img src="figura.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="/api/file?vault=projects&path=dc-portugal-research%2Fanexo-tecnico%2Ffigura.png" alt="fig"></p>`,
		},
		{
			name:     "relative image in subfolder",
			html:     `<p><img src="assets/figuras-dcac/fig-5.4-componentes-dc.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="/api/file?vault=projects&path=dc-portugal-research%2Fanexo-tecnico%2Fassets%2Ffiguras-dcac%2Ffig-5.4-componentes-dc.png" alt="fig"></p>`,
		},
		{
			name:     "absolute URL untouched",
			html:     `<p><img src="https://example.com/img.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="https://example.com/img.png" alt="fig"></p>`,
		},
		{
			name:     "data URI untouched",
			html:     `<p><img src="data:image/png;base64,iVBORw0KGgo=" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="data:image/png;base64,iVBORw0KGgo=" alt="fig"></p>`,
		},
		{
			name:     "parent traversal rejected",
			html:     `<p><img src="../../../secret.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="../../../secret.png" alt="fig"></p>`,
		},
		{
			name:     "parent traversal within vault resolves",
			html:     `<p><img src="../../figura.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="/api/file?vault=projects&path=figura.png" alt="fig"></p>`,
		},
		{
			name:     "spaces and percent-encoding",
			html:     `<p><img src="fig%20ura%20final.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="/api/file?vault=projects&path=dc-portugal-research%2Fanexo-tecnico%2Ffig+ura+final.png" alt="fig"></p>`,
		},
		{
			name:     "already rewritten untouched",
			html:     `<p><img src="/api/file?vault=projects&path=dc-portugal-research%2Ffig.png" alt="fig"></p>`,
			vault:    "projects",
			notePath: "dc-portugal-research/anexo-tecnico/ANEXO-TECNICO.md",
			want:     `<p><img src="/api/file?vault=projects&path=dc-portugal-research%2Ffig.png" alt="fig"></p>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteNoteImageURLs(tt.html, tt.vault, tt.notePath)
			if got != tt.want {
				t.Errorf("rewriteNoteImageURLs() =\n  got  %s\n  want %s", got, tt.want)
			}
		})
	}
}

func TestProtectMathDelims(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "inline parens delimiters survive goldmark",
			raw:  `Inline \( I^d = K/(K+L) \) math.`,
			want: `<p>Inline \( I^d = K/(K+L) \) math.</p>`,
		},
		{
			name: "block bracket delimiters survive",
			raw:  `Block \[ \beta > \underline{\beta}(K) \] end.`,
			want: `<p>Block \[ \beta &gt; \underline{\beta}(K) \] end.</p>`,
		},
		{
			name: "dollar delimiters survive",
			raw:  `Dollar $$ \tau^* = \frac{\alpha-2}{2\alpha-2} $$ end.`,
			want: `<p>Dollar $$ \tau^* = \frac{\alpha-2}{2\alpha-2} $$ end.</p>`,
		},
		{
			name: "math inside code fence untouched",
			raw:  "```\n\\( not math \\)\n```\n\nAfter \\( real \\) math.",
			want: "<pre><code>\\( not math \\)\n</code></pre>\n<p>After \\( real \\) math.</p>",
		},
		{
			name: "escaped parens are treated as math (Obsidian semantics)",
			raw:  `Escaped \(paren\) stays as math.`,
			want: `<p>Escaped \(paren\) stays as math.</p>`,
		},
		{
			name: "multiple spans in one line",
			raw:  `A \(x\) and \(y\) and $$z$$.`,
			want: `<p>A \(x\) and \(y\) and $$z$$.</p>`,
		},
		{
			name: "math with html metacharacters is escaped",
			raw:  `Set \( \{x \mid x < 1\} \) ok.`,
			want: `<p>Set \( \{x \mid x &lt; 1\} \) ok.</p>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.TrimSuffix(renderMarkdown(tt.raw), "\n")
			if got != tt.want {
				t.Errorf("renderMarkdown() =\n  got  %q\n  want %q", got, tt.want)
			}
		})
	}
}

// PUT conflict detection compares the client's mtime (which the server
// itself handed out) with the on-disk mtime exactly: any difference is a
// concurrent change, including one landing within the same second+1.
func TestPutNoteConflictExactMTime(t *testing.T) {
	s := newTestServer(t, AdminConfig{RWPaths: []string{"jll"}})
	full := filepath.Join(s.vaultsDir, "jll", "a.md")
	writeFile(t, full, "v1\n")
	base := time.Unix(1_700_000_000, 0)
	h := s.handler()
	put := func(ifm int64) int {
		return do(t, h, "PUT", fmt.Sprintf("/api/note?vault=jll&path=a.md&ifMTime=%d", ifm), `{"raw":"v2"}`, nil).Code
	}
	for _, c := range []struct {
		name      string
		disk, ifm int64
		want      int
	}{
		{"same mtime saves", 0, 0, 200},
		{"disk 1s newer conflicts", 1, 0, 409},
		{"disk older than client conflicts", 0, 5, 409},
	} {
		if err := os.Chtimes(full, base.Add(time.Duration(c.disk)*time.Second), base.Add(time.Duration(c.disk)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if got := put(base.Unix() + c.ifm); got != c.want {
			t.Errorf("%s: code %d, want %d", c.name, got, c.want)
		}
	}
}
