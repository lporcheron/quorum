package templates

import "context"

type chromeKey int

const (
	ctxInstanceName chromeKey = iota
	ctxCurrentPath
)

// WithChrome attaches the per-request page chrome (instance name,
// current path for the language switcher) to the render context.
func WithChrome(ctx context.Context, instanceName, path string) context.Context {
	ctx = context.WithValue(ctx, ctxInstanceName, instanceName)
	return context.WithValue(ctx, ctxCurrentPath, path)
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
