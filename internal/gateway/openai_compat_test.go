package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type openAICompatUpstreamCapture struct {
	mu   sync.Mutex
	body map[string]any
}

func newOpenAICompatTestService(t *testing.T, upstreamURL string) *Service {
	t.Helper()
	svc, err := NewService(Options{Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.Mem.providers = map[int]*Provider{1: {
		ID: 1, Name: "upstream", Type: 1, Key: "secret", BaseURL: upstreamURL + "/v1",
		Models: "test-model", Status: 1, IsDefault: 1, Weight: 1,
		Capabilities: ProviderCapabilities{Chat: true, Tools: true, Images: true}, QualityTier: 3,
	}}
	return svc
}

func newOpenAICompatUpstream(t *testing.T) (*httptest.Server, *openAICompatUpstreamCapture) {
	t.Helper()
	capture := &openAICompatUpstreamCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		capture.mu.Lock()
		capture.body = body
		capture.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"upstream-id\",\"model\":\"test-model-v2\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"upstream-id\",\"model\":\"test-model-v2\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"upstream-id\",\"model\":\"test-model-v2\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":1}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	return upstream, capture
}

func TestOpenAIChatCompletionsNonStreaming(t *testing.T) {
	upstream, capture := newOpenAICompatUpstream(t)
	svc := newOpenAICompatTestService(t, upstream.URL)
	requestBody := `{
		"model":"test-model",
		"messages":[
			{"role":"system","content":"system rules"},
			{"role":"developer","content":"developer rules"},
			{"role":"user","content":"Hi"}
		],
		"temperature":0.25,
		"max_tokens":42,
		"unknown_client_extension":true
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody))
	res := httptest.NewRecorder()
	svc.handleOpenAIChatCompletions(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["object"] != "chat.completion" || response["model"] != "test-model-v2" {
		t.Fatalf("response=%#v", response)
	}
	choices := response["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "Hello" {
		t.Fatalf("message=%#v", message)
	}
	usage := response["usage"].(map[string]any)
	if usage["total_tokens"] != float64(8) {
		t.Fatalf("usage=%#v", usage)
	}

	capture.mu.Lock()
	forwarded := capture.body
	capture.mu.Unlock()
	if forwarded["temperature"] != 0.25 || forwarded["max_tokens"] != float64(42) || forwarded["stream"] != true {
		t.Fatalf("forwarded controls=%#v", forwarded)
	}
	messages := forwarded["messages"].([]any)
	if messages[0].(map[string]any)["content"] != "system rules\n\ndeveloper rules" {
		t.Fatalf("forwarded messages=%#v", messages)
	}
}

func TestOpenAIChatCompletionsStreaming(t *testing.T) {
	upstream, _ := newOpenAICompatUpstream(t)
	svc := newOpenAICompatTestService(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"test-model","messages":[{"role":"user","content":"Hi"}],
		"stream":true,"stream_options":{"include_usage":true}
	}`))
	res := httptest.NewRecorder()
	svc.handleOpenAIChatCompletions(res, req)

	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d content-type=%q body=%s", res.Code, res.Header().Get("Content-Type"), res.Body.String())
	}
	body := res.Body.String()
	for _, expected := range []string{`"object":"chat.completion.chunk"`, `"content":"Hello"`, `"finish_reason":"stop"`, `"choices":[],"created"`, `data: [DONE]`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream missing %q: %s", expected, body)
		}
	}
}

func TestOpenAIChatCompletionsRejectsUnsupportedN(t *testing.T) {
	svc, err := NewService(Options{Approve: true, NoTools: true, Cwd: t.TempDir(), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"Hi"}],"n":2}`))
	res := httptest.NewRecorder()
	svc.handleOpenAIChatCompletions(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "only n=1") {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
