package entry

import (
	"strings"
	"testing"
	"time"
)

// TestComputeID verifies the guid -> link -> title fallback order.
func TestComputeID(t *testing.T) {
	byGUID := ComputeID("g", "l", "t")
	if byGUID != ComputeID("g", "", "") {
		t.Error("guid must take precedence")
	}
	if ComputeID("", "l", "t") != ComputeID(" l ", "", "") {
		t.Error("link must be used when guid is empty")
	}
	if len(byGUID) != 64 {
		t.Errorf("unexpected id length %d", len(byGUID))
	}
	if ComputeID("", " ", "") != "" {
		t.Error("expected empty id for empty input")
	}
}

// TestNormalize verifies defaults applied by Normalize.
func TestNormalize(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.FixedZone("x", 5*3600))
	e := Entry{Title: "  Hello ", Link: "https://example.com/a"}
	e.Normalize(now)
	if e.Title != "Hello" || e.ID == "" || e.FullText != FullTextNone {
		t.Errorf("unexpected entry: %+v", e)
	}
	if !e.Published.Equal(now) || e.Published.Location() != time.UTC {
		t.Errorf("published = %v", e.Published)
	}
}

// TestSlug verifies transliteration and length limiting.
func TestSlug(t *testing.T) {
	cases := map[string]string{
		"New version: Kubernetes 1.34!": "new-version-kubernetes-1-34",
		"Новая версия Kubernetes":       "novaia-versiia-kubernetes",
		"Борьба с подъёмом":             "borba-s-podiomom",
		"🔥🔥🔥":                           "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	long := Slug(strings.Repeat("word ", 50))
	if len(long) > maxSlugLen || strings.HasSuffix(long, "-") {
		t.Errorf("bad long slug %q", long)
	}
}

// TestFileName verifies the file name layout and UUID fallback.
func TestFileName(t *testing.T) {
	pub := time.Date(2026, 10, 16, 23, 0, 0, 0, time.UTC)
	e := Entry{Title: "Новая версия Kubernetes", Link: "https://x/1", Published: pub}
	e.Normalize(pub)
	if got := e.FileName(); got != "2026-10-16_novaia-versiia-kubernetes.md" {
		t.Errorf("FileName = %q", got)
	}
	if got := e.FileNameUnique(); got != "2026-10-16_novaia-versiia-kubernetes_"+e.ID[:8]+".md" {
		t.Errorf("FileNameUnique = %q", got)
	}

	noTitle := Entry{Title: "🔥", Link: "https://x/2", Published: pub}
	noTitle.Normalize(pub)
	first := noTitle.FileName()
	// "2026-10-16_" + 36 chars of UUID + ".md"
	if len(first) != 11+36+3 || first != noTitle.FileName() {
		t.Errorf("UUID file name must be deterministic, got %q", first)
	}
}
