package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/lporcheron/quorum/translations"
)

// TestCatalogParity guarantees "i18n complet": every message exists in
// both languages, so no page ever mixes them.
func TestCatalogParity(t *testing.T) {
	keys := func(name string) map[string]bool {
		t.Helper()
		raw, err := translations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var doc map[string]any
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		out := make(map[string]bool, len(doc))
		for k := range doc {
			out[k] = true
		}
		return out
	}

	en, fr := keys("en.toml"), keys("fr.toml")
	for k := range en {
		if !fr[k] {
			t.Errorf("key %q missing from fr.toml", k)
		}
	}
	for k := range fr {
		if !en[k] {
			t.Errorf("key %q missing from en.toml", k)
		}
	}
}

func TestLocaleResolution(t *testing.T) {
	tr, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		accept   string
		wantLang string
	}{
		{"fr-FR,fr;q=0.9,en;q=0.8", "fr"},
		{"fr", "fr"},
		{"en-US,en;q=0.9", "en"},
		{"de-DE,de;q=0.9", "en"}, // unsupported language falls back to English
		{"", "en"},
		{"garbage;;;", "en"},
	}
	for _, tc := range cases {
		loc := tr.Locale(tc.accept)
		if loc.Lang != tc.wantLang {
			t.Errorf("Locale(%q).Lang = %q, want %q", tc.accept, loc.Lang, tc.wantLang)
		}
	}
}

func TestTranslation(t *testing.T) {
	tr, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := tr.Locale("fr").T("home.tagline"); got != "Proposez des dates. Partagez un lien. Comptez les voix." {
		t.Errorf("fr tagline = %q", got)
	}
	if got := tr.Locale("en").T("home.tagline"); got != "Propose dates. Share one link. Count the votes." {
		t.Errorf("en tagline = %q", got)
	}
	if got := tr.Locale("en").T("does.not.exist"); got != "does.not.exist" {
		t.Errorf("missing message = %q, want the id back", got)
	}
}

// TestNoOrphanKeys fails when a catalog key is no longer referenced
// anywhere in the source tree, so dead copy never accumulates.
func TestNoOrphanKeys(t *testing.T) {
	raw, err := translations.FS.ReadFile("en.toml")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// go-i18n message ids are the dotted paths of leaf tables (tables
	// whose values are the plural-form strings).
	var keys []string
	var flatten func(prefix string, node map[string]any)
	flatten = func(prefix string, node map[string]any) {
		leaf := true
		for _, v := range node {
			if _, isTable := v.(map[string]any); isTable {
				leaf = false
				break
			}
		}
		if leaf {
			keys = append(keys, prefix)
			return
		}
		for k, v := range node {
			if sub, ok := v.(map[string]any); ok {
				flatten(prefix+"."+k, sub)
			}
		}
	}
	for k, v := range doc {
		if sub, ok := v.(map[string]any); ok {
			flatten(k, sub)
		}
	}

	// Keys assembled at runtime from these prefixes cannot be grepped.
	dynamicPrefixes := []string{"dashboard.status_", "space.role_", "theme."}

	var src strings.Builder
	root := filepath.Join("..", "..")
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".tools" || name == "bin" || name == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, "_templ.go") {
			return nil
		}
		if strings.HasSuffix(name, ".templ") || strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".js") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	code := src.String()
outer:
	for _, key := range keys {
		if strings.Contains(code, `"`+key+`"`) {
			continue
		}
		for _, p := range dynamicPrefixes {
			if strings.HasPrefix(key, p) {
				continue outer
			}
		}
		t.Errorf("catalog key %q is referenced nowhere; delete it or use it", key)
	}
}
