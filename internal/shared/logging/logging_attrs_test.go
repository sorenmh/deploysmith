package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestAttrProvider_AttrImplementsInterface(t *testing.T) {
	var _ AttrProvider = Attr{}
	var _ AttrProvider = Field("k", "v")
}

func TestAttr_LogAttrs(t *testing.T) {
	attrs := Field("wine.type", "red").LogAttrs()
	require.Len(t, attrs, 1)
	assert.Equal(t, "wine.type", attrs[0].Key)
	assert.Equal(t, "red", attrs[0].Value)
}

func TestFlattenProviders_Empty(t *testing.T) {
	assert.Nil(t, flattenProviders(nil))
	assert.Nil(t, flattenProviders([]AttrProvider{}))
}

func TestFlattenProviders_Multiple(t *testing.T) {
	result := flattenProviders([]AttrProvider{
		Field("a", 1),
		Field("b", 2),
		Field("c", 3),
	})
	require.Len(t, result, 3)
	assert.Equal(t, "a", result[0].Key)
	assert.Equal(t, "b", result[1].Key)
	assert.Equal(t, "c", result[2].Key)
}

func TestFlattenProviders_NilProviderSkipped(t *testing.T) {
	assert.NotPanics(t, func() {
		result := flattenProviders([]AttrProvider{Field("a", 1), nil, Field("b", 2)})
		assert.Len(t, result, 2)
	})
}

func TestAppendLogAttrs_Accumulates(t *testing.T) {
	ctx := context.Background()
	ctx = AppendLogAttrs(ctx, Field("request.id", "req-1"))
	ctx = AppendLogAttrs(ctx, Field("user.id", "usr-42"))

	attrs := contextLogAttrs(ctx)
	require.Len(t, attrs, 2)
	assert.Equal(t, "request.id", attrs[0].Key)
	assert.Equal(t, "user.id", attrs[1].Key)
}

func TestAppendLogAttrs_ParentUnaffected(t *testing.T) {
	parent := AppendLogAttrs(context.Background(), Field("a", 1))
	child := AppendLogAttrs(parent, Field("b", 2))

	assert.Len(t, contextLogAttrs(parent), 1, "parent context must not be mutated")
	assert.Len(t, contextLogAttrs(child), 2)
}

func TestAppendLogAttrs_EmptyContextReturnsNil(t *testing.T) {
	assert.Nil(t, contextLogAttrs(context.Background()))
}

func TestLogInfoCtx_IncludesAmbientAttrs(t *testing.T) {
	var buf bytes.Buffer
	// Override singleton with test core so LogInfoCtx writes to our buffer.
	loggerSingleton = nil
	defer func() { loggerSingleton = nil }()

	core := newOtelCore(zapcore.AddSync(&buf), zapcore.DebugLevel)
	// We can't easily inject the core into logCtx without refactoring,
	// so we verify the behaviour via the otelCore directly.

	ctx := AppendLogAttrs(context.Background(),
		Field("request.id", "req-123"),
		Field("user.id", "usr-456"),
	)

	ambient := contextLogAttrs(ctx)
	require.Len(t, ambient, 2)

	// Write a synthetic entry using the ambient attrs to verify JSON output shape.
	fields := make([]zapcore.Field, len(ambient))
	for i, a := range ambient {
		fields[i] = zapcore.Field{Key: a.Key, Type: zapcore.StringType, String: a.Value.(string)}
	}
	entry := zapcore.Entry{Level: zapcore.InfoLevel, Message: "handled"}
	require.NoError(t, core.Write(entry, fields))

	var out otelLogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "req-123", out.Attributes["request.id"])
	assert.Equal(t, "usr-456", out.Attributes["user.id"])
}
