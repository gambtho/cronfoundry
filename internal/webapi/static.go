package webapi

import (
	"io/fs"
	"net/http"
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
		// If the exact file exists in dist, serve it directly.
		f, err := sub.Open(r.URL.Path)
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
