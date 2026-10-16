# litefeed-mcp
Lightweight RSS feed reader with WEB UI and builtin MCP server

## Web UI

The web interface is served at `/`. It is a single page without external
dependencies, embedded into the binary.

- The feed is loaded day by day while scrolling, in the browser's time zone.
  Days without entries are skipped.
- The **Feeds** menu selects which feeds are shown (all by default). The
  selection is stored in the browser.
- The theme follows the system setting by default and can be switched to
  light or dark with the button in the top bar.
- **Read here** shows the stored article body inline. The title opens the
  original article. Raw HTML from feeds is never rendered.

## MCP

The MCP endpoint is served at `/mcp` using the Streamable HTTP transport
(stateless). Example client configuration:

```json
{
  "mcpServers": {
    "litefeed": { "url": "http://localhost:8080/mcp" }
  }
}
```

All tools are read-only:

| Tool             | Description                                                                 |
|------------------|-----------------------------------------------------------------------------|
| `list_feeds`     | Configured feeds (`name`, `title`, `url`)                                   |
| `search_entries` | Brief entries, newest first. Filters: `feeds`, `query`, `since`, `until`, `limit` (default 20, max 100) |
| `get_entry`      | One entry with the full markdown body, by `id`                              |

`since` and `until` accept `YYYY-MM-DD` (UTC, `until` includes the whole day)
or RFC3339 timestamps. `query` works like the REST API `q` parameter.

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
- `q` — text search: every word must occur (case-insensitive) in the title, author, categories, summary or body.
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
