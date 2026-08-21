package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alex6xu/jarvisserver/internal/distributedlog"
)

func TestRequestLogMiddlewareAddsRequestIDAndCapturesResponse(t *testing.T) {
	var gotRequestID string
	handler := requestLogMiddleware(distributedlog.New(distributedlog.Config{}))(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = requestID(r)
		writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/upload", nil)
	req.Header.Set("X-Request-ID", "client-request_123")
	res := httptest.NewRecorder()

	handler(res, req)

	if gotRequestID != "client-request_123" {
		t.Fatalf("request id in context = %q", gotRequestID)
	}
	if res.Header().Get("X-Request-ID") != gotRequestID {
		t.Fatalf("response request id = %q", res.Header().Get("X-Request-ID"))
	}
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestRequestLogMiddlewareReplacesUnsafeRequestIDAndPreservesFlush(t *testing.T) {
	var gotRequestID string
	handler := requestLogMiddleware(distributedlog.New(distributedlog.Config{}))(func(w http.ResponseWriter, r *http.Request) {
		gotRequestID = requestID(r)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("logging response writer must preserve http.Flusher")
		}
		_, _ = w.Write([]byte("event: ready\n\n"))
		flusher.Flush()
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/agent/runs/run_1/events", nil)
	req.Header.Set("X-Request-ID", "unsafe request id\nvalue")
	res := httptest.NewRecorder()

	handler(res, req)

	if gotRequestID == "" || gotRequestID == req.Header.Get("X-Request-ID") {
		t.Fatalf("unsafe request id was not replaced: %q", gotRequestID)
	}
	if !res.Flushed {
		t.Fatal("underlying response was not flushed")
	}
}

func TestIncomingRequestIDValidation(t *testing.T) {
	for _, value := range []string{"request-1", "request_2", "request.3", " ABC-123 "} {
		if incomingRequestID(value) == "" {
			t.Errorf("valid request id %q was rejected", value)
		}
	}
	for _, value := range []string{"", "contains space", "line\nbreak"} {
		if incomingRequestID(value) != "" {
			t.Errorf("unsafe request id %q was accepted", value)
		}
	}
}
