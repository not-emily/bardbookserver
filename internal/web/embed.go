// Package web holds the admin UI's templates and static assets, embedded so a
// plain `go build` produces a working interface.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Templates parses every page against the shared layout. Parsed once at
// startup, so a broken template fails the binary rather than one request.
type Templates struct {
	pages map[string]*template.Template
}

func Parse() (*Templates, error) {
	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	out := &Templates{pages: map[string]*template.Template{}}
	for _, name := range names {
		base := path.Base(name)
		if base == "layout.html" {
			continue
		}
		page, err := template.ParseFS(templateFS, "templates/layout.html", name)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		out.pages[base] = page
	}
	return out, nil
}

func (t *Templates) Render(w io.Writer, page string, data any) error {
	tmpl, ok := t.pages[page]
	if !ok {
		return fmt.Errorf("web: no template %q", page)
	}
	return tmpl.ExecuteTemplate(w, "layout", data)
}

// Static serves the vendored assets, so the UI needs no outbound access.
func Static() (http.Handler, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub))), nil
}
