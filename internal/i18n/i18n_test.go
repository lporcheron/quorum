package i18n

import "testing"

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
