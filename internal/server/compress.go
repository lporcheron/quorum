package server

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// minCompressSize is the body size below which the gzip header and
// trailer cost more bytes than the compression saves.
const minCompressSize = 1024

// compress gzips text-shaped responses. It matters most for the poll
// grid, whose markup is long and highly repetitive, and for the CSS and
// JS bundles: ~90 KB of assets go out as ~26 KB.
//
// Range requests and HEAD are passed through untouched: both describe a
// body by its uncompressed length.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		// The response varies on the request's encoding whether or not
		// this one ends up compressed: a shared cache must never hand a
		// gzipped body to a client that did not ask for one.
		w.Header().Add("Vary", "Accept-Encoding")

		cw := &gzipWriter{ResponseWriter: w}
		defer cw.close()
		next.ServeHTTP(cw, r)
	})
}

// gzipWriter holds the first bytes of the body back until it knows
// whether compressing is worth it: the decision needs the content type
// (which handlers may leave to sniffing) and the body size.
type gzipWriter struct {
	http.ResponseWriter
	status  int    // pending status code; 0 until WriteHeader
	buf     []byte // body withheld while undecided
	gz      *gzip.Writer
	decided bool
	noGzip  bool // a bodiless status ruled compression out
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.decided {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.status = code
	// Informational, 204 and 304 responses carry no body, and a
	// Content-Encoding on them would be a lie.
	if code < http.StatusOK || code == http.StatusNoContent || code == http.StatusNotModified {
		w.noGzip = true
		// No body to release, so commit cannot fail on a write.
		_ = w.commit()
	}
}

func (w *gzipWriter) Write(p []byte) (int, error) {
	if !w.decided {
		w.buf = append(w.buf, p...)
		if len(w.buf) < minCompressSize && w.Header().Get("Content-Encoding") == "" {
			return len(p), nil
		}
		if err := w.commit(); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

// commit settles on an encoding, sends the header and releases whatever
// body was withheld.
func (w *gzipWriter) commit() error {
	w.decided = true
	h := w.Header()

	// A handler that set Content-Encoding itself has already encoded the
	// body; encoding it twice would corrupt it.
	useGzip := !w.noGzip && h.Get("Content-Encoding") == "" &&
		len(w.buf) >= minCompressSize && compressibleType(w.contentType())
	if useGzip {
		// net/http sniffs an unset content type from the bytes it sends,
		// which are now gzip: name the real type before it guesses.
		if h.Get("Content-Type") == "" {
			h.Set("Content-Type", w.contentType())
		}
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		w.gz = gzip.NewWriter(w.ResponseWriter)
	}

	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.status)

	body := w.buf
	w.buf = nil
	if len(body) == 0 {
		return nil
	}
	var err error
	if w.gz != nil {
		_, err = w.gz.Write(body)
	} else {
		_, err = w.ResponseWriter.Write(body)
	}
	return err
}

// close finishes the gzip stream, or sends a body that stayed under the
// compression threshold.
func (w *gzipWriter) close() {
	if !w.decided {
		_ = w.commit()
	}
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

// Flush pushes what is buffered so far to the client.
func (w *gzipWriter) Flush() {
	if !w.decided {
		_ = w.commit()
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer to http.ResponseController.
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// contentType is the type the client will see: the declared one, or the
// one net/http would sniff from the withheld bytes.
func (w *gzipWriter) contentType() string {
	if ct := w.Header().Get("Content-Type"); ct != "" {
		return ct
	}
	return http.DetectContentType(w.buf)
}

// compressibleType reports whether a content type is text-shaped enough
// to gain from gzip. Images, PNG icons and woff2 fonts are compressed
// already and only grow.
func compressibleType(ct string) bool {
	ct, _, _ = strings.Cut(ct, ";")
	ct = strings.TrimSpace(strings.ToLower(ct))
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/javascript", "application/json", "application/xml",
		"application/manifest+json", "image/svg+xml":
		return true
	}
	return false
}

// acceptsGzip parses Accept-Encoding, honouring an explicit "gzip;q=0"
// refusal.
func acceptsGzip(header string) bool {
	for _, coding := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(coding, ";")
		if strings.TrimSpace(token) != "gzip" {
			continue
		}
		for _, param := range strings.Split(params, ";") {
			name, value, ok := strings.Cut(param, "=")
			if !ok || strings.TrimSpace(name) != "q" {
				continue
			}
			q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			return err != nil || q > 0
		}
		return true
	}
	return false
}
