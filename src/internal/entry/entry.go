// Package entry defines the feed entry model shared by the scraper,
// the storage backends and the HTTP/MCP layers.
package entry

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// FullTextStatus describes whether the entry body holds the full article
// text downloaded from the site or just the content provided by the feed.
type FullTextStatus string

const (
	// FullTextNone means full text download was not requested; the body
	// contains the feed content.
	FullTextNone FullTextStatus = "none"
	// FullTextOK means the body contains the extracted article text.
	FullTextOK FullTextStatus = "ok"
	// FullTextFailed means the download was requested but failed; the body
	// contains the feed content and the download may be retried later.
	FullTextFailed FullTextStatus = "failed"
)

// Entry is a single feed item. The YAML tags define the front matter
// layout of the markdown storage; Content is stored as the file body.
type Entry struct {
	// ID is the stable deduplication key, see ComputeID.
	ID string `yaml:"id" json:"id"`
	// GUID is the item identifier as provided by the feed (may be empty).
	GUID string `yaml:"guid,omitempty" json:"guid,omitempty"`
	// Feed is the name of the source feed from the configuration.
	Feed string `yaml:"feed" json:"feed"`
	// Title of the article as provided by the feed.
	Title string `yaml:"title" json:"title"`
	// Link to the original article.
	Link string `yaml:"link,omitempty" json:"link,omitempty"`
	// Author of the article (may be empty).
	Author string `yaml:"author,omitempty" json:"author,omitempty"`
	// Published is the publication time; falls back to Fetched when the
	// feed does not provide it.
	Published time.Time `yaml:"published" json:"published"`
	// Updated is the last modification time reported by the feed.
	Updated time.Time `yaml:"updated,omitempty" json:"updated,omitzero"`
	// Fetched is the time the entry was first saved by the application.
	Fetched time.Time `yaml:"fetched" json:"fetched"`
	// Categories (tags) reported by the feed.
	Categories []string `yaml:"categories,omitempty" json:"categories,omitempty"`
	// Summary is the short description from the feed. It is kept separately
	// from Content only when the body was replaced with the full text.
	Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`
	// FullText is the status of the full text download.
	FullText FullTextStatus `yaml:"full_text" json:"full_text"`
	// Content is the article body in markdown.
	Content string `yaml:"-" json:"content"`
}

// ComputeID returns a stable identifier for an entry: the hex-encoded
// sha256 of the first non-empty value among guid, link and title.
// Feeds are expected to provide at least a link; the title fallback only
// exists so that malformed items still get a deterministic key.
// An empty string is returned when all values are empty.
func ComputeID(guid, link, title string) string {
	for _, v := range []string{guid, link, title} {
		if v = strings.TrimSpace(v); v != "" {
			sum := sha256.Sum256([]byte(v))
			return hex.EncodeToString(sum[:])
		}
	}
	return ""
}

// Normalize trims text fields, fills in the ID, the fetch/publication
// times and the full text status. now is passed explicitly to keep the
// function deterministic in tests. All times are converted to UTC.
func (e *Entry) Normalize(now time.Time) {
	e.GUID = strings.TrimSpace(e.GUID)
	e.Title = strings.TrimSpace(e.Title)
	e.Link = strings.TrimSpace(e.Link)
	e.Author = strings.TrimSpace(e.Author)

	if e.ID == "" {
		e.ID = ComputeID(e.GUID, e.Link, e.Title)
	}
	if e.Fetched.IsZero() {
		e.Fetched = now
	}
	if e.Published.IsZero() {
		e.Published = e.Fetched
	}
	e.Fetched = e.Fetched.UTC()
	e.Published = e.Published.UTC()
	if !e.Updated.IsZero() {
		e.Updated = e.Updated.UTC()
	}
	if e.FullText == "" {
		e.FullText = FullTextNone
	}
}
