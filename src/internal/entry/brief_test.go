package entry

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestExcerpt verifies markdown stripping and truncation.
func TestExcerpt(t *testing.T) {
	md := "![](https://x/cover.png)\n\n# Title\n\n> quote with **bold** and `code`\n\n" +
		"- item [link text](https://x/a) \\*escaped\\*\n\n---\n\nend"
	want := "Title quote with bold and code item link text *escaped* end"
	if got := Excerpt(md, 1000); got != want {
		t.Errorf("Excerpt = %q, want %q", got, want)
	}

	long := strings.Repeat("слово ", 100)
	got := Excerpt(long, 50)
	if utf8.RuneCountInString(got) > 51 || !strings.HasSuffix(got, "слово…") {
		t.Errorf("truncated excerpt = %q", got)
	}
}

// TestBrief verifies the summary source selection.
func TestBrief(t *testing.T) {
	e := Entry{ID: "1", Title: "T", Content: "**body**"}
	if b := e.Brief(); b.Summary != "body" {
		t.Errorf("summary from content = %q", b.Summary)
	}
	e.Summary = "from feed"
	if b := e.Brief(); b.Summary != "from feed" || b.Title != "T" {
		t.Errorf("brief = %+v", b)
	}
}
