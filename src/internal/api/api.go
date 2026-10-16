// Package api implements the read-only REST API over the stored entries.
//
// Endpoints:
//
//	GET /api/feeds              configured feeds
//	GET /api/entries            entries with full content
//	GET /api/entries/brief      entries without the body (short summary)
//	GET /api/entries/{id}       a single entry
//
// List endpoints accept the filters described in parseListParams.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"litefeed-mcp/internal/config"
	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage"
)

// Listing limits.
const (
	defaultLimit = 100
	maxLimit     = 1000
)

// dayLayout is the format of the "day" parameter and "prev_day" field.
const dayLayout = "2006-01-02"

// API serves the REST endpoints.
type API struct {
	store storage.Store
	feeds []config.Feed
}

// New creates the API over the given store and configured feeds.
func New(store storage.Store, feeds []config.Feed) *API {
	return &API{store: store, feeds: feeds}
}

// Register adds the API routes to mux. The literal "/api/entries/brief"
// pattern takes precedence over the "{id}" wildcard in net/http routing.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/feeds", a.handleFeeds)
	mux.HandleFunc("GET /api/entries", a.handleEntries)
	mux.HandleFunc("GET /api/entries/brief", a.handleBrief)
	mux.HandleFunc("GET /api/entries/{id}", a.handleEntry)
}

// feedJSON is the public representation of a configured feed.
type feedJSON struct {
	Name     string `json:"name"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	FullText bool   `json:"full_text"`
}

// listResponse is returned by the list endpoints.
type listResponse[T any] struct {
	// Entries are ordered by publication time, newest first.
	Entries []T `json:"entries"`
	// More is true when more entries match than were returned (limit hit).
	More bool `json:"more"`
	// Day echoes the requested day in day mode.
	Day string `json:"day,omitempty"`
	// PrevDay is the closest earlier day (in the requested time zone) that
	// has entries matching the feed filter; empty when there is none.
	// Only set in day mode. Lets clients skip empty days.
	PrevDay string `json:"prev_day,omitempty"`
}

func (a *API) handleFeeds(w http.ResponseWriter, _ *http.Request) {
	out := make([]feedJSON, 0, len(a.feeds))
	for _, f := range a.feeds {
		out = append(out, feedJSON{Name: f.Name, Title: f.Title, URL: f.URL, FullText: f.FullText})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleEntries(w http.ResponseWriter, r *http.Request) {
	list(a, w, r, func(e *entry.Entry) entry.Entry { return *e })
}

func (a *API) handleBrief(w http.ResponseWriter, r *http.Request) {
	list(a, w, r, (*entry.Entry).Brief)
}

func (a *API) handleEntry(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "entry not found")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// list runs a list query and converts every entry with view.
func list[T any](a *API, w http.ResponseWriter, r *http.Request, view func(*entry.Entry) T) {
	p, err := a.parseListParams(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()

	// One extra entry is requested to find out whether more are available.
	q := p.query
	q.Limit = p.limit + 1
	entries, err := a.store.List(ctx, q)
	if err != nil {
		internalError(w, r, err)
		return
	}

	resp := listResponse[T]{Entries: make([]T, 0, min(len(entries), p.limit))}
	if len(entries) > p.limit {
		resp.More = true
		entries = entries[:p.limit]
	}
	for i := range entries {
		resp.Entries = append(resp.Entries, view(&entries[i]))
	}

	if p.loc != nil {
		resp.Day = p.query.Since.In(p.loc).Format(dayLayout)
		prev, err := a.store.List(ctx, storage.Query{
			Feeds: p.query.Feeds, Text: p.query.Text, Until: p.query.Since, Limit: 1,
		})
		if err != nil {
			internalError(w, r, err)
			return
		}
		if len(prev) > 0 {
			resp.PrevDay = prev[0].Published.In(p.loc).Format(dayLayout)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// listParams is the parsed form of the list query string.
type listParams struct {
	query storage.Query
	limit int
	// loc is set in day mode only.
	loc *time.Location
}

// parseListParams parses list filters:
//
//	feed   feed name; may be repeated or comma separated (default: all)
//	since  RFC3339 time, inclusive lower bound of publication time
//	until  RFC3339 time, exclusive upper bound of publication time
//	day    YYYY-MM-DD; selects one calendar day (excludes since/until)
//	tz     IANA time zone for "day", e.g. Europe/Moscow (default UTC)
//	q      text search: all words must occur (case-insensitive)
//	limit  maximum number of entries (default 100, max 1000)
func (a *API) parseListParams(v url.Values) (listParams, error) {
	p := listParams{limit: defaultLimit}
	p.query.Text = v.Get("q")

	for _, raw := range v["feed"] {
		for name := range strings.SplitSeq(raw, ",") {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			if !a.hasFeed(name) {
				return p, fmt.Errorf("unknown feed %q", name)
			}
			p.query.Feeds = append(p.query.Feeds, name)
		}
	}

	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return p, fmt.Errorf("limit must be an integer between 1 and %d", maxLimit)
		}
		p.limit = n
	}

	var err error
	if p.query.Since, err = parseTime(v.Get("since")); err != nil {
		return p, fmt.Errorf("since: %w", err)
	}
	if p.query.Until, err = parseTime(v.Get("until")); err != nil {
		return p, fmt.Errorf("until: %w", err)
	}

	if day := v.Get("day"); day != "" {
		if !p.query.Since.IsZero() || !p.query.Until.IsZero() {
			return p, errors.New("day cannot be combined with since/until")
		}
		p.loc = time.UTC
		if tz := v.Get("tz"); tz != "" {
			if p.loc, err = time.LoadLocation(tz); err != nil {
				return p, fmt.Errorf("unknown time zone %q", tz)
			}
		}
		start, err := time.ParseInLocation(dayLayout, day, p.loc)
		if err != nil {
			return p, errors.New("day must be in YYYY-MM-DD format")
		}
		// AddDate keeps local midnight across DST changes.
		p.query.Since, p.query.Until = start, start.AddDate(0, 0, 1)
	}
	return p, nil
}

// hasFeed reports whether name is a configured feed.
func (a *API) hasFeed(name string) bool {
	return slices.ContainsFunc(a.feeds, func(f config.Feed) bool { return f.Name == name })
}

// parseTime parses an optional RFC3339 time; "" yields the zero time.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, errors.New("must be an RFC3339 time, e.g. 2026-10-16T00:00:00Z")
	}
	return t, nil
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	// Responses are JSON, not HTML: keep "&", "<", ">" in links readable.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Debug("failed to write response", "err", err)
	}
}

// writeError writes {"error": msg} with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// internalError logs err and returns a generic 500 response, so storage
// details are not exposed to clients.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
