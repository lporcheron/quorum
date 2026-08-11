package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/lporcheron/quorum/web"
)

// gzipHeader asks for gzip explicitly. Go's transport only decompresses
// transparently when it added the header itself, so setting it by hand is
// what lets a test see the encoded response.
var gzipHeader = http.Header{"Accept-Encoding": []string{"gzip"}}

func TestHTMLIsCompressed(t *testing.T) {
	ts, _ := newTestServer(t)

	plain, plainBody := get(t, ts, "/", nil)
	if plain.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", plain.StatusCode)
	}

	resp, body := get(t, ts, "/", gzipHeader)
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", resp.Header.Get("Vary"))
	}
	if len(body) >= len(plainBody) {
		t.Errorf("gzipped body is %d bytes, uncompressed is %d", len(body), len(plainBody))
	}

	zr, err := gzip.NewReader(strings.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if string(got) != plainBody {
		t.Error("decompressed body differs from the uncompressed response")
	}
}

func TestCompressionSkipsIncompressibleResponses(t *testing.T) {
	ts, _ := newTestServer(t)

	// woff2 is compressed already; gzipping it only adds bytes.
	font, _ := get(t, ts, "/static/fonts/public-sans-latin-var.woff2", gzipHeader)
	if font.StatusCode != http.StatusOK {
		t.Fatalf("font status = %d, want 200", font.StatusCode)
	}
	if got := font.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("font Content-Encoding = %q, want none", got)
	}

	// Short bodies lose to the gzip header and trailer.
	health, healthBody := get(t, ts, "/healthz", gzipHeader)
	if got := health.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("healthz Content-Encoding = %q, want none (%d byte body)", got, len(healthBody))
	}
}

func TestNoCompressionWithoutAcceptEncoding(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := get(t, ts, "/", http.Header{"Accept-Encoding": []string{"gzip;q=0"}})
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none for gzip;q=0", got)
	}
	if !strings.Contains(body, "<html") {
		t.Error("body is not the HTML page")
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"":                         false,
		"br":                       false,
		"gzip":                     true,
		"gzip, deflate, br":        true,
		"br;q=1.0, gzip;q=0.8":     true,
		"gzip;q=0":                 false,
		"gzip;q=0.0":               false,
		"identity;q=1, gzip;q=0":   false,
		"deflate, gzip;q=nonsense": true,
	}
	for header, want := range cases {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

// A 304 must not gain a Content-Encoding: there is no body to encode.
func TestNotModifiedIsNotCompressed(t *testing.T) {
	ts, _ := newTestServer(t)
	first, _ := get(t, ts, "/static/css/app.css", nil)
	header := http.Header{
		"If-None-Match":   []string{first.Header.Get("ETag")},
		"Accept-Encoding": []string{"gzip"},
	}
	resp, body := get(t, ts, "/static/css/app.css", header)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("304 Content-Encoding = %q, want none", got)
	}
	if body != "" {
		t.Errorf("304 carried a body of %d bytes", len(body))
	}
}

// Range requests describe an uncompressed byte offset, so they must reach
// the file server untouched.
func TestRangeRequestIsNotCompressed(t *testing.T) {
	ts, _ := newTestServer(t)
	header := http.Header{
		"Range":           []string{"bytes=0-99"},
		"Accept-Encoding": []string{"gzip"},
	}
	resp, body := get(t, ts, "/static/css/app.css", header)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	if len(body) != 100 {
		t.Errorf("body = %d bytes, want 100", len(body))
	}
}

// The compressed body must be byte-identical to the file on disk.
func TestCompressedAssetRoundTrips(t *testing.T) {
	ts, _ := newTestServer(t)
	want, err := web.StaticFS.ReadFile("static/js/htmx.min.js")
	if err != nil {
		t.Fatalf("read embedded asset: %v", err)
	}

	resp, body := get(t, ts, web.AssetURL("js/htmx.min.js"), gzipHeader)
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	// The file server sets Content-Length from the file; compressing must
	// drop it rather than leave it describing the wrong bytes.
	if cl := resp.Header.Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length = %q, encoded body is %d bytes", cl, len(body))
	}
	zr, err := gzip.NewReader(strings.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if string(got) != string(want) {
		t.Error("decompressed asset differs from the embedded file")
	}
}
