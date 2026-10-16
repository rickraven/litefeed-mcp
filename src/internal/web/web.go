// Package web serves the browser UI: a single static page (embedded into
// the binary) that talks to the REST API, plus an endpoint rendering
// entry bodies from markdown to HTML.
package web

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"litefeed-mcp/internal/storage"
)

//go:embed static
var staticFiles embed.FS

// contentSecurityPolicy only allows the page's own scripts and styles.
// Images and media come from the feeds' sites, so any source is allowed
// for them; everything else is restricted to the application itself.
const contentSecurityPolicy = "default-src 'self'; img-src * data:; media-src *; " +
	"style-src 'self'; script-src 'self'; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// Web serves the UI.
type Web struct {
	store  storage.Store
	static fs.FS
	md     goldmark.Markdown
}

// New creates the UI handler over the given store.
func New(store storage.Store) *Web {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // the embedded directory always exists
	}
	return &Web{
		store:  store,
		static: static,
		// Raw HTML is not rendered (goldmark's default without
		// html.WithUnsafe) and dangerous link schemes such as javascript:
		// are dropped, so feed content cannot inject scripts. Hard wraps
		// keep line breaks of plain text descriptions.
		md: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithRendererOptions(html.WithHardWraps()),
		),
	}
}

// Register adds the UI routes to mux.
func (w *Web) Register(mux *http.ServeMux) {
	mux.Handle("GET /{$}", secure(http.HandlerFunc(w.handleIndex)))
	mux.Handle("GET /static/", secure(http.StripPrefix("/static/", http.FileServerFS(w.static))))
	mux.Handle("GET /entries/{id}/content", secure(http.HandlerFunc(w.handleContent)))
}

// secure adds security and caching headers. Static files are small and
// change with every release, so the browser revalidates them.
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("Content-Security-Policy", contentSecurityPolicy)
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Referrer-Policy", "no-referrer")
		hdr.Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func (w *Web) handleIndex(rw http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(rw, r, w.static, "index.html")
}

// handleContent returns the entry body rendered as an HTML fragment.
func (w *Web) handleContent(rw http.ResponseWriter, r *http.Request) {
	e, err := w.store.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(rw, "entry not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("failed to load entry", "id", r.PathValue("id"), "err", err)
		http.Error(rw, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := w.md.Convert([]byte(e.Content), &buf); err != nil {
		slog.Error("failed to render entry", "id", e.ID, "err", err)
		http.Error(rw, "internal error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Write(buf.Bytes())
}
