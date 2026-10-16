package entry

import "testing"

// TestMatches verifies case-insensitive multi-term matching.
func TestMatches(t *testing.T) {
	e := Entry{
		Title:      "Новая версия Kubernetes",
		Categories: []string{"DevOps"},
		Content:    "Релиз включает улучшения планировщика",
	}
	cases := map[string]bool{
		"":                      true,
		"kubernetes":            true,
		"НОВАЯ ПЛАНИРОВЩИКА":    true,
		"devops релиз":          true,
		"kubernetes docker":     false,
		"  версия   улучшения ": true,
	}
	for q, want := range cases {
		if got := e.Matches(SearchTerms(q)); got != want {
			t.Errorf("Matches(%q) = %v, want %v", q, got, want)
		}
	}
}
