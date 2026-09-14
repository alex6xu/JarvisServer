package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/provider"
)

const maxOpenAICompatRequestBytes = 8 << 20

type openAIChatCompletionRequest struct {
	Model               string               `json:"model"`
	Messages            []openAIInputMessage `json:"messages"`
	Tools               []openAIInputTool    `json:"tools,omitempty"`
	Stream              bool                 `json:"stream,omitempty"`
	Temperature         *float64             `json:"temperature,omitempty"`
	TopP                *float64             `json:"top_p,omitempty"`
	MaxTokens           *int                 `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                 `json:"max_completion_tokens,omitempty"`
	Stop                json.RawMessage      `json:"stop,omitempty"`
	PresencePenalty     *float64             `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64             `json:"frequency_penalty,omitempty"`
	Seed                *int64               `json:"seed,omitempty"`
	ResponseFormat      json.RawMessage      `json:"response_format,omitempty"`
	ToolChoice          json.RawMessage      `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                `json:"parallel_tool_calls,omitempty"`
	N                   *int                 `json:"n,omitempty"`
	StreamOptions       *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
}

type openAIInputMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

type openAIInputTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// proxyTool only describes a caller-provided function. The compatibility
// endpoint forwards it to the model but never executes it in Jarvis.
type proxyTool struct {
	name, description string
	schema            json.RawMessage
}

func (t proxyTool) Name() string            { return t.name }
func (t proxyTool) Description() string     { return t.description }
func (t proxyTool) Schema() json.RawMessage { return t.schema }
func (t proxyTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionParallel
}
func (t proxyTool) Execute(context.Context, string, json.RawMessage, agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	return agentcore.AgentToolResult{}, errors.New("proxy tools are caller-executed")
}

func (s *Service) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req openAIChatCompletionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOpenAICompatRequestBytes))
	if err := decoder.Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON request: "+err.Error())
		return
	}
	llm, err := openAIRequestContext(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if req.N != nil && *req.N != 1 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "only n=1 is supported")
		return
	}
	requestedModel := strings.TrimSpace(req.Model)
	if requestedModel == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	plan, err := s.resolveLLMPlanForPurpose(requestedModel, RoutePurposeChat, 0)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	routed, err := s.buildRoutedProvider(requestedModel, plan, "", "", "", "chat", nil)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	extra := openAIRequestExtra(req)
	stream, err := routed.StreamCompletion(r.Context(), provider.CompletionRequest{
		Model:   requestedModel,
		Context: llm,
		Config:  provider.StreamConfig{Extra: extra},
	})
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	id := "chatcmpl-" + strings.TrimPrefix(newID("completion"), "completion_")
	created := time.Now().Unix()
	if req.Stream {
		s.streamOpenAICompletion(w, r, stream, id, created, requestedModel, req.StreamOptions != nil && req.StreamOptions.IncludeUsage)
		return
	}
	s.writeOpenAICompletion(w, r, stream, id, created, requestedModel)
}

func openAIRequestContext(req openAIChatCompletionRequest) (provider.LlmContext, error) {
	var context provider.LlmContext
	var systems []string
	for i, input := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(input.Role))
		switch role {
		case "system", "developer":
			text, err := openAITextContent(input.Content)
			if err != nil {
				return context, fmt.Errorf("messages[%d]: %w", i, err)
			}
			systems = append(systems, text)
		case "user":
			content, err := openAIUserContent(input.Content)
			if err != nil {
				return context, fmt.Errorf("messages[%d]: %w", i, err)
			}
			context.Messages = append(context.Messages, agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: content})
		case "assistant":
			content, err := openAIAssistantContent(input)
			if err != nil {
				return context, fmt.Errorf("messages[%d]: %w", i, err)
			}
			context.Messages = append(context.Messages, agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: content})
		case "tool":
			if strings.TrimSpace(input.ToolCallID) == "" {
				return context, fmt.Errorf("messages[%d].tool_call_id is required", i)
			}
			text, err := openAITextContent(input.Content)
			if err != nil {
				return context, fmt.Errorf("messages[%d]: %w", i, err)
			}
			context.Messages = append(context.Messages, agentcore.ToolResultMessage{RoleField: agentcore.RoleToolResult, ToolCallID: input.ToolCallID, ToolName: input.Name, Content: agentcore.ContentList{agentcore.NewTextContent(text)}})
		default:
			return context, fmt.Errorf("messages[%d].role %q is not supported", i, input.Role)
		}
	}
	if len(context.Messages) == 0 {
		return context, errors.New("messages must contain at least one user, assistant, or tool message")
	}
	context.SystemPrompt = strings.Join(systems, "\n\n")
	for i, input := range req.Tools {
		if input.Type != "function" || strings.TrimSpace(input.Function.Name) == "" {
			return context, fmt.Errorf("tools[%d] must be a named function", i)
		}
		schema := input.Function.Parameters
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if !json.Valid(schema) {
			return context, fmt.Errorf("tools[%d].function.parameters must be valid JSON", i)
		}
		context.Tools = append(context.Tools, proxyTool{name: input.Function.Name, description: input.Function.Description, schema: schema})
	}
	return context, nil
}

func openAITextContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct{ Type, Text string }
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("content must be a string or content-part array")
	}
	var out strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			out.WriteString(part.Text)
		}
	}
	return out.String(), nil
}

func openAIUserContent(raw json.RawMessage) (agentcore.ContentList, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return agentcore.ContentList{agentcore.NewTextContent(text)}, nil
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errors.New("content must be a string or content-part array")
	}
	content := make(agentcore.ContentList, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text":
			content = append(content, agentcore.NewTextContent(part.Text))
		case "image_url":
			image, err := decodeOpenAIDataImage(part.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			content = append(content, image)
		default:
			return nil, fmt.Errorf("content part type %q is not supported", part.Type)
		}
	}
	return content, nil
}

func decodeOpenAIDataImage(value string) (agentcore.ImageContent, error) {
	const marker = ";base64,"
	if !strings.HasPrefix(value, "data:image/") || !strings.Contains(value, marker) {
		return agentcore.ImageContent{}, errors.New("image_url must be a base64 data URL")
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "data:"), marker, 2)
	if len(parts) != 2 {
		return agentcore.ImageContent{}, errors.New("invalid image data URL")
	}
	if _, err := base64.StdEncoding.DecodeString(parts[1]); err != nil {
		return agentcore.ImageContent{}, errors.New("invalid base64 image data")
	}
	return agentcore.NewImageContent(parts[1], parts[0]), nil
}

func openAIAssistantContent(input openAIInputMessage) (agentcore.ContentList, error) {
	text, err := openAITextContent(input.Content)
	if err != nil {
		return nil, err
	}
	var content agentcore.ContentList
	if text != "" {
		content = append(content, agentcore.NewTextContent(text))
	}
	for i, call := range input.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return nil, fmt.Errorf("tool_calls[%d].type must be function", i)
		}
		args := call.Function.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage(`{}`)
		}
		// OpenAI arguments are normally a JSON-encoded string.
		var encoded string
		if json.Unmarshal(args, &encoded) == nil {
			args = json.RawMessage(encoded)
		}
		content = append(content, agentcore.NewToolCallContent(call.ID, call.Function.Name, args))
	}
	return content, nil
}

func openAIRequestExtra(req openAIChatCompletionRequest) map[string]any {
	extra := map[string]any{}
	if req.Temperature != nil {
		extra["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		extra["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		extra["max_tokens"] = *req.MaxTokens
	}
	if req.MaxCompletionTokens != nil {
		extra["max_completion_tokens"] = *req.MaxCompletionTokens
		if req.MaxTokens == nil {
			extra["max_tokens"] = *req.MaxCompletionTokens
		}
	}
	if len(req.Stop) > 0 {
		var value any
		if json.Unmarshal(req.Stop, &value) == nil {
			extra["stop"] = value
		}
	}
	if req.PresencePenalty != nil {
		extra["presence_penalty"] = *req.PresencePenalty
	}
	if req.FrequencyPenalty != nil {
		extra["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.Seed != nil {
		extra["seed"] = *req.Seed
	}
	if len(req.ResponseFormat) > 0 {
		var value any
		if json.Unmarshal(req.ResponseFormat, &value) == nil {
			extra["response_format"] = value
		}
	}
	if len(req.ToolChoice) > 0 {
		var value any
		if json.Unmarshal(req.ToolChoice, &value) == nil {
			extra["tool_choice"] = value
		}
	}
	if req.ParallelToolCalls != nil {
		extra["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	return extra
}

func (s *Service) writeOpenAICompletion(w http.ResponseWriter, r *http.Request, stream *provider.AssistantMessageEventStream, id string, created int64, requestedModel string) {
	var final agentcore.AssistantMessage
	var streamErr error
	for event := range stream.Events() {
		switch value := event.(type) {
		case provider.StreamDoneEvent:
			final = value.Message
		case provider.StreamErrorEvent:
			final, streamErr = value.Message, providerEventError(value)
		}
	}
	if streamErr == nil && final.RoleField == "" {
		final, streamErr = stream.Result(r.Context())
	}
	if streamErr != nil || final.StopReason == agentcore.StopReasonError {
		message := final.ErrorMessage
		if message == "" && streamErr != nil {
			message = streamErr.Error()
		}
		writeOpenAIError(w, http.StatusBadGateway, "api_error", message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "object": "chat.completion", "created": created,
		"model":   openAIResponseModel(final, requestedModel),
		"choices": []any{map[string]any{"index": 0, "message": openAIResponseMessage(final), "finish_reason": openAIFinishReason(final.StopReason)}},
		"usage":   openAIUsage(final.Usage),
	})
}

func (s *Service) streamOpenAICompletion(w http.ResponseWriter, r *http.Request, stream *provider.AssistantMessageEventStream, id string, created int64, requestedModel string, includeUsage bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "api_error", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	model := requestedModel
	writeOpenAIChunk(w, flusher, id, created, model, map[string]any{"role": "assistant", "content": ""}, nil, nil)
	previousText, previousThinking := "", ""
	previousCalls := []agentcore.ToolCallContent{}
	for event := range stream.Events() {
		var partial agentcore.AssistantMessage
		terminal := false
		var eventErr error
		switch value := event.(type) {
		case provider.StreamTextEvent:
			partial = value.Partial
		case provider.StreamThinkingEvent:
			partial = value.Partial
		case provider.StreamToolCallEvent:
			partial = value.Partial
		case provider.StreamDoneEvent:
			partial, terminal = value.Message, true
		case provider.StreamErrorEvent:
			partial, terminal, eventErr = value.Message, true, providerEventError(value)
		default:
			continue
		}
		model = openAIResponseModel(partial, model)
		text, thinking := responseTextAndThinking(partial)
		delta := map[string]any{}
		if strings.HasPrefix(text, previousText) && len(text) > len(previousText) {
			delta["content"] = text[len(previousText):]
		}
		if strings.HasPrefix(thinking, previousThinking) && len(thinking) > len(previousThinking) {
			delta["reasoning_content"] = thinking[len(previousThinking):]
		}
		calls := partial.ToolCalls()
		if toolDelta := openAIToolCallDeltas(previousCalls, calls, terminal); len(toolDelta) > 0 {
			delta["tool_calls"] = toolDelta
		}
		previousText, previousThinking, previousCalls = text, thinking, calls
		if len(delta) > 0 {
			writeOpenAIChunk(w, flusher, id, created, model, delta, nil, nil)
		}
		if eventErr != nil || partial.StopReason == agentcore.StopReasonError {
			message := partial.ErrorMessage
			if message == "" && eventErr != nil {
				message = eventErr.Error()
			}
			writeOpenAIStreamError(w, flusher, message)
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		if terminal {
			writeOpenAIChunk(w, flusher, id, created, model, map[string]any{}, openAIFinishReason(partial.StopReason), nil)
			if includeUsage {
				writeOpenAIUsageChunk(w, flusher, id, created, model, openAIUsage(partial.Usage))
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
	}
	writeOpenAIStreamError(w, flusher, "upstream stream ended unexpectedly")
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func responseTextAndThinking(message agentcore.AssistantMessage) (string, string) {
	var text, thinking strings.Builder
	for _, content := range message.Content {
		switch value := content.(type) {
		case agentcore.TextContent:
			text.WriteString(value.Text)
		case agentcore.ThinkingContent:
			thinking.WriteString(value.Thinking)
		}
	}
	return text.String(), thinking.String()
}

func openAIResponseMessage(message agentcore.AssistantMessage) map[string]any {
	text, thinking := responseTextAndThinking(message)
	out := map[string]any{"role": "assistant", "content": text}
	if thinking != "" {
		out["reasoning_content"] = thinking
	}
	if calls := openAIToolCalls(message.ToolCalls()); len(calls) > 0 {
		out["tool_calls"] = calls
		if text == "" {
			out["content"] = nil
		}
	}
	return out
}

func openAIToolCalls(calls []agentcore.ToolCallContent) []any {
	out := make([]any, 0, len(calls))
	for _, call := range calls {
		out = append(out, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
	}
	return out
}

func openAIToolCallDeltas(previous, current []agentcore.ToolCallContent, terminal bool) []any {
	var out []any
	for i, call := range current {
		prior := agentcore.ToolCallContent{}
		if i < len(previous) {
			prior = previous[i]
		}
		entry := map[string]any{"index": i}
		if prior.ID == "" {
			entry["id"], entry["type"] = call.ID, "function"
		}
		function := map[string]any{}
		if prior.Name == "" {
			function["name"] = call.Name
		}
		currentArgs, priorArgs := string(call.Arguments), string(prior.Arguments)
		// OpenAIDecoder represents a not-yet-started argument stream as {}.
		// Suppress that placeholder until either real bytes arrive or the terminal
		// event confirms that the function genuinely has an empty object.
		if priorArgs == "{}" {
			priorArgs = ""
		}
		if currentArgs == "{}" && !terminal {
			currentArgs = ""
		}
		if strings.HasPrefix(currentArgs, priorArgs) && len(currentArgs) > len(priorArgs) {
			function["arguments"] = currentArgs[len(priorArgs):]
		}
		if len(function) > 0 {
			entry["function"] = function
		}
		if len(entry) > 1 {
			out = append(out, entry)
		}
	}
	return out
}

func openAIResponseModel(message agentcore.AssistantMessage, fallback string) string {
	if message.ResponseModel != "" {
		return message.ResponseModel
	}
	if message.Model != "" {
		return message.Model
	}
	return fallback
}

func openAIFinishReason(reason string) string {
	switch reason {
	case agentcore.StopReasonLength:
		return "length"
	case agentcore.StopReasonToolUse:
		return "tool_calls"
	default:
		return "stop"
	}
}

func openAIUsage(usage *agentcore.Usage) map[string]int {
	if usage == nil {
		return map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	}
	return map[string]int{"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.InputTokens + usage.OutputTokens}
}

func writeOpenAIChunk(w http.ResponseWriter, flusher http.Flusher, id string, created int64, model string, delta map[string]any, finish any, usage any) {
	choice := map[string]any{"index": 0, "delta": delta, "finish_reason": finish}
	body := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{choice}}
	if usage != nil {
		body["usage"] = usage
	}
	encoded, _ := json.Marshal(body)
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	flusher.Flush()
}

func writeOpenAIUsageChunk(w http.ResponseWriter, flusher http.Flusher, id string, created int64, model string, usage map[string]int) {
	body := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{}, "usage": usage}
	encoded, _ := json.Marshal(body)
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	flusher.Flush()
}

func writeOpenAIError(w http.ResponseWriter, status int, kind, message string) {
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": kind, "param": nil, "code": nil}})
}

func writeOpenAIStreamError(w http.ResponseWriter, flusher http.Flusher, message string) {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message, "type": "api_error", "param": nil, "code": nil}})
	fmt.Fprintf(w, "data: %s\n\n", body)
	flusher.Flush()
}
