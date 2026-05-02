package webapi

import (
	"io/fs"
	"net/http"
	"strings"
)

// staticHandler serves the embedded React SPA.
// Unknown paths serve index.html so React Router handles client-side routing.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "UI not built. Run `make web` first.", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// io/fs paths are slash-separated with NO leading slash. r.URL.Path
		// always starts with "/". Strip it before probing the embedded FS,
		// otherwise sub.Open("/assets/x.js") returns ErrNotExist for every
		// real asset and we fall back to index.html with text/html — which
		// browsers refuse to execute as a JS module under strict MIME.
		probe := strings.TrimPrefix(r.URL.Path, "/")
		if probe == "" {
			probe = "."
		}
		f, err := sub.Open(probe)
		if err != nil {
			// Fall back to index.html for SPA client-side routing.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		_ = f.Close()
		fileServer.ServeHTTP(w, r)
	})
}
