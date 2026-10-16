# litefeed-mcp
Lightweight RSS feed reader with WEB UI and builtin MCP server

## REST API

All endpoints are read-only and return JSON. Errors are returned as
`{"error": "message"}` with a 4xx/5xx status.

| Method | Path                    | Description                                   |
|--------|-------------------------|-----------------------------------------------|
| GET    | `/api/feeds`            | Configured feeds                              |
| GET    | `/api/entries`          | Entries with full content                     |
| GET    | `/api/entries/brief`    | Entries without content, with a short summary |
| GET    | `/api/entries/{id}`     | A single entry with full content              |

### List parameters

`/api/entries` and `/api/entries/brief` accept the same query parameters:

- `feed` — feed name; may be repeated (`feed=a&feed=b`) or comma separated (`feed=a,b`). Default: all feeds.
- `since` — RFC3339 time, entries published at or after it.
- `until` — RFC3339 time, entries published before it.
- `day` — `YYYY-MM-DD`, entries of one calendar day. Cannot be combined with `since`/`until`.
- `tz` — IANA time zone used to interpret `day`, e.g. `Europe/Moscow`. Default: `UTC`.
- `limit` — maximum number of entries, 1–1000. Default: 100.

Entries are ordered by publication time, newest first. Response:

```json
{
  "entries": [ ... ],
  "more": false,
  "day": "2026-10-16",
  "prev_day": "2026-10-14"
}
```

- `more` — `true` when more entries match than `limit` allowed.
- `day`, `prev_day` — only in `day` mode. `prev_day` is the closest earlier
  day that has entries for the selected feeds (absent when there is none),
  which allows clients to load the feed day by day skipping empty days.

### Entry

```json
{
  "id": "a3f1c9e2...",
  "guid": "https://habr.com/p/123456/",
  "feed": "habr",
  "title": "New Kubernetes version",
  "link": "https://habr.com/ru/articles/123456/",
  "author": "Author",
  "published": "2026-10-16T09:30:00Z",
  "updated": "2026-10-16T10:00:00Z",
  "fetched": "2026-10-16T10:05:12Z",
  "categories": ["kubernetes"],
  "summary": "Feed description (only when it differs from the body)",
  "full_text": "none",
  "content": "Article body in markdown"
}
```

Brief entries contain `id`, `feed`, `title`, `link`, `author`, `published`,
`categories` and `summary`. Here `summary` is a plain text excerpt (up to 300
characters) of the feed description, or of the body when there is no
description.
