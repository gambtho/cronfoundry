package webapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Static-asset paths must NOT fall through to index.html (text/html). Browsers'
// strict MIME policy refuses to execute JS modules served as text/html.
//
// Regression: r.URL.Path starts with "/", but io/fs paths are slash-separated
// with NO leading slash. sub.Open("/assets/index.js") was returning
// ErrNotExist for every real asset, dropping us into the SPA fallback. After
// the fix, /assets/<hash>.js gets a JavaScript Content-Type from
// mime.TypeByExtension via http.FileServer.
func TestStaticHandler_JSAssetServedAsJavaScript(t *testing.T) {
	sub, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		t.Skip("no embedded web/dist (UI not built); skipping")
	}
	var jsName string
	_ = fs.WalkDir(sub, "assets", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".js") {
			jsName = strings.TrimPrefix(path, "assets/")
			return fs.SkipAll
		}
		return nil
	})
	if jsName == "" {
		t.Skip("no JS asset in embedded dist; skipping")
	}

	h := staticHandler()
	req := httptest.NewRequest(http.MethodGet, "/assets/"+jsName, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	ct := rr.Result().Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/html") {
		t.Fatalf("/assets/%s served as text/html — SPA fallback bug regressed (Content-Type=%q)", jsName, ct)
	}
	if !strings.Contains(ct, "javascript") {
		t.Errorf("/assets/%s Content-Type = %q, want a javascript MIME", jsName, ct)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("/assets/%s status = %d, want 200", jsName, rr.Code)
	}
}
