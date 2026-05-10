package logging

import (
	"context"

	"github.com/oklog/ulid/v2"
)

// ---- Trace context ----

type traceContextKey struct{}

// TraceContext holds OpenTelemetry trace propagation fields.
type TraceContext struct {
	TraceID    string
	SpanID     string
	TraceFlags string
}

// CtxWithTraceContext stores a TraceContext in the given context.
func CtxWithTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

func CtxWithTrace(ctx context.Context, traceID string, spanID string) context.Context {
	return CtxWithTraceContext(ctx, TraceContext{
		TraceID: traceID,
		SpanID:  spanID,
	})
}

func CtxCreateTrace(ctx context.Context, traceIdInp *string) context.Context {
	var traceId string
	if traceIdInp != nil {
		traceId = *traceIdInp
	} else {
		traceId = generateID()
	}
	return CtxWithTrace(ctx, traceId, generateID())
}

func GetTraceIDFromContext(ctx context.Context) string {
	tc := traceFromContext(ctx)
	return tc.TraceID
}

func GetSpanIDFromContext(ctx context.Context) string {
	tc := traceFromContext(ctx)
	return tc.SpanID
}

func generateID() string {
	return ulid.Make().String()
}

func traceFromContext(ctx context.Context) TraceContext {
	if tc, ok := ctx.Value(traceContextKey{}).(TraceContext); ok {
		return tc
	}
	return TraceContext{}
}
