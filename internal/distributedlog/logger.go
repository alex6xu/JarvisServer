// Package distributedlog provides structured, correlation-aware logging with
// a pluggable sink. The first sink uses go-zero's rotating file logger; remote
// collectors can be added without changing business call sites.
package distributedlog

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"
)

type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelError Level = "error"
)

type Field struct {
	Key   string
	Value any
}

func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

func Err(err error) Field {
	if err == nil {
		return F("error", "")
	}
	return F("error", err.Error())
}

// Sink is the extension point for a future remote log collector.
// Implementations must be safe for concurrent use and must not panic.
type Sink interface {
	Write(ctx context.Context, level Level, message string, fields []Field)
}

type Config struct {
	Service     string
	Environment string
	InstanceID  string
}

type Logger struct {
	sink     Sink
	identity []Field
	scoped   []Field
}

const redactedValue = "[REDACTED]"

func New(config Config) *Logger {
	return NewWithSink(config, logxSink{})
}

func NewWithSink(config Config, sink Sink) *Logger {
	if sink == nil {
		sink = logxSink{}
	}
	config = config.withDefaults()
	return &Logger{
		sink: sink,
		identity: []Field{
			F("service", config.Service),
			F("environment", config.Environment),
			F("instance_id", config.InstanceID),
		},
	}
}

func (c Config) withDefaults() Config {
	c.Service = strings.TrimSpace(c.Service)
	c.Environment = strings.TrimSpace(c.Environment)
	c.InstanceID = strings.TrimSpace(c.InstanceID)
	if c.Service == "" {
		c.Service = "jarvis-gateway"
	}
	if c.Environment == "" {
		c.Environment = "development"
	}
	if c.InstanceID == "" {
		c.InstanceID = strings.TrimSpace(os.Getenv("JARVIS_INSTANCE_ID"))
	}
	if c.InstanceID == "" {
		host, err := os.Hostname()
		if err != nil || strings.TrimSpace(host) == "" {
			host = "unknown"
		}
		c.InstanceID = host + "-" + strconv.Itoa(os.Getpid())
	}
	return c
}

func (l *Logger) With(fields ...Field) *Logger {
	if l == nil {
		return nil
	}
	scoped := make([]Field, 0, len(l.scoped)+len(fields))
	scoped = append(scoped, l.scoped...)
	scoped = append(scoped, fields...)
	return &Logger{sink: l.sink, identity: l.identity, scoped: normalizeFields(scoped)}
}

func (l *Logger) Debug(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, LevelDebug, message, fields)
}

func (l *Logger) Info(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, LevelInfo, message, fields)
}

func (l *Logger) Error(ctx context.Context, message string, fields ...Field) {
	l.write(ctx, LevelError, message, fields)
}

func (l *Logger) write(ctx context.Context, level Level, message string, fields []Field) {
	if l == nil || l.sink == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	all := make([]Field, 0, len(l.identity)+len(l.scoped)+len(fields)+5)
	all = append(all, l.scoped...)
	all = append(all, fields...)
	// Identity and correlation fields are authoritative and cannot be spoofed
	// by a component-specific field with the same name.
	all = append(all, l.identity...)
	all = append(all, correlationFields(ctx)...)
	l.sink.Write(ctx, level, message, normalizeFields(all))
}

func normalizeFields(fields []Field) []Field {
	out := make([]Field, 0, len(fields))
	positions := make(map[string]int, len(fields))
	for _, field := range fields {
		field.Key = strings.TrimSpace(field.Key)
		if field.Key == "" {
			continue
		}
		if sensitiveFieldKey(field.Key) {
			field.Value = redactedValue
		}
		if pos, ok := positions[field.Key]; ok {
			out[pos] = field
			continue
		}
		positions[field.Key] = len(out)
		out = append(out, field)
	}
	return out
}

func sensitiveFieldKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(strings.TrimSpace(key)))
	switch key {
	case "authorization", "cookie", "set_cookie", "api_key", "password", "secret", "token":
		return true
	}
	for _, suffix := range []string{"_api_key", "_password", "_secret", "_token"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

type logxSink struct{}

func (logxSink) Write(ctx context.Context, level Level, message string, fields []Field) {
	logFields := make([]logx.LogField, 0, len(fields))
	for _, field := range fields {
		logFields = append(logFields, logx.Field(field.Key, field.Value))
	}
	logger := logx.WithContext(ctx).WithCallerSkip(3)
	switch level {
	case LevelDebug:
		logger.Debugw(message, logFields...)
	case LevelError:
		logger.Errorw(message, logFields...)
	default:
		logger.Infow(message, logFields...)
	}
}
