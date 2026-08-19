package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/provider"
	corerouter "github.com/alex6xu/jarvisserver/internal/router"
)

type routedCandidate struct {
	route    LLMRoute
	provider provider.Provider
}

type turnPlanner func(context.Context, RoutePurpose, provider.CompletionRequest) (RoutePlan, []routedCandidate, error)

// failoverProvider replans before every completion call (one LLM turn), then
// tries candidates until meaningful output commits the attempt.
type failoverProvider struct {
	candidates  []routedCandidate // fixed candidates are retained for focused tests.
	planner     turnPlanner
	router      *ProviderRouter
	store       *GatewayStore
	runID       string
	sessionID   string
	workspaceID string
	mode        string
	publish     func(StreamEvent)
	turn        atomic.Int64
}

func (p *failoverProvider) Name() string { return "router" }

func (p *failoverProvider) Models() []provider.Model {
	var out []provider.Model
	for _, candidate := range p.candidates {
		out = append(out, candidate.provider.Models()...)
	}
	return out
}

func (p *failoverProvider) StreamCompletion(ctx context.Context, req provider.CompletionRequest) (*provider.AssistantMessageEventStream, error) {
	nextTurn := int(p.turn.Load()) + 1
	purpose := routePurposeForCompletion(p.mode, nextTurn, req)
	turn := int(p.turn.Load())
	if purpose != RoutePurposeCompaction {
		turn = int(p.turn.Add(1))
	} else if turn == 0 {
		turn = 1
	}
	plan := RoutePlan{}
	candidates := p.candidates
	if p.planner != nil {
		var err error
		plan, candidates, err = p.planner(ctx, purpose, req)
		if err != nil {
			return nil, err
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("provider router: no candidates")
	}
	if purpose != RoutePurposeCompaction && p.store != nil && p.runID != "" {
		checkpoint := RunCheckpoint{RunID: p.runID, Turn: turn, SessionID: p.sessionID,
			WorkspaceID: p.workspaceID, Mode: p.mode, Model: req.Model,
			SystemPrompt: req.Context.SystemPrompt, Messages: req.Context.Messages, CreatedAt: time.Now().UTC()}
		if err := p.store.SaveRunCheckpoint(context.Background(), checkpoint); err != nil {
			return nil, fmt.Errorf("save run checkpoint: %w", err)
		}
	}
	out := provider.NewAssistantMessageEventStream(0)
	go p.run(ctx, req, out, turn, purpose, plan, candidates)
	return out, nil
}

func routePurposeForCompletion(mode string, turn int, req provider.CompletionRequest) RoutePurpose {
	if value, ok := req.Config.Extra["route_purpose"].(string); ok {
		if purpose := normalizeRoutePurpose(RoutePurpose(value)); purpose != RoutePurposeDefault {
			return purpose
		}
	}
	if strings.EqualFold(mode, "coder") {
		if turn <= 2 {
			return RoutePurposeCodeAnalysis
		}
		return RoutePurposeCodeExecution
	}
	return RoutePurposeChat
}

func (p *failoverProvider) run(ctx context.Context, req provider.CompletionRequest, out *provider.AssistantMessageEventStream, turn int, purpose RoutePurpose, plan RoutePlan, candidates []routedCandidate) {
	defer out.Close()
	var lastErr error
	for ordinal, candidate := range candidates {
		attempt := RunAttempt{ID: newID("attempt"), RunID: p.runID,
			EndpointID: candidate.route.EndpointID, ProviderID: candidate.route.ProviderID,
			Model: candidate.route.Model, Ordinal: ordinal + 1, Turn: turn, Status: "running",
			Purpose: string(purpose), RouteReason: plan.Reason, PolicyRevision: plan.PolicyRev,
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if attempt.EndpointID == "" {
			attempt.EndpointID = fmt.Sprintf("provider_%d", candidate.route.ProviderID)
		}
		if p.store != nil && p.runID != "" {
			if err := p.store.CreateRunAttempt(context.Background(), attempt); err != nil {
				lastErr = err
				continue
			}
		}
		if p.publish != nil {
			p.publish(StreamEvent{Type: "route.selected", Model: candidate.route.Model,
				AttemptID: attempt.ID, Purpose: string(purpose), Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
		}
		started := time.Now()
		attemptReq := req
		attemptReq.Model = candidate.route.Model
		attemptReq.Config.APIKey = candidate.route.APIKey
		stream, err := candidate.provider.StreamCompletion(ctx, attemptReq)
		if err != nil {
			lastErr = err
			p.finishAttempt(&attempt, started, time.Time{}, "failed", "before_stream", err)
			continue
		}

		buffered := make([]provider.AssistantMessageEvent, 0, 2)
		committed := false
		attemptFailed := false
		var firstToken time.Time
	eventLoop:
		for ev := range stream.Events() {
			if !committed {
				switch e := ev.(type) {
				case provider.StreamStartEvent:
					buffered = append(buffered, ev)
					continue
				case provider.StreamThinkingEvent:
					if len(buffered) < 2 {
						buffered = append(buffered, ev)
					} else {
						buffered[len(buffered)-1] = ev
					}
					continue
				case provider.StreamErrorEvent:
					lastErr = providerEventError(e)
					attemptFailed = true
					p.finishAttempt(&attempt, started, firstToken, "failed", "before_output", lastErr)
					break eventLoop
				default:
					committed = true
					firstToken = time.Now()
					for _, pending := range buffered {
						if err := out.Emit(ctx, pending); err != nil {
							p.finishAttempt(&attempt, started, firstToken, attemptStatusForError(err), "during_stream", err)
							return
						}
					}
				}
			}
			switch ev.(type) {
			case provider.StreamTextEvent:
				attempt.ProducedOutput = true
			case provider.StreamToolCallEvent:
				attempt.ProducedToolCall = true
			}
			if err := out.Emit(ctx, ev); err != nil {
				p.finishAttempt(&attempt, started, firstToken, attemptStatusForError(err), "during_stream", err)
				return
			}
			switch e := ev.(type) {
			case provider.StreamDoneEvent:
				if e.Message.Usage != nil {
					attempt.InputTokens = e.Message.Usage.InputTokens
					attempt.OutputTokens = e.Message.Usage.OutputTokens
				}
				p.finishAttempt(&attempt, started, firstToken, "done", "", nil)
				return
			case provider.StreamErrorEvent:
				lastErr = providerEventError(e)
				stage := "after_output"
				if attempt.ProducedToolCall {
					stage = "after_tool_call"
				}
				p.finishAttempt(&attempt, started, firstToken, "failed", stage, lastErr)
				return
			}
		}
		if committed {
			result, resultErr := stream.Result(context.Background())
			if result.Usage != nil {
				attempt.InputTokens = result.Usage.InputTokens
				attempt.OutputTokens = result.Usage.OutputTokens
			}
			if resultErr != nil {
				lastErr = resultErr
				_ = out.Emit(context.Background(), provider.StreamErrorEvent{Message: errorAssistant(candidate.route, resultErr), Err: resultErr})
			} else {
				_ = out.Emit(context.Background(), provider.StreamDoneEvent{Message: result})
			}
			stage := ""
			if resultErr != nil {
				stage = "after_output"
			}
			p.finishAttempt(&attempt, started, firstToken, statusForError(resultErr), stage, resultErr)
			return
		}
		if attemptFailed {
			continue
		}
		lastErr = errors.New("provider stream ended before producing output")
		p.finishAttempt(&attempt, started, firstToken, "failed", "before_output", lastErr)
	}
	if lastErr == nil {
		lastErr = errors.New("all provider candidates failed")
	}
	last := candidates[len(candidates)-1].route
	_ = out.Emit(context.Background(), provider.StreamErrorEvent{Message: errorAssistant(last, lastErr), Err: lastErr})
}

func (p *failoverProvider) finishAttempt(attempt *RunAttempt, started, firstToken time.Time, status, stage string, runErr error) {
	finished := time.Now().UTC()
	attempt.Status = status
	attempt.FailureStage = stage
	attempt.LatencyMs = finished.Sub(started).Milliseconds()
	if !firstToken.IsZero() {
		attempt.FirstTokenMs = firstToken.Sub(started).Milliseconds()
	}
	attempt.FinishedAt = finished.Format(time.RFC3339Nano)
	if runErr != nil {
		attempt.Error = runErr.Error()
		attempt.ErrorCategory = classifyProviderError(runErr)
	}
	if p.store != nil && p.runID != "" {
		_ = p.store.FinishRunAttempt(context.Background(), *attempt)
	}
	if p.router != nil && !contextTerminated(runErr) {
		_ = p.router.ObserveResult(corerouter.AttemptResult{EndpointID: attempt.EndpointID, RunID: attempt.RunID,
			AttemptID: attempt.ID, Success: runErr == nil, ErrorCategory: attempt.ErrorCategory,
			ErrorText: attempt.Error, Latency: time.Duration(attempt.LatencyMs) * time.Millisecond,
			FirstToken: time.Duration(attempt.FirstTokenMs) * time.Millisecond, OccurredAt: finished})
	}
}

func statusForError(err error) string {
	if err != nil {
		return "failed"
	}
	return "done"
}

func attemptStatusForError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return runStatusTimedOut
	}
	if errors.Is(err, context.Canceled) {
		return runStatusCancelled
	}
	return "failed"
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
	return agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Provider: route.ProviderLabel,
		Model: route.Model, StopReason: agentcore.StopReasonError,
		ErrorMessage: fmt.Sprintf("all provider routes failed: %v", err)}
}
