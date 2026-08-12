package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/smallnest/pigo/internal/agentcore"
)

func TestTranslateHandlerDeltaAndDone(t *testing.T) {
	var got []StreamEvent
	pub := func(ev StreamEvent) { got = append(got, ev) }
	h, finish := NewTranslateHandler(pub, "m1", "s1", nil)

	h.OnText("hello")
	h.OnText(" world")
	h.OnEvent(agentcore.ToolExecutionStartEvent{
		ToolCallID: "t1",
		ToolName:   "bash",
		Args:       json.RawMessage(`{"command":"ls"}`),
	})
	h.OnEvent(agentcore.ToolExecutionEndEvent{
		ToolCallID: "t1",
		ToolName:   "bash",
		Result:     agentcore.AgentToolResult{Content: agentcore.ContentList{agentcore.NewTextContent("ok")}},
	})
	finish(nil)

	if len(got) < 4 {
		t.Fatalf("got %d events, want >= 4: %#v", len(got), got)
	}
	if got[0].Type != "delta" || got[0].Content != "hello" {
		t.Fatalf("first delta: %#v", got[0])
	}
	if got[1].Type != "delta" || got[1].Content != " world" {
		t.Fatalf("second delta: %#v", got[1])
	}
	var sawStart, sawTool, sawDone bool
	for _, ev := range got {
		if ev.Type == "tool_step" && ev.Step != nil && ev.Step.Tool == "bash" {
			if ev.Step.Status == "running" && strings.Contains(ev.Step.Args, "ls") {
				sawStart = true
			}
			if ev.Step.Result == "ok" && strings.Contains(ev.Step.Args, "ls") {
				sawTool = true
			}
		}
		if ev.Type == "done" && ev.Content == "hello world" && len(ev.ToolSteps) == 1 {
			if strings.Contains(ev.ToolSteps[0].Args, "ls") {
				sawDone = true
			}
		}
	}
	if !sawStart {
		t.Fatalf("missing running tool_step with args: %#v", got)
	}
	if !sawTool {
		t.Fatalf("missing tool_step: %#v", got)
	}
	if !sawDone {
		t.Fatalf("missing done: %#v", got)
	}
}

func TestRunManagerAfterSeq(t *testing.T) {
	m := NewRunManager()
	st := m.Register("sess", "m", "", func() {})
	st.Publish(StreamEvent{Type: "delta", Content: "a"})
	st.Publish(StreamEvent{Type: "delta", Content: "b"})
	st.Finish(nil)

	ch := st.Subscribe(1)
	var seqs []int64
	for ev := range ch {
		seqs = append(seqs, ev.Seq)
	}
	if len(seqs) != 1 || seqs[0] != 2 {
		t.Fatalf("after_seq=1 got %v, want [2]", seqs)
	}
}
