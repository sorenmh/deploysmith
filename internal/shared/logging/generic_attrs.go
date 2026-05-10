package logging

func WithError(err error) Attr {
	return Attr{Key: "error", Value: err.Error()}
}

func WithErrorIf(err error) Attr {
	if err != nil {
		return WithError(err)
	}
	return Attr{}
}

func WithTrace(traceID string, spanID string) []Attr {
	attr := []Attr{}
	if traceID != "" {
		attr = append(attr, Attr{Key: "traceId", Value: traceID})
	}
	if spanID != "" {
		attr = append(attr, Attr{Key: "spanId", Value: spanID})
	}
	return attr
}
