package distributedlog

import "context"

type correlation struct {
	RequestID string
	TraceID   string
	RunID     string
	SessionID string
}

type correlationContextKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	values := correlationFrom(ctx)
	values.RequestID = requestID
	return context.WithValue(nonNilContext(ctx), correlationContextKey{}, values)
}

func WithTraceID(ctx context.Context, traceID string) context.Context {
	values := correlationFrom(ctx)
	values.TraceID = traceID
	return context.WithValue(nonNilContext(ctx), correlationContextKey{}, values)
}

func WithRun(ctx context.Context, runID, sessionID string) context.Context {
	values := correlationFrom(ctx)
	values.RunID = runID
	values.SessionID = sessionID
	return context.WithValue(nonNilContext(ctx), correlationContextKey{}, values)
}

func RequestID(ctx context.Context) string {
	return correlationFrom(ctx).RequestID
}

func correlationFrom(ctx context.Context) correlation {
	if ctx == nil {
		return correlation{}
	}
	values, _ := ctx.Value(correlationContextKey{}).(correlation)
	return values
}

func correlationFields(ctx context.Context) []Field {
	values := correlationFrom(ctx)
	fields := make([]Field, 0, 4)
	if values.RequestID != "" {
		fields = append(fields, F("request_id", values.RequestID))
	}
	if values.TraceID != "" {
		fields = append(fields, F("trace_id", values.TraceID))
	}
	if values.RunID != "" {
		fields = append(fields, F("run_id", values.RunID))
	}
	if values.SessionID != "" {
		fields = append(fields, F("session_id", values.SessionID))
	}
	return fields
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
