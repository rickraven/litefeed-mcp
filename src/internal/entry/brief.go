package entry

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// briefLen is the maximum length (in characters) of Brief.Summary.
const briefLen = 300

// Brief is a lightweight view of an entry without the article body,
// used for listings where the full text is not needed.
type Brief struct {
	ID         string    `json:"id"`
	Feed       string    `json:"feed"`
	Title      string    `json:"title"`
	Link       string    `json:"link,omitempty"`
	Author     string    `json:"author,omitempty"`
	Published  time.Time `json:"published"`
	Categories []string  `json:"categories,omitempty"`
	// Summary is a short plain text excerpt: the feed description when
	// available, otherwise the beginning of the body.
	Summary string `json:"summary,omitempty"`
}

// Brief returns the lightweight view of the entry.
func (e *Entry) Brief() Brief {
	src := e.Summary
	if strings.TrimSpace(src) == "" {
		src = e.Content
	}
	return Brief{
		ID:         e.ID,
		Feed:       e.Feed,
		Title:      e.Title,
		Link:       e.Link,
		Author:     e.Author,
		Published:  e.Published,
		Categories: e.Categories,
		Summary:    Excerpt(src, briefLen),
	}
}

var (
	// mdImageRe matches markdown images: ![alt](url).
	mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	// mdLinkRe matches markdown links: [text](url); text is kept.
	mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// mdLinePrefixRe matches block markers at line start: headings,
	// quotes, list bullets and horizontal rules.
	mdLinePrefixRe = regexp.MustCompile(`(?m)^\s*(#{1,6}\s+|>\s?|[-*+]\s+|---+\s*$)`)
	// mdEmphasisRe matches emphasis and inline code markers.
	mdEmphasisRe = regexp.MustCompile("\\*\\*|__|`+|~~")
	// mdEscapeRe matches backslash escapes added by the HTML converter.
	mdEscapeRe = regexp.MustCompile(`\\([\\` + "`" + `*_{}\[\]()#+\-.!|>~])`)
)

// Excerpt converts markdown into plain text (images removed, links
// replaced with their text, formatting markers dropped, whitespace
// collapsed) and cuts it to at most n characters at a word boundary,
// appending "…" when truncated.
func Excerpt(md string, n int) string {
	s := mdImageRe.ReplaceAllString(md, " ")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdLinePrefixRe.ReplaceAllString(s, " ")
	s = mdEmphasisRe.ReplaceAllString(s, "")
	s = mdEscapeRe.ReplaceAllString(s, "$1")
	s = strings.Join(strings.Fields(s), " ")

	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)[:n]
	cut := string(runes)
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}
