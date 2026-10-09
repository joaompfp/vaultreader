// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
)

// Callouts as Obsidian users write them: the body continues on the next
// "> " line, which goldmark folds into the title's paragraph.
func TestRenderCallouts(t *testing.T) {
	cases := []struct {
		name, md, title, body string
	}{
		{"title and body on next line", "> [!info] Status\n> Body text here.\n",
			"Status", "<p>Body text here.</p>"},
		{"no title, body on next line", "> [!warning]\n> Careful now.\n",
			"Warning", "<p>Careful now.</p>"},
		{"title, blank line, body", "> [!tip] Hint\n>\n> Second paragraph.\n",
			"Hint", "<p>Second paragraph.</p>"},
		{"title only", "> [!note] Just a title\n", "Just a title", ""},
		{"body with a link", "> [!info] See\n> the [docs](https://example.com).\n",
			"See", `<a href="https://example.com">docs</a>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderCallouts(renderMarkdown(c.md))
			if !strings.Contains(out, `<div class="callout callout-`) {
				t.Fatalf("not rendered as a callout:\n%s", out)
			}
			wantTitle := `<span class="callout-icon"></span>` + c.title + `</div>`
			if !strings.Contains(out, wantTitle) {
				t.Errorf("title: want %q in\n%s", wantTitle, out)
			}
			if c.body != "" && !strings.Contains(out, c.body) {
				t.Errorf("body: want %q in\n%s", c.body, out)
			}
			if strings.Contains(out, "[!") {
				t.Errorf("marker left in output:\n%s", out)
			}
		})
	}
}
