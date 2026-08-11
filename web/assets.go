package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"sync"
)

// assetHashes maps a path relative to static/ ("css/app.css") to a short
// hex digest of the file's bytes. The files are baked into the binary,
// so the map is computed once and is then constant for the process.
var assetHashes = sync.OnceValue(func() map[string]string {
	hashes := make(map[string]string)
	// A read error here means a corrupt binary, which cannot be fixed at
	// runtime: skip the file and let AssetURL fall back to an unversioned
	// URL, which is merely cached for less time.
	_ = fs.WalkDir(StaticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a partial manifest still works
		}
		body, err := StaticFS.ReadFile(p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(body)
		hashes[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:6])
		return nil
	})
	return hashes
})

// AssetURL returns the URL of a file under static/ with a digest of its
// content attached: "css/app.css" → "/static/css/app.css?v=1a2b3c4d5e6f".
// The server marks those responses immutable, so browsers keep them
// without revalidating, and a build that changes a file changes its URL.
func AssetURL(name string) string {
	if h := assetHashes()[name]; h != "" {
		return "/static/" + name + "?v=" + h
	}
	return "/static/" + name
}

// AssetHash returns the content digest of a file under static/, or ""
// when no such file is embedded.
func AssetHash(name string) string {
	return assetHashes()[name]
}
