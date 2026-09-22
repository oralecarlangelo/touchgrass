package http

import (
	"bytes"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	nethttp "net/http"
)

// docsPrefix is the mount point of the documentation site.
const docsPrefix = "/docs/"

// docsNotFound is the site's own 404 page, served with a 404 status.
const docsNotFound = "404.html"

// Docs serves the embedded documentation site: exact files, directory
// index.html, else the site's 404 page. Without a docs build it answers
// 503 with a precise error. Unlike the SPA there is no index fallback —
// docs URLs map to real files.
func Docs(logger *slog.Logger, docs fs.FS) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		name := strings.TrimPrefix(r.URL.Path, docsPrefix)
		if name == "" || strings.HasSuffix(name, "/") {
			name += indexFile
		}

		if data, err := readDist(docs, name); err == nil {
			nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))

			return
		}

		if data, err := readDist(docs, name+"/"+indexFile); err == nil {
			nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))

			return
		}

		if _, err := readDist(docs, indexFile); err != nil {
			writeError(
				w,
				logger,
				err,
				"docs are not built: run npm run build in docs-site/ and rebuild",
				"docs_not_built",
				nethttp.StatusServiceUnavailable,
			)

			return
		}

		if page, err := readDist(docs, docsNotFound); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(nethttp.StatusNotFound)
			_, _ = w.Write(page)

			return
		}

		nethttp.NotFound(w, r)
	})
}
