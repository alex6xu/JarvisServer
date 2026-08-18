package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/provider"
)

type routedCandidate struct {
	route    LLMRoute
	provider provider.Provider
}

// failoverProvider tries candidates in plan order until one produces meaningful
// model output. Once output begins, the attempt is committed and never replayed.
type failoverProvider struct {
	candidates []routedCandidate
	router     *ProviderRouter
}

func (p *failoverProvider) Name() string {
	if len(p.candidates) == 0 {
		return "router"
	}
	return p.candidates[0].provider.Name()
}

func (p *failoverProvider) Models() []provider.Model {
	var out []provider.Model
	for _, candidate := range p.candidates {
		out = append(out, candidate.provider.Models()...)
	}
	return out
}

func (p *failoverProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	if len(p.candidates) == 0 {
		return nil, errors.New("provider router: no candidates")
	}
	out := provider.NewAssistantMessageEventStream(0)
	go p.run(ctx, req, out)
	return out, nil
}

func (p *failoverProvider) run(ctx context.Context, req provider.CompletionRequest, out *provider.AssistantMessageEventStream) {
	defer out.Close()
	var lastErr error
	for _, candidate := range p.candidates {
		attemptReq := req
		attemptReq.Model = candidate.route.Model
		attemptReq.Config.APIKey = candidate.route.APIKey
		stream, err := candidate.provider.StreamCompletion(ctx, attemptReq)
		if err != nil {
			lastErr = err
			p.router.Observe(candidate.route.ProviderID, err)
			continue
		}

		buffered := make([]provider.AssistantMessageEvent, 0, 2)
		committed := false
		attemptFailed := false
		for ev := range stream.Events() {
			if !committed {
				switch e := ev.(type) {
				case provider.StreamStartEvent:
					buffered = append(buffered, ev)
					continue
				case provider.StreamErrorEvent:
					lastErr = providerEventError(e)
					p.router.Observe(candidate.route.ProviderID, lastErr)
					attemptFailed = true
					continue
				default:
					committed = true
					for _, pending := range buffered {
						if err := out.Emit(ctx, pending); err != nil {
							return
						}
					}
				}
			}
			if err := out.Emit(ctx, ev); err != nil {
				return
			}
			switch e := ev.(type) {
			case provider.StreamDoneEvent:
				p.router.Observe(candidate.route.ProviderID, nil)
				return
			case provider.StreamErrorEvent:
				p.router.Observe(candidate.route.ProviderID, providerEventError(e))
				return
			}
		}
		if committed {
			result, resultErr := stream.Result(context.Background())
			if resultErr != nil {
				lastErr = resultErr
				_ = out.Emit(context.Background(), provider.StreamErrorEvent{Message: errorAssistant(candidate.route, resultErr), Err: resultErr})
			} else {
				_ = out.Emit(context.Background(), provider.StreamDoneEvent{Message: result})
			}
			p.router.Observe(candidate.route.ProviderID, resultErr)
			return
		}
		if attemptFailed {
			continue
		}
		lastErr = errors.New("provider stream ended before producing output")
		p.router.Observe(candidate.route.ProviderID, lastErr)
	}
	if lastErr == nil {
		lastErr = errors.New("all provider candidates failed")
	}
	last := p.candidates[len(p.candidates)-1].route
	_ = out.Emit(context.Background(), provider.StreamErrorEvent{Message: errorAssistant(last, lastErr), Err: lastErr})
}

func providerEventError(ev provider.StreamErrorEvent) error {
	if ev.Err != nil {
		return ev.Err
	}
	if ev.Message.ErrorMessage != "" {
		return errors.New(ev.Message.ErrorMessage)
	}
	return errors.New("provider stream error")
}

func errorAssistant(route LLMRoute, err error) agentcore.AssistantMessage {
	return agentcore.AssistantMessage{
		RoleField: agentcore.RoleAssistant, Provider: route.ProviderLabel, Model: route.Model,
		StopReason: agentcore.StopReasonError, ErrorMessage: fmt.Sprintf("all provider routes failed: %v", err),
	}
}
