package gateway

import (
	"context"
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
	st, err := m.Register("sess", "m", "", func() {})
	if err != nil {
		t.Fatal(err)
	}
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

func TestRunManagerPersistsAndReloadsEvents(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	m := NewRunManager(store)
	st, err := m.Register("sess", "m", "workspace", func() {})
	if err != nil {
		t.Fatal(err)
	}
	st.Publish(StreamEvent{Type: "delta", Content: "a"})
	st.Publish(StreamEvent{Type: "done", Content: "ab"})
	st.Finish(nil)

	reloaded, ok := NewRunManager(store).Get(st.ID)
	if !ok {
		t.Fatal("persisted run not found")
	}
	if reloaded.Status != runStatusDone || reloaded.LastSeq != 2 {
		t.Fatalf("reloaded run = status %q seq %d", reloaded.Status, reloaded.LastSeq)
	}
	var events []StoredEvent
	for event := range reloaded.Subscribe(1) {
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Payload.Content != "ab" {
		t.Fatalf("replayed events: %#v", events)
	}
}

func TestLoadRunMarksInterruptedRunAsError(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	st := &RunState{ID: "run_interrupted", SessionID: "sess", Status: runStatusRunning}
	if err := store.CreateRun(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRun(context.Background(), st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runStatusError || loaded.Err == nil {
		t.Fatalf("loaded interrupted run = status %q err %v", loaded.Status, loaded.Err)
	}
}
