package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ---- OTel severity numbers (https://opentelemetry.io/docs/specs/otel/logs/data-model/#field-severitynumber) ----

var otelSeverityNumbers = map[zapcore.Level]int{
	zapcore.DebugLevel:  5,
	zapcore.InfoLevel:   9,
	zapcore.WarnLevel:   13,
	zapcore.ErrorLevel:  17,
	zapcore.DPanicLevel: 21,
	zapcore.PanicLevel:  21,
	zapcore.FatalLevel:  21,
}

// ---- Resource ----

type logResource struct {
	ServiceName    string `json:"service.name,omitempty"`
	ServiceVersion string `json:"service.version,omitempty"`
}

func loadResource() logResource {
	return logResource{
		ServiceName:    os.Getenv("SERVICE_NAME"),
		ServiceVersion: os.Getenv("SERVICE_VERSION"),
	}
}

// ---- OTel log entry (JSON output) ----

type otelLogEntry struct {
	Timestamp      string         `json:"timestamp"`
	LogRecordUID   string         `json:"log.record.uid"`
	SeverityText   string         `json:"severityText"`
	SeverityNumber int            `json:"severityNumber"`
	Body           string         `json:"body"`
	TraceID        string         `json:"traceId,omitempty"`
	SpanID         string         `json:"spanId,omitempty"`
	TraceFlags     string         `json:"traceFlags,omitempty"`
	Resource       logResource    `json:"resource"`
	Attributes     map[string]any `json:"attributes,omitempty"`
}

func newLogRecordUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Reserved field keys that are promoted to top-level OTel fields instead of attributes.
const (
	OtelFieldTraceID    = "traceId"
	OtelFieldSpanID     = "spanId"
	OtelFieldTraceFlags = "traceFlags"
)

// ---- Custom zapcore.Core for OTel JSON output ----

type otelCore struct {
	out      zapcore.WriteSyncer
	level    zapcore.LevelEnabler
	resource logResource
	fields   []zapcore.Field
	mu       sync.Mutex
}

func newOtelCore(out zapcore.WriteSyncer, level zapcore.LevelEnabler) *otelCore {
	return &otelCore{
		out:      out,
		level:    level,
		resource: loadResource(),
	}
}

func (c *otelCore) Enabled(l zapcore.Level) bool {
	return c.level.Enabled(l)
}

func (c *otelCore) With(fields []zapcore.Field) zapcore.Core {
	newFields := make([]zapcore.Field, len(c.fields), len(c.fields)+len(fields))
	copy(newFields, c.fields)
	newFields = append(newFields, fields...)
	return &otelCore{
		out:      c.out,
		level:    c.level,
		resource: c.resource,
		fields:   newFields,
	}
}

func (c *otelCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *otelCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	allFields := append(c.fields, fields...)

	logEntry := otelLogEntry{
		Timestamp:      entry.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		LogRecordUID:   newLogRecordUID(),
		SeverityText:   entry.Level.CapitalString(),
		SeverityNumber: otelSeverityNumbers[entry.Level],
		Body:           entry.Message,
		Resource:       c.resource,
	}

	// Add caller info
	var attrs map[string]any
	if entry.Caller.Defined {
		attrs = make(map[string]any)
		attrs["code.filepath"] = entry.Caller.TrimmedPath()
		attrs["code.lineno"] = entry.Caller.Line
		if entry.Caller.Function != "" {
			attrs["code.function.name"] = entry.Caller.Function
		}
	}

	// Decode zap fields: reserved keys go top-level, rest go to attributes
	if len(allFields) > 0 {
		enc := zapcore.NewMapObjectEncoder()
		for _, f := range allFields {
			f.AddTo(enc)
		}
		for k, v := range enc.Fields {
			switch k {
			case OtelFieldTraceID:
				if s, ok := v.(string); ok {
					logEntry.TraceID = s
				}
			case OtelFieldSpanID:
				if s, ok := v.(string); ok {
					logEntry.SpanID = s
				}
			case OtelFieldTraceFlags:
				if s, ok := v.(string); ok {
					logEntry.TraceFlags = s
				}
			default:
				if attrs == nil {
					attrs = make(map[string]any)
				}
				attrs[k] = v
			}
		}
	}

	if len(attrs) > 0 {
		logEntry.Attributes = attrs
	}

	data, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("otelCore: failed to marshal log entry: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	_, err = c.out.Write(data)
	c.mu.Unlock()
	return err
}

func (c *otelCore) Sync() error {
	return c.out.Sync()
}

// ---- Logger ----

// Logger wraps *zap.Logger and hides zap internals from the rest of the codebase.
// Add domain-specific methods here rather than exposing the underlying zap logger.
type Logger struct {
	inner *zap.Logger
}

func (l *Logger) sugar() *zap.SugaredLogger {
	return l.inner.WithOptions(zap.AddCallerSkip(callerSkipOffset())).Sugar()
}

func (l *Logger) Debug(args ...any) { l.sugar().Debug(args...) }
func (l *Logger) Info(args ...any)  { l.sugar().Info(args...) }
func (l *Logger) Warn(args ...any)  { l.sugar().Warn(args...) }
func (l *Logger) Error(args ...any) { l.sugar().Error(args...) }

func (l *Logger) Debugf(template string, args ...any) { l.sugar().Debugf(template, args...) }
func (l *Logger) Infof(template string, args ...any)  { l.sugar().Infof(template, args...) }
func (l *Logger) Warnf(template string, args ...any)  { l.sugar().Warnf(template, args...) }
func (l *Logger) Errorf(template string, args ...any) { l.sugar().Errorf(template, args...) }

func (l *Logger) Debugw(msg string, keysAndValues ...any) { l.sugar().Debugw(msg, keysAndValues...) }
func (l *Logger) Infow(msg string, keysAndValues ...any)  { l.sugar().Infow(msg, keysAndValues...) }
func (l *Logger) Warnw(msg string, keysAndValues ...any)  { l.sugar().Warnw(msg, keysAndValues...) }
func (l *Logger) Errorw(msg string, keysAndValues ...any) { l.sugar().Errorw(msg, keysAndValues...) }

func (l *Logger) Sync() error { return l.inner.Sync() }

// with and withOptions are package-private — zap types must not leak into public API.
func (l *Logger) with(fields ...zap.Field) *Logger {
	return &Logger{inner: l.inner.With(fields...)}
}
// callerSkipOffset counts the number of consecutive stack frames that belong
// to this package above the call site. Passing that count to zap.AddCallerSkip
// makes zap report the first frame outside the logging package, regardless of
// how many wrapper layers deep the call originated from.
func callerSkipOffset() int {
	const pkg = "internal/logging"
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:]) // skip runtime.Callers + callerSkipOffset
	frames := runtime.CallersFrames(pcs[:n])
	skip := 0
	for {
		f, more := frames.Next()
		if !strings.Contains(f.Function, pkg) {
			return skip
		}
		skip++
		if !more {
			break
		}
	}
	return skip
}

// ---- Logger methods: simple (no context) ----

func (l *Logger) LogDebug(tpl string, args ...any) { l.Debugf(tpl, args...) }
func (l *Logger) LogInfo(tpl string, args ...any)  { l.Infof(tpl, args...) }
func (l *Logger) LogWarn(tpl string, args ...any)  { l.Warnf(tpl, args...) }
func (l *Logger) LogError(err error)               { l.Errorw(err.Error(), "error", err.Error()) }

// ---- Logger methods: context-aware ----
// Extract TraceContext and ambient attrs from ctx; both are included automatically.

func (l *Logger) LogDebugCtx(ctx context.Context, msg string, attrs ...AttrProvider) {
	l.logCtx(ctx, zapcore.DebugLevel, msg, attrs)
}

func (l *Logger) LogInfoCtx(ctx context.Context, msg string, attrs ...AttrProvider) {
	l.logCtx(ctx, zapcore.InfoLevel, msg, attrs)
}

func (l *Logger) LogWarnCtx(ctx context.Context, msg string, attrs ...AttrProvider) {
	l.logCtx(ctx, zapcore.WarnLevel, msg, attrs)
}

// LogErrorCtx logs at ERROR level. If msg is empty the error string is used as the body.
// An "error" attribute is automatically added alongside any extra attrs.
func (l *Logger) LogErrorCtx(ctx context.Context, msg string, err error, attrs ...AttrProvider) {
	if msg == "" && err != nil {
		msg = err.Error()
	}
	if err != nil {
		attrs = append([]AttrProvider{Field("error", err.Error())}, attrs...)
	}
	l.logCtx(ctx, zapcore.ErrorLevel, msg, attrs)
}

func (l *Logger) logCtx(ctx context.Context, level zapcore.Level, msg string, providers []AttrProvider) {
	tc := traceFromContext(ctx)

	// Ambient attrs (from middleware/AppendLogAttrs) come first, then call-site attrs.
	ambient := contextLogAttrs(ctx)
	explicit := flattenProviders(providers)

	allAttrs := make([]Attr, 0, len(ambient)+len(explicit))
	allAttrs = append(allAttrs, ambient...)
	allAttrs = append(allAttrs, explicit...)

	fields := make([]zap.Field, 0, len(allAttrs)+3)
	if tc.TraceID != "" {
		fields = append(fields, zap.String(OtelFieldTraceID, tc.TraceID))
	}
	if tc.SpanID != "" {
		fields = append(fields, zap.String(OtelFieldSpanID, tc.SpanID))
	}
	if tc.TraceFlags != "" {
		fields = append(fields, zap.String(OtelFieldTraceFlags, tc.TraceFlags))
	}
	for _, a := range allAttrs {
		fields = append(fields, zap.Any(a.Key, a.Value))
	}

	target := l.with(fields...)
	switch level {
	case zapcore.DebugLevel:
		target.Debug(msg)
	case zapcore.InfoLevel:
		target.Info(msg)
	case zapcore.WarnLevel:
		target.Warn(msg)
	case zapcore.ErrorLevel:
		target.Error(msg)
	}
}
