package templates

import "context"

type chromeKey int

const (
	ctxInstanceName chromeKey = iota
	ctxCurrentPath
	ctxCSRF
	ctxIsInstanceAdmin
	ctxTheme
)

// WithChrome attaches the per-request page chrome (instance name,
// current path for the language switcher, CSRF token, instance-admin
// flag) to the render context.
func WithChrome(ctx context.Context, instanceName, path, csrf string, isInstanceAdmin bool, theme string) context.Context {
	ctx = context.WithValue(ctx, ctxInstanceName, instanceName)
	ctx = context.WithValue(ctx, ctxCurrentPath, path)
	ctx = context.WithValue(ctx, ctxCSRF, csrf)
	ctx = context.WithValue(ctx, ctxIsInstanceAdmin, isInstanceAdmin)
	return context.WithValue(ctx, ctxTheme, theme)
}

// theme is the forced theme ("light"/"dark"), empty for system.
func theme(ctx context.Context) string {
	v, _ := ctx.Value(ctxTheme).(string)
	return v
}

func isInstanceAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(ctxIsInstanceAdmin).(bool)
	return v
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
