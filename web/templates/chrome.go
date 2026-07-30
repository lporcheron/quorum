package templates

import "context"

type chromeKey int

const (
	ctxInstanceName chromeKey = iota
	ctxCurrentPath
	ctxCSRF
)

// WithChrome attaches the per-request page chrome (instance name,
// current path for the language switcher, CSRF token) to the render
// context.
func WithChrome(ctx context.Context, instanceName, path, csrf string) context.Context {
	ctx = context.WithValue(ctx, ctxInstanceName, instanceName)
	ctx = context.WithValue(ctx, ctxCurrentPath, path)
	return context.WithValue(ctx, ctxCSRF, csrf)
}

func csrfValue(ctx context.Context) string {
	v, _ := ctx.Value(ctxCSRF).(string)
	return v
}

func instanceName(ctx context.Context) string {
	if v, _ := ctx.Value(ctxInstanceName).(string); v != "" {
		return v
	}
	return "Quorum"
}

func currentPath(ctx context.Context) string {
	v, _ := ctx.Value(ctxCurrentPath).(string)
	return v
}
