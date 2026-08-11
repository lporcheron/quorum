package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lporcheron/quorum/web"
)

func TestFingerprintedAssetsAreImmutable(t *testing.T) {
	ts, _ := newTestServer(t)

	hash := web.AssetHash("css/app.css")
	if hash == "" {
		t.Fatal("no hash for css/app.css (run `make css` if it is missing)")
	}
	if url := web.AssetURL("css/app.css"); url != "/static/css/app.css?v="+hash {
		t.Fatalf("AssetURL = %q", url)
	}

	fresh, _ := get(t, ts, "/static/css/app.css?v="+hash, nil)
	if cc := fresh.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("fingerprinted Cache-Control = %q, want immutable", cc)
	}
	if got, want := fresh.Header.Get("ETag"), `"`+hash+`"`; got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}

	// A stale fingerprint must not be cached forever under the new bytes.
	stale, _ := get(t, ts, "/static/css/app.css?v=deadbeef", nil)
	if cc := stale.Header.Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("stale fingerprint Cache-Control = %q, want a short lifetime", cc)
	}
}

func TestStaticRevalidatesWithETag(t *testing.T) {
	ts, _ := newTestServer(t)

	first, body := get(t, ts, "/static/js/quorum.js", nil)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a static response")
	}
	if len(body) == 0 {
		t.Fatal("empty body")
	}

	again, againBody := get(t, ts, "/static/js/quorum.js", http.Header{"If-None-Match": []string{etag}})
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("status = %d, want 304", again.StatusCode)
	}
	if againBody != "" {
		t.Errorf("304 carried %d bytes of body", len(againBody))
	}
}

func TestPageLinksFingerprintedAssets(t *testing.T) {
	ts, _ := newTestServer(t)
	_, body := get(t, ts, "/", nil)
	if want := web.AssetURL("css/app.css"); !strings.Contains(body, want) {
		t.Errorf("page does not link %s", want)
	}
}

func TestUnknownStaticFileIs404(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, _ := get(t, ts, "/static/css/nope.css", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
