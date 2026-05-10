package logging

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboundHTTPLog_ImplementsAttrProvider(t *testing.T) {
	var _ AttrProvider = InboundHTTPLog{}
	var _ AttrProvider = InboundHTTP("GET", "/wines")
}

func TestInboundHTTPLog_RequiredFields(t *testing.T) {
	attrs := InboundHTTP("POST", "/api/wines").LogAttrs()
	assertAttr(t, attrs, "http.request.method", "POST")
	assertAttr(t, attrs, "http.route", "/api/wines")
}

func TestInboundHTTPLog_OptionalStatusCode(t *testing.T) {
	withoutStatus := InboundHTTP("GET", "/wines").LogAttrs()
	assert.False(t, hasAttrKey(withoutStatus, "http.response.status_code"))

	withStatus := InboundHTTP("GET", "/wines").WithStatus(200).LogAttrs()
	assertAttr(t, withStatus, "http.response.status_code", 200)
}

func TestInboundHTTPLog_OptionalDuration(t *testing.T) {
	withoutDuration := InboundHTTP("GET", "/wines").LogAttrs()
	assert.False(t, hasAttrKey(withoutDuration, "http.request.duration_ms"))

	withDuration := InboundHTTP("GET", "/wines").WithDuration(250 * time.Millisecond).LogAttrs()
	assertAttr(t, withDuration, "http.request.duration_ms", int64(250))
}

func TestInboundHTTPLog_BuilderImmutable(t *testing.T) {
	base := InboundHTTP("GET", "/wines")
	with200 := base.WithStatus(200)
	with404 := base.WithStatus(404)
	assertAttr(t, with200.LogAttrs(), "http.response.status_code", 200)
	assertAttr(t, with404.LogAttrs(), "http.response.status_code", 404)
}

func TestOutboundHTTPLog_ImplementsAttrProvider(t *testing.T) {
	var _ AttrProvider = OutboundHTTPLog{}
	var _ AttrProvider = OutboundHTTP("GET", "https://api.zyte.com")
}

func TestOutboundHTTPLog_RequiredFields(t *testing.T) {
	attrs := OutboundHTTP("POST", "https://api.zyte.com/v1/extract").LogAttrs()
	assertAttr(t, attrs, "http.request.method", "POST")
	assertAttr(t, attrs, "url.full", "https://api.zyte.com/v1/extract")
}

func TestOutboundHTTPLog_UsesURLFullNotRoute(t *testing.T) {
	attrs := OutboundHTTP("GET", "https://example.com/path").LogAttrs()
	assert.True(t, hasAttrKey(attrs, "url.full"))
	assert.False(t, hasAttrKey(attrs, "http.route"), "outbound logs must not emit http.route")
}

func TestOutboundHTTPLog_OptionalFields(t *testing.T) {
	attrs := OutboundHTTP("GET", "https://example.com").
		WithStatus(201).
		WithDuration(100 * time.Millisecond).
		LogAttrs()
	assertAttr(t, attrs, "http.response.status_code", 201)
	assertAttr(t, attrs, "http.request.duration_ms", int64(100))
}

// ---- helpers ----

func assertAttr(t *testing.T, attrs []Attr, key string, want any) {
	t.Helper()
	for _, a := range attrs {
		if a.Key == key {
			assert.Equal(t, want, a.Value)
			return
		}
	}
	require.Failf(t, "attr not found", "key %q not found in attrs", key)
}

func hasAttrKey(attrs []Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}
