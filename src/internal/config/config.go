// Package config loads and validates the application configuration
// from a YAML file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Storage backend identifiers accepted in `storage.type`.
const (
	StorageSQLite   = "sqlite"
	StorageMarkdown = "markdown"
)

// Default values applied when the corresponding option is omitted.
const (
	defaultListen      = "0.0.0.0"
	defaultPort        = 8080
	defaultInterval    = 30 * time.Minute
	defaultTimeout     = 30 * time.Second
	defaultUserAgent   = "Mozilla/5.0 (compatible; litefeed-mcp/1.0)"
	defaultSQLitePath  = "data/litefeed.db"
	defaultMarkdownDir = "data/feeds"
	minScrapeInterval  = time.Minute
)

// feedNameRe restricts feed names to characters that are safe to use as
// directory names on any filesystem and inside URLs.
var feedNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config is the root of the configuration file.
type Config struct {
	Server    Server    `yaml:"server"`
	Scrape    Scrape    `yaml:"scrape"`
	Storage   Storage   `yaml:"storage"`
	Retention Retention `yaml:"retention"`
	Feeds     []Feed    `yaml:"feeds"`
}

// Server holds the HTTP listener settings shared by the web UI,
// the REST API and the MCP endpoint.
type Server struct {
	// Listen is the address to bind to, e.g. "0.0.0.0" or "127.0.0.1".
	Listen string `yaml:"listen"`
	// Port is the TCP port to bind to.
	Port int `yaml:"port"`
}

// Addr returns the "host:port" string suitable for net/http.
func (s Server) Addr() string {
	return net.JoinHostPort(s.Listen, strconv.Itoa(s.Port))
}

// Scrape controls how and how often feeds are fetched.
type Scrape struct {
	// Interval between two consecutive polls of all feeds (e.g. "30m").
	Interval time.Duration `yaml:"interval"`
	// Timeout for a single HTTP request.
	Timeout time.Duration `yaml:"timeout"`
	// UserAgent sent with every HTTP request. Some sites block
	// default Go user agents, so a browser-like value may be needed.
	UserAgent string `yaml:"user_agent"`
}

// Storage selects and configures the storage backend.
type Storage struct {
	// Type is either "sqlite" or "markdown".
	Type     string          `yaml:"type"`
	SQLite   SQLiteStorage   `yaml:"sqlite"`
	Markdown MarkdownStorage `yaml:"markdown"`
}

// SQLiteStorage configures the sqlite backend.
type SQLiteStorage struct {
	// Path to the database file. Created if missing.
	Path string `yaml:"path"`
}

// MarkdownStorage configures the markdown files backend.
type MarkdownStorage struct {
	// Dir is the root directory; every feed gets its own subdirectory.
	Dir string `yaml:"dir"`
}

// Retention controls removal of old entries.
type Retention struct {
	// MaxAgeDays is the maximum age of an entry (by publication date)
	// in days. Zero disables cleanup.
	MaxAgeDays int `yaml:"max_age_days"`
}

// MaxAge returns the retention period as a duration (zero if disabled).
func (r Retention) MaxAge() time.Duration {
	return time.Duration(r.MaxAgeDays) * 24 * time.Hour
}

// Feed describes a single RSS/Atom source.
type Feed struct {
	// Name is a short machine-friendly identifier used as directory name
	// and in API requests. Must match feedNameRe.
	Name string `yaml:"name"`
	// Title is a human-readable name shown in the web UI.
	// Defaults to Name when omitted.
	Title string `yaml:"title"`
	// URL of the feed.
	URL string `yaml:"url"`
	// FullText enables downloading the article page and extracting
	// its readable content instead of relying on the feed content only.
	FullText bool `yaml:"full_text"`
}

// Load reads the configuration file at path, applies defaults and
// validates the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse decodes YAML configuration from data, applies defaults and
// validates the result. Unknown keys are rejected to catch typos early.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// applyDefaults fills in values for options that were left empty.
func (c *Config) applyDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = defaultListen
	}
	if c.Server.Port == 0 {
		c.Server.Port = defaultPort
	}
	if c.Scrape.Interval == 0 {
		c.Scrape.Interval = defaultInterval
	}
	if c.Scrape.Timeout == 0 {
		c.Scrape.Timeout = defaultTimeout
	}
	if c.Scrape.UserAgent == "" {
		c.Scrape.UserAgent = defaultUserAgent
	}
	if c.Storage.Type == "" {
		c.Storage.Type = StorageSQLite
	}
	if c.Storage.SQLite.Path == "" {
		c.Storage.SQLite.Path = defaultSQLitePath
	}
	if c.Storage.Markdown.Dir == "" {
		c.Storage.Markdown.Dir = defaultMarkdownDir
	}
	for i := range c.Feeds {
		if c.Feeds[i].Title == "" {
			c.Feeds[i].Title = c.Feeds[i].Name
		}
	}
}

// validate checks the configuration for semantic errors. All problems
// are collected and returned together so the user can fix them at once.
func (c *Config) validate() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port: %d is out of range 1-65535", c.Server.Port))
	}
	if c.Scrape.Interval < minScrapeInterval {
		errs = append(errs, fmt.Errorf("scrape.interval: must be at least %s", minScrapeInterval))
	}
	if c.Scrape.Timeout < 0 {
		errs = append(errs, errors.New("scrape.timeout: must be positive"))
	}
	switch c.Storage.Type {
	case StorageSQLite, StorageMarkdown:
	default:
		errs = append(errs, fmt.Errorf("storage.type: %q is not one of %q, %q",
			c.Storage.Type, StorageSQLite, StorageMarkdown))
	}
	if c.Retention.MaxAgeDays < 0 {
		errs = append(errs, errors.New("retention.max_age_days: must not be negative"))
	}

	if len(c.Feeds) == 0 {
		errs = append(errs, errors.New("feeds: at least one feed is required"))
	}
	seen := make(map[string]bool, len(c.Feeds))
	for i, f := range c.Feeds {
		prefix := fmt.Sprintf("feeds[%d]", i)
		if !feedNameRe.MatchString(f.Name) {
			errs = append(errs, fmt.Errorf("%s.name: %q must match %s", prefix, f.Name, feedNameRe))
		}
		if seen[f.Name] {
			errs = append(errs, fmt.Errorf("%s.name: duplicate name %q", prefix, f.Name))
		}
		seen[f.Name] = true
		if u, err := url.Parse(f.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s.url: %q is not a valid http(s) URL", prefix, f.URL))
		}
	}

	return errors.Join(errs...)
}
