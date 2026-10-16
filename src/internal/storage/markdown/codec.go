package markdown

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"litefeed-mcp/internal/entry"
)

// frontMatterDelim separates the YAML front matter from the body.
const frontMatterDelim = "---"

// errNoFrontMatter is returned for files that do not start with a
// front matter block.
var errNoFrontMatter = errors.New("missing front matter")

// encode serializes an entry into the file format:
//
//	---
//	<yaml metadata>
//	---
//
//	<markdown body>
func encode(e *entry.Entry) ([]byte, error) {
	meta, err := yaml.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal front matter: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString(frontMatterDelim + "\n")
	buf.Write(meta)
	buf.WriteString(frontMatterDelim + "\n\n")
	if body := strings.Trim(e.Content, "\n"); body != "" {
		buf.WriteString(body)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// decode parses a file produced by encode. Files edited by hand are
// tolerated as long as they keep the front matter block: Windows line
// endings and a UTF-8 BOM are accepted.
func decode(data []byte) (*entry.Entry, error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	if !strings.HasPrefix(text, frontMatterDelim+"\n") {
		return nil, errNoFrontMatter
	}
	rest := text[len(frontMatterDelim)+1:]

	// The closing delimiter is either at the very beginning of rest (empty
	// metadata) or on its own line further down.
	var meta, body string
	switch {
	case strings.HasPrefix(rest, frontMatterDelim+"\n") || rest == frontMatterDelim:
		body = strings.TrimPrefix(rest, frontMatterDelim)
	default:
		i := strings.Index(rest, "\n"+frontMatterDelim+"\n")
		if i < 0 {
			if !strings.HasSuffix(rest, "\n"+frontMatterDelim) {
				return nil, errors.New("unterminated front matter")
			}
			i = len(rest) - len(frontMatterDelim) - 1
		}
		meta = rest[:i]
		body = rest[min(i+len(frontMatterDelim)+2, len(rest)):]
	}

	var e entry.Entry
	if err := yaml.Unmarshal([]byte(meta), &e); err != nil {
		return nil, fmt.Errorf("parse front matter: %w", err)
	}
	e.Content = strings.Trim(body, "\n")
	return &e, nil
}
