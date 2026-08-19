package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/runtime"
)

// PublishFunc publishes one sequenced stream event (seq assigned by RunManager).
type PublishFunc func(ev StreamEvent)

// toolStepCollector accumulates tool start/end into web ToolStep values.
type toolStepCollector struct {
	mu    sync.Mutex
	order []string
	byID  map[string]*ToolStep
}

func newToolStepCollector() *toolStepCollector {
	return &toolStepCollector{byID: make(map[string]*ToolStep)}
}

func (c *toolStepCollector) start(id, name string, args any) *ToolStep {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byID[id]; !ok {
		c.order = append(c.order, id)
	}
	c.byID[id] = &ToolStep{
		Tool:   name,
		Args:   stringifyArgs(args),
		ID:     id,
		Status: "running",
	}
	cp := *c.byID[id]
	return &cp
}

func (c *toolStepCollector) end(id, name string, result agentcore.AgentToolResult, isError bool) *ToolStep {
	c.mu.Lock()
	defer c.mu.Unlock()
	step, ok := c.byID[id]
	if !ok {
		step = &ToolStep{Tool: name, ID: id}
		c.byID[id] = step
		c.order = append(c.order, id)
	}
	text := agentcore.ContentToText(result.Content)
	if isError && text == "" {
		text = "error"
	}
	step.Result = text
	if isError {
		step.Status = "error"
	} else {
		step.Status = "done"
	}
	cp := *step
	return &cp
}

func (c *toolStepCollector) all() []ToolStep {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ToolStep, 0, len(c.order))
	for _, id := range c.order {
		if s := c.byID[id]; s != nil {
			out = append(out, *s)
		}
	}
	return out
}

func stringifyArgs(args any) string {
	switch v := args.(type) {
	case nil:
		return ""
	case string:
		return prettyJSONBytes([]byte(v))
	case []byte:
		return prettyJSONBytes(v)
	case json.RawMessage:
		return prettyJSONBytes(v)
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

func prettyJSONBytes(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// NewStreamHandler builds a runtime.StreamHandler that publishes web SSE events.
// Call Finish after DrainStream to emit the terminal done/error event.
type translateState struct {
	pub       PublishFunc
	model     string
	sessionID string
	steps     *toolStepCollector
	full      strings.Builder
	mu        sync.Mutex
	finished  bool
}

// NewTranslateHandler returns the DrainStream handler and a Finish func that
// must be called once after the drain completes (with the drain error).
func NewTranslateHandler(pub PublishFunc, model, sessionID string, extraOnEvent func(agentcore.AgentEvent)) (runtime.StreamHandler, func(error)) {
	st := &translateState{
		pub:       pub,
		model:     model,
		sessionID: sessionID,
		steps:     newToolStepCollector(),
	}
	h := runtime.StreamHandler{
		OnText: func(delta string) {
			if delta == "" {
				return
			}
			st.full.WriteString(delta)
			st.pub(StreamEvent{
				Type:      "delta",
				Content:   delta,
				SessionID: st.sessionID,
				Model:     st.model,
			})
		},
		OnEvent: func(ev agentcore.AgentEvent) {
			if extraOnEvent != nil {
				extraOnEvent(ev)
			}
			switch e := ev.(type) {
			case agentcore.ToolExecutionStartEvent:
				step := st.steps.start(e.ToolCallID, e.ToolName, e.Args)
				st.pub(StreamEvent{
					Type:      "tool_step",
					SessionID: st.sessionID,
					Model:     st.model,
					Step:      step,
				})
			case agentcore.ToolExecutionEndEvent:
				step := st.steps.end(e.ToolCallID, e.ToolName, e.Result, e.IsError)
				st.pub(StreamEvent{
					Type:      "tool_step",
					SessionID: st.sessionID,
					Model:     st.model,
					Step:      step,
				})
			case agentcore.TurnEndEvent:
				if e.Message.StopReason == "error" || e.Message.StopReason == "aborted" {
					reason := e.Message.StopReason
					if text := agentcore.ContentToText(e.Message.Content); text != "" {
						reason = text
					}
					st.pub(StreamEvent{
						Type:      "error",
						Content:   reason,
						SessionID: st.sessionID,
						Model:     st.model,
					})
				}
			}
		},
	}
	finish := func(err error) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.finished {
			return
		}
		st.finished = true
		if err != nil && !contextTerminated(err) {
			st.pub(StreamEvent{
				Type:      "error",
				Content:   err.Error(),
				SessionID: st.sessionID,
				Model:     st.model,
			})
		}
		steps := st.steps.all()
		st.pub(StreamEvent{
			Type:      "done",
			Content:   st.full.String(),
			SessionID: st.sessionID,
			Model:     st.model,
			ToolSteps: steps,
		})
	}
	return h, finish
}

func contextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

func contextTerminated(err error) bool {
	return contextCanceled(err) || errors.Is(err, context.DeadlineExceeded)
}
