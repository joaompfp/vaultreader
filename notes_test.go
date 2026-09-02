package main

import "testing"

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
