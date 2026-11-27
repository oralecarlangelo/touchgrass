package http

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	nethttp "net/http"
)

// DefaultDevProxy is the Vite dev server target in dev mode.
const DefaultDevProxy = "http://127.0.0.1:5173"

// indexFile is the SPA entrypoint, also the fallback for client routes.
const indexFile = "index.html"

var fingerprintedAsset = regexp.MustCompile(`^assets/[A-Za-z0-9_.-]+-[A-Za-z0-9_-]{8,}\.[a-z0-9]+$`)

// SPA serves the embedded UI with an index.html fallback for client-side
// routes. Without a web build it answers 503 with a precise error.
func SPA(logger *slog.Logger, dist fs.FS) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = indexFile
		}

		if strings.HasPrefix(name, "assets/") {
			serveAsset(w, r, dist, name)
			return
		}

		data, err := readDist(dist, name)
		if err != nil {
			name = indexFile
			data, err = readDist(dist, name)
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

		if name == indexFile {
			w.Header().Set("Cache-Control", "no-cache")
		}

		nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

func serveAsset(w nethttp.ResponseWriter, r *nethttp.Request, dist fs.FS, name string) {
	if strings.HasSuffix(name, ".gz") {
		nethttp.NotFound(w, r)

		return
	}

	// Only fingerprinted build output can be retained across deployments.
	if fingerprintedAsset.MatchString(name) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css") {
		w.Header().Add("Vary", "Accept-Encoding")

		if acceptsGzip(r.Header.Values("Accept-Encoding")) && r.Header.Get("Range") == "" {
			if data, err := readDist(dist, name+".gz"); err == nil {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))

				return
			}
		}
	}

	data, err := readDist(dist, name)
	if err != nil {
		w.Header().Del("Cache-Control")
		nethttp.NotFound(w, r)

		return
	}

	nethttp.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// An explicit gzip preference takes precedence over the wildcard.
func acceptsGzip(values []string) bool {
	var wildcard bool

	for _, value := range values {
		for member := range strings.SplitSeq(value, ",") {
			coding, parameter, hasParameter := strings.Cut(member, ";")
			coding = strings.ToLower(strings.TrimSpace(coding))
			accepted := true

			if hasParameter {
				key, quality, ok := strings.Cut(parameter, "=")
				q, err := strconv.ParseFloat(strings.TrimSpace(quality), 64)
				accepted = ok && strings.TrimSpace(key) == "q" && err == nil && q > 0 && q <= 1
			}

			switch coding {
			case "gzip":
				return accepted
			case "*":
				wildcard = accepted
			}
		}
	}

	return wildcard
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
