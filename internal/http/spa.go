package http

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	nethttp "net/http"
)

// DefaultDevProxy is the Vite dev server target in dev mode.
const DefaultDevProxy = "http://127.0.0.1:5173"

// indexFile is the SPA entrypoint, also the fallback for client routes.
const indexFile = "index.html"

// SPA serves the embedded UI with an index.html fallback for client-side
// routes. Without a web build it answers 503 with a precise error.
func SPA(logger *slog.Logger, dist fs.FS) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = indexFile
		}

		data, err := readDist(dist, name)
		if err != nil {
			data, err = readDist(dist, indexFile)
		}

		if err != nil {
			writeError(
				w,
				logger,
				err,
				"web UI is not built: run npm run build in web/ and rebuild",
				"spa_not_built",
				nethttp.StatusServiceUnavailable,
			)

			return
		}

		nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

// readDist reads one embedded file, rejecting invalid paths.
func readDist(dist fs.FS, name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrNotExist
	}

	return fs.ReadFile(dist, name)
}

// DevProxy forwards UI requests to the Vite dev server.
func DevProxy(logger *slog.Logger, target string) nethttp.Handler {
	return devProxy(logger, target, nil)
}

// devProxy builds the reverse proxy, using transport when non-nil so tests
// can stub the backend without sockets.
func devProxy(logger *slog.Logger, target string, transport nethttp.RoundTripper) nethttp.Handler {
	parsed, err := url.Parse(target)
	if err != nil {
		return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			writeError(w, logger, err, "dev proxy is misconfigured", "dev_proxy_error", nethttp.StatusBadGateway)
		})
	}

	proxy := httputil.NewSingleHostReverseProxy(parsed)

	if transport != nil {
		proxy.Transport = transport
	}

	proxy.ErrorHandler = func(w nethttp.ResponseWriter, _ *nethttp.Request, err error) {
		writeError(w, logger, err, "vite dev server is unreachable", "dev_proxy_error", nethttp.StatusBadGateway)
	}

	return proxy
}
