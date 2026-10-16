package entry

import "strings"

// SearchTerms splits a search query into lowercase terms.
func SearchTerms(query string) []string {
	return strings.Fields(strings.ToLower(query))
}

// Matches reports whether every term occurs in the entry's title, author,
// categories, summary or content. Terms must be lowercase (see
// SearchTerms). Matching is a Unicode-aware substring search, so it works
// for any language without stemming. An empty term list matches everything.
//
// The search is done in Go rather than in the storage engine because
// SQLite's built-in LIKE/lower() only fold ASCII letters, which would make
// e.g. Cyrillic search case-sensitive.
func (e *Entry) Matches(terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	var b strings.Builder
	for _, s := range []string{e.Title, e.Author, strings.Join(e.Categories, " "), e.Summary, e.Content} {
		b.WriteString(strings.ToLower(s))
		b.WriteByte('\n')
	}
	text := b.String()
	for _, t := range terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}
