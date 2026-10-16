# litefeed-mcp

Lightweight self-hosted RSS reader with a web UI, a REST API and a built-in
[MCP](https://modelcontextprotocol.io) server.

litefeed-mcp periodically polls your RSS/Atom/JSON feeds, stores new articles
locally (without duplicates) and gives access to them in three ways: a simple
web page for reading, a JSON REST API and an MCP endpoint that lets LLM
assistants (Cursor, Claude, etc.) search and read your feeds.

It is a single static binary with no runtime dependencies; a Docker image is
available as well.

## Features

- RSS, Atom and JSON Feed support; deduplication by item GUID (or link).
- Two storage backends:
  - **SQLite** — a single local database file;
  - **Markdown files** — one human-readable `.md` file per article with YAML
    front matter, organized in a directory per feed.
- Article HTML is converted to markdown; images are kept as links to the
  original sites (only text is stored).
- Optional **full text** download per feed: the article page is fetched and
  its main content is extracted (Readability algorithm), useful for feeds that
  publish only teasers.
- Automatic removal of articles older than a configured number of days.
- Web UI: day-by-day feed with infinite scrolling, feed filter, light/dark
  theme. No external scripts or fonts.
- MCP tools to list feeds, search articles (by feed, date and text) and read
  full articles.

## Quick start

### Docker

Images for `linux/amd64`, `linux/arm64` and `linux/arm/v7` are published on
Docker Hub as `rickraven/litefeed-mcp`.

Create a `config.yaml` (see [Configuration](#configuration)) and run:

```sh
docker run -d --name litefeed \
  -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/litefeed/config.yaml:ro" \
  -v litefeed-data:/data \
  rickraven/litefeed-mcp:latest
```

Then open <http://localhost:8080>. The MCP endpoint is
<http://localhost:8080/mcp>.

Inside the container all data must be kept in `/data` (the volume), so use
`/data/...` paths for storage in your config:

```yaml
storage:
  type: sqlite
  sqlite:
    path: /data/litefeed.db
  markdown:
    dir: /data/feeds
```

Without a mounted config the image starts with a default configuration that
follows the Go Blog feed only.

The container runs as user `10001`. When a host directory is mounted to
`/data` instead of a named volume, it must be writable for this user (or start
the container with `--user` matching the directory owner).

Docker Compose:

```yaml
services:
  litefeed:
    image: rickraven/litefeed-mcp:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/etc/litefeed/config.yaml:ro
      - litefeed-data:/data

volumes:
  litefeed-data:
```

### Binary

Download the archive for your platform from the
[Releases](https://github.com/rickraven/litefeed-mcp/releases) page
(Linux, macOS, Windows and FreeBSD; amd64 and arm64, plus armv7 for Linux),
unpack it and run:

```sh
./litefeed-mcp -config config.yaml
```

The archive contains `config.example.yaml` to start from. Relative storage
paths are resolved against the current working directory.

Command line flags:

| Flag              | Description                                     |
|-------------------|-------------------------------------------------|
| `-config <path>`  | Configuration file (default `config.yaml`)      |
| `-debug`          | Verbose logging (per-feed results)              |
| `-version`        | Print the version and exit                      |

The application stops gracefully on `SIGINT`/`SIGTERM`.

## Configuration

The configuration is a YAML file. Unknown keys are rejected, so typos are
reported at startup. All options except `feeds` are optional.

```yaml
# HTTP listener for the web UI, REST API and MCP endpoint.
server:
  listen: 0.0.0.0           # default 0.0.0.0
  port: 8080                # default 8080

# Feed polling.
scrape:
  interval: 30m             # how often all feeds are polled; minimum 1m, default 30m
  timeout: 30s              # timeout of a single HTTP request, default 30s
  user_agent: "Mozilla/5.0 (compatible; litefeed-mcp/1.0)"  # sent with every request

# Storage backend: "sqlite" (default) or "markdown".
storage:
  type: sqlite
  sqlite:
    path: data/litefeed.db  # database file, created if missing
  markdown:
    dir: data/feeds         # root directory, one subdirectory per feed

# Removal of old entries (by publication date).
retention:
  max_age_days: 30          # 0 keeps everything (default)

# Feeds to follow.
feeds:
  - name: habr              # identifier: lowercase letters, digits, "-", "_"
    title: Habr             # display name, defaults to name
    url: https://habr.com/ru/rss/all/all/
    full_text: false        # download the article page and extract its text
```

Feeds are polled right after startup and then every `interval`. Articles
older than `max_age_days` are neither saved nor kept.

Changes to the configuration file take effect after a restart.

## Storage

### SQLite

All entries are stored in one table of the database file. The database can
be inspected with any SQLite client.

### Markdown files

Every entry is a separate file `<dir>/<feed>/<date>_<slug>.md`, where the slug
is the title transliterated to Latin characters, e.g.
`2026-10-16_novaia-versiia-kubernetes.md`. When the title cannot be
transliterated, a UUID is used instead. Metadata is stored in YAML front
matter:

```markdown
---
id: a3f1c9e2...
guid: https://habr.com/p/123456/
feed: habr
title: Новая версия Kubernetes
link: https://habr.com/ru/articles/123456/
author: Иван Иванов
published: 2026-10-16T09:30:00Z
fetched: 2026-10-16T10:05:12Z
categories:
  - kubernetes
full_text: none
---

Article text in markdown ![](https://habr.com/img/diagram.png)
```

`full_text` is `none` (feed content), `ok` (full text downloaded from the
site) or `failed` (download failed, feed content kept).

The files are read once at startup; editing or removing them while the
application is running takes effect after a restart.

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
