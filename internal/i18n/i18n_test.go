package i18n

import (
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
