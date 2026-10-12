package observability

import (
	"context"
	"errors"
	"math"
	"reflect"
	"time"
)

const (
	maxLogCodeBytes  = 64
	maxFieldKeyBytes = 32
	defaultMaxFields = 16
	hardMaxFields    = 16
)

var (
	ErrInvalid  = errors.New("observability: invalid input")
	ErrLimit    = errors.New("observability: configured or cardinality limit reached")
	ErrConflict = errors.New("observability: metric definition conflict")
	ErrSink     = errors.New("observability: log sink failed")
)

// Level is the severity of a structured log record.
type Level uint8

const (
	LevelDebug Level = iota + 1
	LevelInfo
	LevelWarn
	LevelError
)

// FieldKind identifies the scalar representation of a log field.
type FieldKind uint8

const (
	FieldInt FieldKind = iota + 1
	FieldFloat
	FieldBool
)

// Field is a bounded scalar-only log attribute. Its private representation
// intentionally has no string or arbitrary-payload value.
type Field struct {
	key  string
	kind FieldKind
	i    int64
	f    float64
	b    bool
}

// IntField creates an integer-valued structured log field.
func IntField(key string, value int64) Field {
	return Field{key: key, kind: FieldInt, i: value}
}

// FloatField creates a finite floating-point structured log field. Emit
// rejects non-finite values.
func FloatField(key string, value float64) Field {
	return Field{key: key, kind: FieldFloat, f: value}
}

// BoolField creates a boolean-valued structured log field.
func BoolField(key string, value bool) Field {
	return Field{key: key, kind: FieldBool, b: value}
}

// Key returns the field's key.
func (f Field) Key() string { return f.key }

// Kind returns the field's scalar kind.
func (f Field) Kind() FieldKind { return f.kind }

// Int64 returns the integer value and whether the field is an integer.
func (f Field) Int64() (int64, bool) { return f.i, f.kind == FieldInt }

// Float64 returns the floating-point value and whether the field is a float.
func (f Field) Float64() (float64, bool) { return f.f, f.kind == FieldFloat }

// Bool returns the boolean value and whether the field is a boolean.
func (f Field) Bool() (bool, bool) { return f.b, f.kind == FieldBool }

// LogRecord is one bounded, detached structured record. Code is a controlled
// classification, not a message. Fields contain only numeric or boolean data.
type LogRecord struct {
	At     time.Time
	Level  Level
	Code   string
	Trace  TraceContext
	Fields []Field
}

// LogSink is an injected synchronous destination. Implementations own their
// own lifecycle and are responsible for any behavior beyond this call.
type LogSink interface {
	WriteLog(LogRecord) error
}

// LogSinkFunc adapts a function to LogSink.
type LogSinkFunc func(LogRecord) error

// WriteLog invokes f with one record.
func (f LogSinkFunc) WriteLog(r LogRecord) error { return f(r) }

// LoggerConfig supplies a caller-owned sink, an optional clock, and a field
// limit. MaxFields defaults to 16 and may only lower that hard cap.
type LoggerConfig struct {
	Sink      LogSink
	Clock     func() time.Time
	MaxFields int
}

// Logger emits bounded records directly to an injected sink. Calls are
// synchronous and are not queued or retained by Logger. Concurrent Emit calls
// are safe when the injected sink and clock are themselves concurrency-safe.
type Logger struct {
	sink      LogSink
	clock     func() time.Time
	maxFields int
}

// NewLogger constructs a logger with no global or external dependencies.
func NewLogger(cfg LoggerConfig) (*Logger, error) {
	if isNil(cfg.Sink) || cfg.MaxFields < 0 || cfg.MaxFields > hardMaxFields {
		return nil, ErrInvalid
	}
	if cfg.MaxFields == 0 {
		cfg.MaxFields = defaultMaxFields
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Logger{sink: cfg.Sink, clock: cfg.Clock, maxFields: cfg.MaxFields}, nil
}

// Emit validates and writes a structured log record. Caller-provided context
// is used only for optional trace correlation; cancellation does not suppress
// an observation. Sink errors are deliberately not returned verbatim.
func (l *Logger) Emit(ctx context.Context, level Level, code string, fields ...Field) error {
	if l == nil || isNil(l.sink) || ctx == nil || !validLevel(level) || !validToken(code, maxLogCodeBytes) || len(fields) > l.maxFields {
		return ErrInvalid
	}
	owned := make([]Field, len(fields))
	for i, field := range fields {
		if !validToken(field.key, maxFieldKeyBytes) {
			return ErrInvalid
		}
		for j := 0; j < i; j++ {
			if owned[j].key == field.key {
				return ErrInvalid
			}
		}
		switch field.kind {
		case FieldInt, FieldBool:
		case FieldFloat:
			if math.IsNaN(field.f) || math.IsInf(field.f, 0) {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		owned[i] = field
	}
	trace, _ := TraceFromContext(ctx)
	record := LogRecord{At: l.clock().UTC(), Level: level, Code: code, Trace: trace, Fields: owned}
	if err := l.sink.WriteLog(record); err != nil {
		return ErrSink
	}
	return nil
}

func validLevel(level Level) bool {
	return level >= LevelDebug && level <= LevelError
}

func validToken(value string, maxBytes int) bool {
	if len(value) == 0 || len(value) > maxBytes || !asciiLetter(value[0]) {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !asciiLetter(c) && (c < '0' || c > '9') && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func asciiLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
