package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestGetLogger(t *testing.T) {
	t.Run("should return a logger instance", func(t *testing.T) {
		assert.NotNil(t, GetLogger())
	})

	t.Run("should configure production logger when ENV is PROD", func(t *testing.T) {
		_ = os.Setenv("ENV", "PROD")
		defer func() { _ = os.Unsetenv("ENV") }()
		loggerSingleton = nil
		assert.NotNil(t, GetLogger())
	})

	t.Run("should configure development logger when ENV is DEV", func(t *testing.T) {
		_ = os.Setenv("ENV", "DEV")
		defer func() { _ = os.Unsetenv("ENV") }()
		loggerSingleton = nil
		assert.NotNil(t, GetLogger())
	})
}

func TestGetSugaredLogger(t *testing.T) {
	t.Run("should return a sugared logger instance", func(t *testing.T) {
		logger := GetSugaredLogger()
		assert.NotNil(t, logger)
	})

	t.Run("should return a *Logger", func(t *testing.T) {
		logger := GetSugaredLogger()
		assert.NotNil(t, logger)
	})
}

func TestLogFunctions(t *testing.T) {
	t.Run("LogDebug should not panic", func(t *testing.T) {
		assert.NotPanics(t, func() { LogDebug("test debug: %s", "param") })
	})

	t.Run("LogInfo should not panic", func(t *testing.T) {
		assert.NotPanics(t, func() { LogInfo("test info: %s", "param") })
	})

	t.Run("LogWarn should not panic", func(t *testing.T) {
		assert.NotPanics(t, func() { LogWarn("test warn: %s", "param") })
	})

	t.Run("LogError should not panic", func(t *testing.T) {
		assert.NotPanics(t, func() { LogError(assert.AnError) })
	})
}

// ---- OTel core unit tests ----

func TestOtelCore_OutputShape(t *testing.T) {
	var buf bytes.Buffer
	core := newOtelCore(zapcore.AddSync(&buf), zapcore.DebugLevel)

	ts, _ := time.Parse(time.RFC3339, "2026-04-04T12:00:00Z")
	entry := zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Time:    ts,
		Message: "hello world",
		Caller:  zapcore.NewEntryCaller(1, "api/deploy.go", 42, true),
	}
	require.NoError(t, core.Write(entry, nil))

	var out otelLogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))

	assert.Equal(t, "INFO", out.SeverityText)
	assert.Equal(t, 9, out.SeverityNumber)
	assert.Equal(t, "hello world", out.Body)
	assert.Equal(t, "2026-04-04T12:00:00.000Z", out.Timestamp)
	assert.Equal(t, float64(42), out.Attributes["code.lineno"]) // JSON numbers decode as float64
}

func TestOtelCore_SeverityNumbers(t *testing.T) {
	cases := []struct {
		level    zapcore.Level
		wantText string
		wantNum  int
	}{
		{zapcore.DebugLevel, "DEBUG", 5},
		{zapcore.InfoLevel, "INFO", 9},
		{zapcore.WarnLevel, "WARN", 13},
		{zapcore.ErrorLevel, "ERROR", 17},
	}

	for _, tc := range cases {
		t.Run(tc.wantText, func(t *testing.T) {
			var buf bytes.Buffer
			core := newOtelCore(zapcore.AddSync(&buf), zapcore.DebugLevel)
			entry := zapcore.Entry{Level: tc.level, Message: "test"}
			require.NoError(t, core.Write(entry, nil))

			var out otelLogEntry
			require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
			assert.Equal(t, tc.wantText, out.SeverityText)
			assert.Equal(t, tc.wantNum, out.SeverityNumber)
		})
	}
}

func TestOtelCore_TraceFieldsPromotedToTopLevel(t *testing.T) {
	var buf bytes.Buffer
	core := newOtelCore(zapcore.AddSync(&buf), zapcore.DebugLevel)

	fields := []zapcore.Field{
		{Key: OtelFieldTraceID, Type: zapcore.StringType, String: "abc123"},
		{Key: OtelFieldSpanID, Type: zapcore.StringType, String: "def456"},
		{Key: OtelFieldTraceFlags, Type: zapcore.StringType, String: "01"},
		{Key: "http.method", Type: zapcore.StringType, String: "GET"},
	}

	entry := zapcore.Entry{Level: zapcore.InfoLevel, Message: "request handled"}
	require.NoError(t, core.Write(entry, fields))

	var out otelLogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))

	assert.Equal(t, "abc123", out.TraceID)
	assert.Equal(t, "def456", out.SpanID)
	assert.Equal(t, "01", out.TraceFlags)
	assert.Equal(t, "GET", out.Attributes["http.method"])
	// trace fields must not appear inside attributes
	assert.NotContains(t, out.Attributes, OtelFieldTraceID)
	assert.NotContains(t, out.Attributes, OtelFieldSpanID)
}

func TestOtelCore_With(t *testing.T) {
	var buf bytes.Buffer
	core := newOtelCore(zapcore.AddSync(&buf), zapcore.DebugLevel)
	child := core.With([]zapcore.Field{
		{Key: "service", Type: zapcore.StringType, String: "viino-api"},
	})

	entry := zapcore.Entry{Level: zapcore.InfoLevel, Message: "child log"}
	require.NoError(t, child.Write(entry, nil))

	var out otelLogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "viino-api", out.Attributes["service"])
}

func TestTraceContext_RoundTrip(t *testing.T) {
	tc := TraceContext{TraceID: "trace1", SpanID: "span1", TraceFlags: "01"}
	ctx := CtxWithTraceContext(context.Background(), tc)

	got := traceFromContext(ctx)
	assert.Equal(t, "trace1", got.TraceID)
	assert.Equal(t, "span1", got.SpanID)
	assert.Equal(t, "01", got.TraceFlags)
}

func TestTraceContext_EmptyOnMissingKey(t *testing.T) {
	got := traceFromContext(context.Background())
	assert.Empty(t, got.TraceID)
	assert.Empty(t, got.SpanID)
}

func TestLogInfoCtx(t *testing.T) {
	assert.NotPanics(t, func() {
		ctx := CtxWithTraceContext(context.Background(), TraceContext{
			TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
			SpanID:  "00f067aa0ba902b7",
		})
		LogInfoCtx(ctx, "request handled",
			Field("http.method", "POST"),
			Field("http.status_code", 200),
		)
	})
}

func TestLogErrorCtx(t *testing.T) {
	assert.NotPanics(t, func() {
		LogErrorCtx(context.Background(), "test", assert.AnError, Field("component", "test"))
	})
}

func TestField(t *testing.T) {
	a := Field("key", "value")
	assert.Equal(t, "key", a.Key)
	assert.Equal(t, "value", a.Value)
}

func TestOtelCore_LogRecordUID(t *testing.T) {
	var buf bytes.Buffer
	core := newOtelCore(zapcore.AddSync(&buf), zapcore.DebugLevel)
	require.NoError(t, core.Write(zapcore.Entry{Level: zapcore.InfoLevel, Message: "test"}, nil))

	var out otelLogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.NotEmpty(t, out.LogRecordUID, "every log entry must have a log.record.uid")
	assert.Len(t, out.LogRecordUID, 32, "uid should be 16 random bytes as hex")

	buf.Reset()
	require.NoError(t, core.Write(zapcore.Entry{Level: zapcore.InfoLevel, Message: "second"}, nil))
	var out2 otelLogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out2))
	assert.NotEqual(t, out.LogRecordUID, out2.LogRecordUID, "each entry must get a unique uid")
}
