package entry

import (
	"strings"

	"github.com/google/uuid"
	"github.com/gosimple/slug"
)

// maxSlugLen limits the slug part of a file name. Long titles are cut at a
// word boundary to keep paths well below filesystem limits.
const maxSlugLen = 80

// dateLayout is the date prefix format of entry file names.
const dateLayout = "2006-01-02"

// fileExt is the extension of entry files.
const fileExt = ".md"

// uuidNamespace is the namespace used to derive deterministic UUIDs
// from entry IDs, so the same entry always maps to the same file name.
var uuidNamespace = uuid.MustParse("6f1c5a52-6f0e-4c1e-9a43-3d0c8a1b7e21")

// signsRemover deletes Cyrillic soft and hard signs before transliteration.
var signsRemover = strings.NewReplacer("ь", "", "Ь", "", "ъ", "", "Ъ", "")

// Slug converts a title into a lowercase ASCII slug using transliteration
// (e.g. Cyrillic is converted to Latin). The result is limited to
// maxSlugLen characters and may be empty if the title has no
// transliterable characters.
func Slug(title string) string {
	// Soft and hard signs are transliterated to apostrophes, which the
	// slug library turns into separators ("bor-ba"); drop them instead.
	title = signsRemover.Replace(title)
	s := slug.Make(title)
	if len(s) <= maxSlugLen {
		return s
	}
	// The slug is pure ASCII, so byte slicing is safe. Cut at the last
	// separator to avoid half words.
	s = s[:maxSlugLen]
	if i := strings.LastIndexByte(s, '-'); i > 0 {
		s = s[:i]
	}
	return strings.Trim(s, "-")
}

// FileName returns the base file name for the entry:
// "<published date>_<slug>.md". When the title cannot be transliterated a
// UUID derived from the entry ID is used instead of the slug.
// The entry must be normalized (see Normalize).
func (e *Entry) FileName() string {
	name := Slug(e.Title)
	if name == "" {
		name = uuid.NewSHA1(uuidNamespace, []byte(e.ID)).String()
	}
	return e.Published.Format(dateLayout) + "_" + name + fileExt
}

// FileNameUnique returns the file name with a short ID suffix
// ("<date>_<slug>_<id[:8]>.md"). It is used when FileName collides with a
// file belonging to another entry.
func (e *Entry) FileNameUnique() string {
	base := strings.TrimSuffix(e.FileName(), fileExt)
	id := e.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return base + "_" + id + fileExt
}
