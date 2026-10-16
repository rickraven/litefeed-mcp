package config

import (
	"strings"
	"testing"
	"time"
)

// TestParseDefaults verifies that a minimal config gets sane defaults.
func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
feeds:
  - name: habr
    url: https://habr.com/ru/rss/all/
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Addr() != "0.0.0.0:8080" {
		t.Errorf("addr = %q", cfg.Server.Addr())
	}
	if cfg.Scrape.Interval != 30*time.Minute {
		t.Errorf("interval = %s", cfg.Scrape.Interval)
	}
	if cfg.Storage.Type != StorageSQLite {
		t.Errorf("storage type = %q", cfg.Storage.Type)
	}
	if cfg.Feeds[0].Title != "habr" {
		t.Errorf("title = %q", cfg.Feeds[0].Title)
	}
}

// TestParseFull verifies decoding of every option, including durations.
func TestParseFull(t *testing.T) {
	cfg, err := Parse([]byte(`
server: {listen: 127.0.0.1, port: 9000}
scrape: {interval: 15m, timeout: 10s, user_agent: test}
storage: {type: markdown, markdown: {dir: /data}}
retention: {max_age_days: 7}
feeds:
  - {name: a, title: A, url: "http://a.example/rss", full_text: true}
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Scrape.Interval != 15*time.Minute || cfg.Scrape.Timeout != 10*time.Second {
		t.Errorf("durations = %s / %s", cfg.Scrape.Interval, cfg.Scrape.Timeout)
	}
	if cfg.Retention.MaxAge() != 7*24*time.Hour {
		t.Errorf("max age = %s", cfg.Retention.MaxAge())
	}
	if !cfg.Feeds[0].FullText || cfg.Storage.Markdown.Dir != "/data" {
		t.Errorf("unexpected values: %+v", cfg)
	}
}

// TestParseErrors verifies that invalid configs are rejected.
func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no feeds":      `server: {port: 80}`,
		"bad name":      "feeds: [{name: Bad Name, url: 'http://x/'}]",
		"duplicate":     "feeds: [{name: a, url: 'http://x/'}, {name: a, url: 'http://y/'}]",
		"bad url":       "feeds: [{name: a, url: 'ftp://x/'}]",
		"bad storage":   "storage: {type: s3}\nfeeds: [{name: a, url: 'http://x/'}]",
		"unknown field": "foo: 1\nfeeds: [{name: a, url: 'http://x/'}]",
		"short period":  "scrape: {interval: 5s}\nfeeds: [{name: a, url: 'http://x/'}]",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data)); err == nil {
				t.Errorf("expected error for %s", strings.TrimSpace(data))
			}
		})
	}
}
