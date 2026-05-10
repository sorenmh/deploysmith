package logging

import "context"

// ---- Package-level thin wrappers (delegate to singleton) ----

func LogDebug(tpl string, args ...any) { GetLogger().LogDebug(tpl, args...) }
func LogInfo(tpl string, args ...any)  { GetLogger().LogInfo(tpl, args...) }
func LogWarn(tpl string, args ...any)  { GetLogger().LogWarn(tpl, args...) }
func LogError(err error)               { GetLogger().LogError(err) }

func LogDebugCtx(ctx context.Context, msg string, attrs ...AttrProvider) {
	GetLogger().LogDebugCtx(ctx, msg, attrs...)
}
func LogInfoCtx(ctx context.Context, msg string, attrs ...AttrProvider) {
	GetLogger().LogInfoCtx(ctx, msg, attrs...)
}
func LogWarnCtx(ctx context.Context, msg string, attrs ...AttrProvider) {
	GetLogger().LogWarnCtx(ctx, msg, attrs...)
}
func LogErrorCtx(ctx context.Context, msg string, err error, attrs ...AttrProvider) {
	GetLogger().LogErrorCtx(ctx, msg, err, attrs...)
}
