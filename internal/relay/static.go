package relay

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler serves the console files with an SPA fallback to index.html.
// /api and /ws are matched by more specific mux patterns and never get here.
func (s *Server) staticHandler() http.Handler {
	fsys := s.cfg.WebFS
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fsys == nil {
			writeError(w, http.StatusNotFound, "web console not bundled; pass --web-dir")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFileFS(w, r, fsys, name)
			return
		}
		// SPA fallback
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
