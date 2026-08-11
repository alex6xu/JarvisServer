// Tests for background bash execution: launching a detached job, draining its
// output incrementally via bash_output, and terminating a long-running job with
// kill_bash. These exercise the shared BashJobStore wiring end to end.
package agenttool

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
)

func runBGTool(t *testing.T, tool agentcore.AgentTool, args map[string]any) (agentcore.AgentToolResult, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tool.Execute(context.Background(), "call-1", raw, nil)
}

// A background command returns immediately with a bash_id, then bash_output
// drains its output and reports it exited.
func TestBashBackgroundRunAndOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on windows")
	}
	jobs := NewBashJobStore()
	bash := &BashTool{Jobs: jobs}
	out := &BashOutputTool{Jobs: jobs}

	res, gerr := runBGTool(t, bash, map[string]any{"command": "echo bg-hello", "run_in_background": true})
	if gerr != nil {
		t.Fatalf("unexpected error: %v", gerr)
	}
	details, ok := res.Details.(map[string]any)
	if !ok || details["background"] != true {
		t.Fatalf("expected background details, got %+v", res.Details)
	}
	id, _ := details["bash_id"].(string)
	if id == "" {
		t.Fatalf("no bash_id returned")
	}

	// Poll bash_output until the job exits and produced its line.
	var text string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := runBGTool(t, out, map[string]any{"bash_id": id})
		if err != nil {
			t.Fatalf("bash_output: %v", err)
		}
		text += resultText(r)
		d, _ := r.Details.(map[string]any)
		if d["status"] == string(BashExited) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(text, "bg-hello") {
		t.Errorf("output = %q, want to contain bg-hello", text)
	}
	if !strings.Contains(text, "exited") {
		t.Errorf("status never reported exited: %q", text)
	}
}

// kill_bash terminates a long-running background job and it stops running.
func TestBashBackgroundKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on windows")
	}
	jobs := NewBashJobStore()
	bash := &BashTool{Jobs: jobs}
	kill := &BashKillTool{Jobs: jobs}
	out := &BashOutputTool{Jobs: jobs}

	res, gerr := runBGTool(t, bash, map[string]any{"command": "sleep 30", "run_in_background": true})
	if gerr != nil {
		t.Fatalf("unexpected error: %v", gerr)
	}
	id := res.Details.(map[string]any)["bash_id"].(string)

	kr, err := runBGTool(t, kill, map[string]any{"bash_id": id})
	if err != nil {
		t.Fatalf("kill_bash: %v", err)
	}
	if killed, _ := kr.Details.(map[string]any)["killed"].(bool); !killed {
		t.Errorf("expected killed=true, got %+v", kr.Details)
	}

	// After the kill, the job should report exited within a short window.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := runBGTool(t, out, map[string]any{"bash_id": id})
		if r.Details.(map[string]any)["status"] == string(BashExited) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("job still running after kill")
}

// bash_output and kill_bash report a clear error for an unknown id.
func TestBashControlUnknownID(t *testing.T) {
	jobs := NewBashJobStore()
	out := &BashOutputTool{Jobs: jobs}
	kill := &BashKillTool{Jobs: jobs}

	r, _ := runBGTool(t, out, map[string]any{"bash_id": "bash_99"})
	if !strings.Contains(resultText(r), "no background command") {
		t.Errorf("bash_output on unknown id should error, got %q", resultText(r))
	}
	r, _ = runBGTool(t, kill, map[string]any{"bash_id": "bash_99"})
	if !strings.Contains(resultText(r), "no background command") {
		t.Errorf("kill_bash on unknown id should error, got %q", resultText(r))
	}
}

// run_in_background without a store wired reports it is unavailable.
func TestBashBackgroundNoStore(t *testing.T) {
	bash := &BashTool{}
	r, err := runBGTool(t, bash, map[string]any{"command": "echo x", "run_in_background": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resultText(r), "not available") {
		t.Errorf("expected unavailable message when no store is wired, got %q", resultText(r))
	}
}
