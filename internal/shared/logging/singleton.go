package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ANSI colour helpers for pretty logging.
const (
	ansiReset    = "\033[0m"
	ansiDarkGray = "\033[90m"
	ansiCyan     = "\033[36m"
	ansiYellow   = "\033[33m"
	ansiWhite    = "\033[97m"
)

func levelColor(l zapcore.Level) string {
	switch l {
	case zapcore.DebugLevel:
		return "\033[37m" // white
	case zapcore.InfoLevel:
		return "\033[32m" // green
	case zapcore.WarnLevel:
		return "\033[93m" // bright yellow
	default:
		return "\033[31m" // red
	}
}

// prettyCore is a zapcore.Core that renders log lines as coloured human-readable text.
// It stores "with" fields itself so they go through the same coloured key=value formatter
// as fields passed directly to the log call.
type prettyCore struct {
	out    zapcore.WriteSyncer
	level  zapcore.LevelEnabler
	fields []zapcore.Field
	mu     sync.Mutex
}

func newPrettyCore(out zapcore.WriteSyncer, level zapcore.LevelEnabler) *prettyCore {
	return &prettyCore{out: out, level: level}
}

func (c *prettyCore) Enabled(l zapcore.Level) bool { return c.level.Enabled(l) }

func (c *prettyCore) With(fields []zapcore.Field) zapcore.Core {
	merged := make([]zapcore.Field, len(c.fields)+len(fields))
	copy(merged, c.fields)
	copy(merged[len(c.fields):], fields)
	return &prettyCore{out: c.out, level: c.level, fields: merged}
}

func (c *prettyCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *prettyCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	var sb strings.Builder

	sb.WriteString(ansiDarkGray + ent.Time.Format("15:04:05.000") + ansiReset)
	sb.WriteString("  ")
	sb.WriteString(levelColor(ent.Level) + ent.Level.CapitalString() + ansiReset)
	sb.WriteString("  ")
	if ent.Caller.Defined {
		sb.WriteString(ansiCyan + ent.Caller.TrimmedPath() + ansiReset)
		sb.WriteString("  ")
	}
	sb.WriteString(ent.Message)

	all := append(c.fields, fields...)
	for _, f := range all {
		sb.WriteString("\n    ")
		sb.WriteString(ansiYellow + f.Key + ansiReset + "=")
		sb.WriteString(ansiWhite + fieldStr(f) + ansiReset)
	}
	sb.WriteByte('\n')

	c.mu.Lock()
	_, err := c.out.Write([]byte(sb.String()))
	c.mu.Unlock()
	return err
}

func (c *prettyCore) Sync() error { return c.out.Sync() }

// fieldStr renders a zap field value as a plain string.
// Strings are returned as-is; everything else is JSON-marshalled.
func fieldStr(f zapcore.Field) string {
	enc := zapcore.NewMapObjectEncoder()
	f.AddTo(enc)
	v, ok := enc.Fields[f.Key]
	if !ok {
		return "<nil>"
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ---- Logger singleton ----

var loggerSingleton *Logger

// getLogLevel reads LOG_LEVEL (debug|info|warn|error) and defaults to info.
func getLogLevel() zap.AtomicLevel {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return zap.NewAtomicLevelAt(zap.DebugLevel)
	case "warn":
		return zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		return zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		return zap.NewAtomicLevelAt(zap.InfoLevel)
	}
}

// isPretty returns true when LOG_FORMAT=pretty.
func isPretty() bool {
	return strings.ToLower(os.Getenv("LOG_FORMAT")) == "pretty"
}

func GetLogger() *Logger {
	if loggerSingleton != nil {
		return loggerSingleton
	}

	level := getLogLevel()

	var zapLogger *zap.Logger
	if isPretty() {
		core := newPrettyCore(zapcore.AddSync(os.Stdout), level)
		zapLogger = zap.New(core, zap.AddCaller(), zap.Development())
	} else {
		core := newOtelCore(zapcore.AddSync(os.Stdout), level)
		zapLogger = zap.New(core, zap.AddCaller())
	}

	// Bridge slog to use the same format so all packages produce consistent output.
	slogOpts := &slog.HandlerOptions{Level: slog.Level(level.Level())}
	var slogHandler slog.Handler
	if isPretty() {
		slogHandler = slog.NewTextHandler(os.Stdout, slogOpts)
	} else {
		slogHandler = slog.NewJSONHandler(os.Stdout, slogOpts)
	}
	slog.SetDefault(slog.New(slogHandler))

	zapLogger.Info("Logger initialised",
		zap.String("service", os.Getenv("SERVICE_NAME")),
		zap.String("log_level", level.String()),
		zap.String("log_format", os.Getenv("LOG_FORMAT")),
	)

	loggerSingleton = &Logger{inner: zapLogger}
	return loggerSingleton
}

// GetSugaredLogger returns a *Logger. Kept for backwards compatibility — prefer GetLogger().
func GetSugaredLogger() *Logger {
	return GetLogger()
}
