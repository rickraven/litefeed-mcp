package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"litefeed-mcp/internal/entry"
	"litefeed-mcp/internal/storage/markdown"
)

// newTestServer serves the UI over a store with one entry whose body
// contains markdown, raw HTML and a javascript: link.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	store, err := markdown.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &entry.Entry{
		Feed:  "f",
		Title: "t",
		Link:  "https://x/1",
		Content: "**bold** ![pic](https://x/a.png)\n\n" +
			"<script>alert(1)</script>\n\n[bad](javascript:alert(1)) [good](https://x/b)\n\nline one\nline two",
	}
	e.Normalize(time.Now())
	store.Save(context.Background(), e)

	mux := http.NewServeMux()
	New(store).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, e.ID
}

// get returns status, headers and body of a GET request.
func get(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func TestStaticFiles(t *testing.T) {
	srv, _ := newTestServer(t)
	cases := map[string]string{
		"/":                "text/html",
		"/static/app.js":   "text/javascript",
		"/static/theme.js": "text/javascript",
		"/static/app.css":  "text/css",
	}
	for path, ctype := range cases {
		code, hdr, body := get(t, srv.URL+path)
		if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), ctype) || body == "" {
			t.Errorf("%s: status %d, type %q", path, code, hdr.Get("Content-Type"))
		}
		if !strings.Contains(hdr.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("%s: missing CSP", path)
		}
	}
	if code, _, _ := get(t, srv.URL+"/nope"); code != 404 {
		t.Errorf("unknown path: status %d", code)
	}
}

func TestContentIsSanitized(t *testing.T) {
	srv, id := newTestServer(t)
	code, hdr, body := get(t, srv.URL+"/entries/"+id+"/content")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
		t.Fatalf("status %d, type %q", code, hdr.Get("Content-Type"))
	}
	for _, want := range []string{
		"<strong>bold</strong>",
		`<img src="https://x/a.png" alt="pic">`,
		`<a href="https://x/b">good</a>`,
		"line one<br>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"<script", "javascript:"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains %q:\n%s", bad, body)
		}
	}
	if code, _, _ := get(t, srv.URL+"/entries/missing/content"); code != 404 {
		t.Errorf("missing entry: status %d", code)
	}
}
