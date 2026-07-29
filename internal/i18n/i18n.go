// Package i18n wraps go-i18n: it loads the embedded catalogs once and
// hands out per-request locales resolved from Accept-Language.
package i18n

import (
	"fmt"
	"io/fs"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/lporcheron/quorum/translations"
)

// Translator holds the loaded message catalogs. Safe for concurrent use.
type Translator struct {
	bundle    *goi18n.Bundle
	matcher   language.Matcher
	supported []language.Tag
}

// New loads every embedded catalog. English is the source language.
func New() (*Translator, error) {
	bundle := goi18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)

	entries, err := fs.ReadDir(translations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read translations: %w", err)
	}
	supported := []language.Tag{language.English}
	for _, e := range entries {
		if _, err := bundle.LoadMessageFileFS(translations.FS, e.Name()); err != nil {
			return nil, fmt.Errorf("load catalog %s: %w", e.Name(), err)
		}
	}
	for _, tag := range bundle.LanguageTags() {
		if tag != language.English {
			supported = append(supported, tag)
		}
	}
	return &Translator{
		bundle:    bundle,
		matcher:   language.NewMatcher(supported),
		supported: supported,
	}, nil
}

// Locale resolves language preferences (typically the raw
// Accept-Language header) into a request-scoped locale.
func (t *Translator) Locale(prefs ...string) *Locale {
	lang := language.English
	for _, p := range prefs {
		tags, _, err := language.ParseAcceptLanguage(p)
		if err != nil || len(tags) == 0 {
			continue
		}
		_, idx, _ := t.matcher.Match(tags...)
		lang = t.supported[idx]
		break
	}
	base, _ := lang.Base()
	return &Locale{
		Lang:      base.String(),
		localizer: goi18n.NewLocalizer(t.bundle, lang.String()),
	}
}

// Locale translates messages for one resolved language.
type Locale struct {
	// Lang is the base language code ("en", "fr"), suitable for the
	// <html lang> attribute.
	Lang      string
	localizer *goi18n.Localizer
}

// T returns the translation for id, or the id itself if the message is
// missing — a visible-but-harmless failure mode.
func (l *Locale) T(id string) string {
	msg, err := l.localizer.Localize(&goi18n.LocalizeConfig{MessageID: id})
	if err != nil {
		return id
	}
	return msg
}

// TD returns the translation for id with template data.
func (l *Locale) TD(id string, data map[string]any) string {
	msg, err := l.localizer.Localize(&goi18n.LocalizeConfig{MessageID: id, TemplateData: data})
	if err != nil {
		return id
	}
	return msg
}

// TN returns the plural-aware translation for id with a count. The
// message may use {{.Count}}.
func (l *Locale) TN(id string, n int) string {
	msg, err := l.localizer.Localize(&goi18n.LocalizeConfig{
		MessageID:    id,
		PluralCount:  n,
		TemplateData: map[string]any{"Count": n},
	})
	if err != nil {
		return id
	}
	return msg
}
