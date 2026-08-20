package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/provider"
)

type recordingProvider struct {
	inner        provider.Provider
	store        *GatewayStore
	runID        string
	sessionID    string
	providerID   int
	providerName string
}

type auditTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schema      any    `json:"schema,omitempty"`
}

type auditProviderRequest struct {
	Model         string                  `json:"model"`
	SystemPrompt  string                  `json:"system_prompt,omitempty"`
	Messages      agentcore.MessageList   `json:"messages"`
	Tools         []auditTool             `json:"tools,omitempty"`
	ThinkingLevel agentcore.ThinkingLevel `json:"thinking_level,omitempty"`
	Extra         map[string]any          `json:"extra,omitempty"`
}

func (p *recordingProvider) Name() string { return p.inner.Name() }

func (p *recordingProvider) Models() []provider.Model { return p.inner.Models() }

func (p *recordingProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	tools := make([]auditTool, 0, len(req.Context.Tools))
	for _, tool := range req.Context.Tools {
		tools = append(tools, auditTool{Name: tool.Name(), Description: tool.Description(), Schema: tool.Schema()})
	}
	id := newID("attempt")
	started := time.Now().UTC()
	requestBody := p.store.marshalAuditJSON(auditProviderRequest{
		Model: req.Model, SystemPrompt: req.Context.SystemPrompt, Messages: req.Context.Messages,
		Tools: tools, ThinkingLevel: req.Config.ThinkingLevel, Extra: req.Config.Extra,
	})
	if err := p.store.CreateProviderExchange(ctx, ProviderExchange{
		ID: id, RunID: p.runID, SessionID: p.sessionID, ProviderID: p.providerID,
		ProviderName: p.providerName, Model: req.Model, RequestBody: requestBody, CreatedAt: started,
	}); err != nil {
		return nil, fmt.Errorf("record provider request: %w", err)
	}

	upstream, err := p.inner.StreamCompletion(ctx, req)
	if err != nil {
		p.finish(id, agentcore.AssistantMessage{}, providerExchangeStatus(agentcore.AssistantMessage{}, err), err)
		return nil, err
	}
	stream := provider.NewAssistantMessageEventStream(0)
	go p.proxy(ctx, id, upstream, stream)
	return stream, nil
}

func (p *recordingProvider) proxy(ctx context.Context, id string, upstream, downstream *provider.AssistantMessageEventStream) {
	defer downstream.Close()
	var final agentcore.AssistantMessage
	var streamErr error
	finalSet := false
	downstreamOpen := true
	for ev := range upstream.Events() {
		switch e := ev.(type) {
		case provider.StreamDoneEvent:
			final = e.Message
			finalSet = true
		case provider.StreamErrorEvent:
			final = e.Message
			finalSet = true
			streamErr = e.Err
		}
		if downstreamOpen {
			if err := downstream.Emit(ctx, ev); err != nil {
				streamErr = err
				downstreamOpen = false
			}
		}
	}
	if !finalSet {
		if result, err := upstream.Result(context.Background()); err == nil {
			final = result
			finalSet = true
		} else if streamErr == nil {
			streamErr = err
		}
	}
	status := providerExchangeStatus(final, streamErr)
	p.finish(id, final, status, streamErr)
}

func providerExchangeStatus(msg agentcore.AssistantMessage, runErr error) string {
	if errors.Is(runErr, context.Canceled) || msg.StopReason == agentcore.StopReasonAborted {
		return runStatusCancelled
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		return runStatusTimedOut
	}
	if runErr != nil || msg.StopReason == agentcore.StopReasonError {
		return "error"
	}
	return "done"
}

func (p *recordingProvider) finish(id string, msg agentcore.AssistantMessage, status string, runErr error) {
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	} else if msg.ErrorMessage != "" {
		errorText = msg.ErrorMessage
	}
	promptTokens, completionTokens := 0, 0
	if msg.Usage != nil {
		promptTokens = msg.Usage.InputTokens
		completionTokens = msg.Usage.OutputTokens
	}
	statusCode := 200
	if status != "done" {
		statusCode = 0
	}
	if err := p.store.FinishProviderExchange(context.Background(), id, p.store.marshalAuditJSON(msg), status,
		errorText, statusCode, promptTokens, completionTokens, 0, time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: finish provider audit %s: %v\n", id, err)
	}
}
