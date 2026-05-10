package logging

import "context"

// AttrProvider is implemented by any value that can contribute structured OTel log attributes.
// Both Attr and the typed domain builders (InboundHTTPLog, MongoQuery, etc.) implement this interface,
// so they can all be passed interchangeably to LogInfoCtx, LogWarnCtx, etc.
type AttrProvider interface {
	LogAttrs() []Attr
}

// Attr is a single key-value log attribute.
type Attr struct {
	Key   string
	Value any
}

// Field constructs an Attr. Use this for one-off attributes that do not have a typed builder.
func Field(key string, value any) Attr {
	return Attr{Key: key, Value: value}
}

// LogAttrs implements AttrProvider so Attr can be passed directly to log functions.
func (a Attr) LogAttrs() []Attr { return []Attr{a} }

// flattenProviders collects attrs from all providers into a single slice.
// A nil interface value is skipped safely.
func flattenProviders(providers []AttrProvider) []Attr {
	if len(providers) == 0 {
		return nil
	}
	out := make([]Attr, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			out = append(out, p.LogAttrs()...)
		}
	}
	return out
}

// ---- Context-embedded ambient attributes ----
// Middleware can call AppendLogAttrs once and every downstream LogXxxCtx call
// will automatically include those attributes without the caller having to repeat them.

type logAttrsKey struct{}

// AppendLogAttrs adds attributes to the context. They are automatically included in
// every subsequent LogXxxCtx call that uses this context or a child of it.
// Multiple calls accumulate; child contexts do not affect parent contexts.
func AppendLogAttrs(ctx context.Context, providers ...AttrProvider) context.Context {
	existing := contextLogAttrs(ctx)
	merged := make([]Attr, len(existing), len(existing)+len(providers))
	copy(merged, existing)
	for _, p := range providers {
		if p != nil {
			merged = append(merged, p.LogAttrs()...)
		}
	}
	return context.WithValue(ctx, logAttrsKey{}, merged)
}

// contextLogAttrs reads the ambient attrs stored by AppendLogAttrs.
// Returns nil (not an empty slice) when none are present.
func contextLogAttrs(ctx context.Context) []Attr {
	if attrs, ok := ctx.Value(logAttrsKey{}).([]Attr); ok {
		return attrs
	}
	return nil
}
