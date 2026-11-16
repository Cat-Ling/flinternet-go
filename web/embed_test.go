package web_test

import (
	"io/fs"
	"testing"

	"github.com/flinternet/flinternet/web"
)

func TestEmbeddedStaticAndTemplates(t *testing.T) {
	staticFS, err := web.StaticFS()
	if err != nil {
		t.Fatalf("StaticFS failed: %v", err)
	}
	staticRequired := []string{
		"style.css",
		"app.js",
		"masonry.pkgd.min.js",
		"hls.light.min.js",
		"favicon.ico",
	}
	for _, name := range staticRequired {
		data, err := fs.ReadFile(staticFS, name)
		if err != nil {
			t.Fatalf("missing embedded static asset %q: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded static asset %q is empty", name)
		}
	}

	tmplFS, err := web.TemplatesFS()
	if err != nil {
		t.Fatalf("TemplatesFS failed: %v", err)
	}
	tmplRequired := []string{
		"layout.html",
		"index.html",
		"search.html",
		"pin.html",
		"comments.html",
		"user.html",
		"board.html",
		"ideas.html",
		"legal.html",
		"error.html",
	}
	for _, name := range tmplRequired {
		data, err := fs.ReadFile(tmplFS, name)
		if err != nil {
			t.Fatalf("missing embedded template %q: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded template %q is empty", name)
		}
	}
}
