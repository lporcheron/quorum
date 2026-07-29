package templates

import (
	"context"
	"strings"
)

// RenderEmail renders the transactional email shell to an HTML string.
func RenderEmail(ctx context.Context, title, body, ctaLabel, ctaURL string) (string, error) {
	var sb strings.Builder
	if err := email(title, body, ctaLabel, ctaURL).Render(ctx, &sb); err != nil {
		return "", err
	}
	return sb.String(), nil
}
