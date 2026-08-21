package distributedlog

import (
	"context"
	"testing"
)

type capturedEntry struct {
	level   Level
	message string
	fields  []Field
}

type captureSink struct {
	entries []capturedEntry
}

func (s *captureSink) Write(_ context.Context, level Level, message string, fields []Field) {
	s.entries = append(s.entries, capturedEntry{level: level, message: message, fields: append([]Field(nil), fields...)})
}

func TestLoggerAddsDistributionAndCorrelationFields(t *testing.T) {
	sink := new(captureSink)
	logger := NewWithSink(Config{Service: "gateway", Environment: "test", InstanceID: "node-1"}, sink)
	ctx := WithRun(WithRequestID(context.Background(), "req-1"), "run-1", "session-1")

	logger.Error(ctx, "upload failed", F("stage", "archive"), Err(context.DeadlineExceeded))

	if len(sink.entries) != 1 {
		t.Fatalf("entries = %d", len(sink.entries))
	}
	entry := sink.entries[0]
	if entry.level != LevelError || entry.message != "upload failed" {
		t.Fatalf("entry = %+v", entry)
	}
	want := map[string]any{
		"service": "gateway", "environment": "test", "instance_id": "node-1",
		"request_id": "req-1", "run_id": "run-1", "session_id": "session-1",
		"stage": "archive", "error": context.DeadlineExceeded.Error(),
	}
	for _, field := range entry.fields {
		if value, ok := want[field.Key]; ok {
			if value != field.Value {
				t.Errorf("field %s = %v, want %v", field.Key, field.Value, value)
			}
			delete(want, field.Key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing fields: %v", want)
	}
}

func TestLoggerWithDoesNotMutateParentAndDeduplicatesFields(t *testing.T) {
	sink := new(captureSink)
	parent := NewWithSink(Config{Service: "gateway", Environment: "test", InstanceID: "node-1"}, sink)
	child := parent.With(F("component", "provider"), F("service", "overridden"))

	parent.Info(context.Background(), "parent")
	child.Info(context.Background(), "child", F("component", "router"))

	if got := fieldValue(sink.entries[0].fields, "service"); got != "gateway" {
		t.Fatalf("parent service = %v", got)
	}
	if got := fieldValue(sink.entries[1].fields, "service"); got != "gateway" {
		t.Fatalf("child service = %v", got)
	}
	if got := fieldValue(sink.entries[1].fields, "component"); got != "router" {
		t.Fatalf("child component = %v", got)
	}
}

func TestLoggerRedactsSensitiveFieldsWithoutRedactingTokenCounts(t *testing.T) {
	sink := new(captureSink)
	logger := NewWithSink(Config{Service: "gateway", Environment: "test", InstanceID: "node-1"}, sink)

	logger.Info(context.Background(), "provider result",
		F("api_key", "sk-secret"),
		F("github_access_token", "ghp_secret"),
		F("prompt_tokens", 42),
	)

	fields := sink.entries[0].fields
	if got := fieldValue(fields, "api_key"); got != redactedValue {
		t.Fatalf("api_key = %v", got)
	}
	if got := fieldValue(fields, "github_access_token"); got != redactedValue {
		t.Fatalf("github_access_token = %v", got)
	}
	if got := fieldValue(fields, "prompt_tokens"); got != 42 {
		t.Fatalf("prompt_tokens = %v", got)
	}
}

func fieldValue(fields []Field, key string) any {
	for _, field := range fields {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}
