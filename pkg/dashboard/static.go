package dashboard

import (
	"io/fs"
	"net/http"
	"strings"
)

// StaticHandler returns an http.Handler that serves embedded static files
// with SPA fallback (any path that doesn't match a file serves index.html).
func StaticHandler(staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Try to open the requested file
		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		_, err := staticFS.Open(cleanPath)
		if err != nil {
			// Try with .html extension (Next.js static export generates login.html for /login)
			htmlPath := cleanPath + ".html"
			_, errHTML := staticFS.Open(htmlPath)
			if errHTML == nil {
				// Found with .html extension — rewrite URL to serve it
				r.URL.Path = "/" + htmlPath
			} else {
				// Not found at all — serve index.html (SPA fallback)
				r.URL.Path = "/"
			}
		}

		fileServer.ServeHTTP(w, r)
	})
}
