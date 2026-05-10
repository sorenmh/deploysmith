package logging

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// InboundHTTPLog carries OTel semantic attributes for an inbound HTTP request (server-side).
// https://opentelemetry.io/docs/specs/semconv/http/http-spans/#http-server
//
// Usage:
//
//	LogInfoCtx(ctx, "request handled",
//	    InboundHTTP(r.Method, "/offers/{wineType}/{country}").WithStatus(200).WithDuration(elapsed),
//	)
type InboundHTTPLog struct {
	Method     string
	Route      string
	StatusCode int
	Duration   time.Duration
	Query      string
	SizeBytes  int
	ClientIP   string
	UserAgent  string
	RemoteAddr string
	Headers    map[string]string
}

func InboundHTTPRequest(req *http.Request) InboundHTTPLog {
	return InboundHTTPLog{
		Method: req.Method,
		Route:  req.URL.Path,
	}
}

// InboundHTTP returns an InboundHTTPLog for the given HTTP method and route pattern.
func InboundHTTP(method, route string) InboundHTTPLog {
	return InboundHTTPLog{Method: method, Route: route, Headers: make(map[string]string)}
}

// WithStatus sets the HTTP response status code.
func (h InboundHTTPLog) WithStatus(code int) InboundHTTPLog {
	h.StatusCode = code
	return h
}

// WithDuration sets the total request duration.
func (h InboundHTTPLog) WithDuration(d time.Duration) InboundHTTPLog {
	h.Duration = d
	return h
}

func (h InboundHTTPLog) WithQuery(query string) InboundHTTPLog {
	h.Query = query
	return h
}

func (h InboundHTTPLog) WithSizeBytes(size int) InboundHTTPLog {
	h.SizeBytes = size
	return h
}

func (h InboundHTTPLog) WithClientIP(ip string) InboundHTTPLog {
	h.ClientIP = ip
	return h
}

func (h InboundHTTPLog) WithUserAgent(ua string) InboundHTTPLog {
	h.UserAgent = ua
	return h
}

func (h InboundHTTPLog) WithRemoteAddr(addr string) InboundHTTPLog {
	h.RemoteAddr = addr
	return h
}

func (h InboundHTTPLog) WithHeader(key, value string) InboundHTTPLog {
	h.Headers[key] = value
	return h
}

func (h InboundHTTPLog) WithXRealIP(xRealIP string) InboundHTTPLog {
	h.Headers["X-Real-IP"] = xRealIP
	return h
}

func (h InboundHTTPLog) WithXForwardedFor(xForwardedFor string) InboundHTTPLog {
	h.Headers["X-Forwarded-For"] = xForwardedFor
	return h
}

// LogAttrs implements AttrProvider.
func (h InboundHTTPLog) LogAttrs() []Attr {
	attrs := []Attr{
		{Key: "http.request.method", Value: h.Method},
		{Key: "http.route", Value: h.Route},
	}
	if h.StatusCode != 0 {
		attrs = append(attrs, Attr{Key: "http.response.status_code", Value: h.StatusCode})
	}
	if h.Duration > 0 {
		attrs = append(attrs, Attr{Key: "http.request.duration_ms", Value: h.Duration.Milliseconds()})
	}
	attrs = append(attrs, Attr{Key: "url.query", Value: h.Query})
	if h.SizeBytes != 0 {
		attrs = append(attrs, Attr{Key: "http.response.body.size", Value: h.SizeBytes})
	}
	if h.ClientIP != "" {
		attrs = append(attrs, Attr{Key: "client.address", Value: h.ClientIP})
	}
	if h.UserAgent != "" {
		attrs = append(attrs, Attr{Key: "user_agent.original", Value: h.UserAgent})
	}
	if h.RemoteAddr != "" {
		if host, port, err := net.SplitHostPort(h.RemoteAddr); err == nil {
			attrs = append(attrs, Attr{Key: "network.peer.address", Value: host})
			attrs = append(attrs, Attr{Key: "network.peer.port", Value: port})
		} else {
			attrs = append(attrs, Attr{Key: "network.peer.address", Value: h.RemoteAddr})
		}
	}
	for key, value := range h.Headers {
		attrs = append(attrs, Attr{Key: "http.request.header." + strings.ToLower(key), Value: value})
	}
	return attrs
}

// OutboundHTTPLog carries OTel semantic attributes for an outbound HTTP request (client-side).
// https://opentelemetry.io/docs/specs/semconv/http/http-spans/#http-client
//
// Usage:
//
//	LogInfoCtx(ctx, "zyte api called",
//	    OutboundHTTP(http.MethodPost, "https://api.zyte.com/v1/extract").WithStatus(200).WithDuration(elapsed),
//	)
type OutboundHTTPLog struct {
	Method     string
	URL        string
	StatusCode int
	Duration   time.Duration
}

// OutboundHTTP returns an OutboundHTTPLog for the given HTTP method and full URL.
func OutboundHTTP(method, url string) OutboundHTTPLog {
	return OutboundHTTPLog{Method: method, URL: url}
}

// WithStatus sets the HTTP response status code.
func (h OutboundHTTPLog) WithStatus(code int) OutboundHTTPLog {
	h.StatusCode = code
	return h
}

// WithDuration sets the total request duration.
func (h OutboundHTTPLog) WithDuration(d time.Duration) OutboundHTTPLog {
	h.Duration = d
	return h
}

// LogAttrs implements AttrProvider.
func (h OutboundHTTPLog) LogAttrs() []Attr {
	attrs := []Attr{
		{Key: "http.request.method", Value: h.Method},
		{Key: "url.full", Value: h.URL},
	}
	if h.StatusCode != 0 {
		attrs = append(attrs, Attr{Key: "http.response.status_code", Value: h.StatusCode})
	}
	if h.Duration > 0 {
		attrs = append(attrs, Attr{Key: "http.request.duration_ms", Value: h.Duration.Milliseconds()})
	}
	return attrs
}
