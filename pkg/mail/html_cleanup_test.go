package mail

import (
	"strings"
	"testing"
)

func TestCleanupEmailHTMLUsesConservativeMarkers(t *testing.T) {
	t.Parallel()

	cleaned, err := cleanupEmailHTML(`<body>
		<script>unsafe()</script>
		<div id="mcnPreviewText" style="max-height: 0; overflow: hidden">Preview</div>
		<img style="width: 1px; height: 1px" src="pixel.gif">
		<div id="divRplyFwdMsg">Reply header</div>
		<div class="preheader-content">Visible preheader discussion</div>
		<div class="gmail_quote_summary">Visible quote summary</div>
		<img width="120" height="40" src="logo.png">
	</body>`)
	if err != nil {
		t.Fatalf("cleanupEmailHTML() error = %v", err)
	}

	for _, removed := range []string{"unsafe()", "Preview", "pixel.gif", "Reply header"} {
		if strings.Contains(cleaned, removed) {
			t.Errorf("cleaned HTML retained %q: %s", removed, cleaned)
		}
	}
	for _, preserved := range []string{"Visible preheader discussion", "Visible quote summary", "logo.png"} {
		if !strings.Contains(cleaned, preserved) {
			t.Errorf("cleaned HTML removed legitimate content %q: %s", preserved, cleaned)
		}
	}
}
