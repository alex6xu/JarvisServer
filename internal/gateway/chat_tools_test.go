package gateway

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/cli/run"
	"github.com/alex6xu/jarvisserver/internal/runtime"
)

func TestGatewayToolPolicyForChat(t *testing.T) {
	policy := gatewayToolPolicy("chat", false)
	if !slices.Equal(policy.Allow, []string{"websearch", "webfetch", "memory_search", "stock_latest_digest", "skill_load"}) {
		t.Fatalf("chat allow-list = %v", policy.Allow)
	}

	tools := run.ApplyToolPolicy(run.BuiltinTools(t.TempDir(), false), policy)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	if !slices.Equal(names, []string{"webfetch", "websearch"}) {
		t.Fatalf("chat built-in tools = %v, want only safe retrieval tools", names)
	}
}

func TestGatewaySkillCannotEnableBuiltinWorkspaceTools(t *testing.T) {
	skill, err := runtime.ParseSkill("unsafe.md", []byte("---\nname: unsafe\ndescription: test\nallowed-tools:\n  - bash\n  - plugin_weather\n---\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := SkillSnapshot{Skills: []*runtime.Skill{skill}}
	tools := append(run.BuiltinTools(t.TempDir(), false), &policyTool{name: "plugin_weather"})
	filtered := applyGatewayToolPolicy("chat", false, tools, snapshot)
	var names []string
	for _, tool := range filtered {
		names = append(names, tool.Name())
	}
	if slices.Contains(names, "bash") || !slices.Contains(names, "plugin_weather") {
		t.Fatalf("filtered tools=%v", names)
	}
}

type policyTool struct{ name string }

func (t *policyTool) Name() string            { return t.name }
func (t *policyTool) Description() string     { return "test" }
func (t *policyTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *policyTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionParallel
}
func (t *policyTool) Execute(context.Context, string, json.RawMessage, agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	return agentcore.AgentToolResult{}, nil
}

func TestGatewayToolPolicyForCoder(t *testing.T) {
	if policy := gatewayToolPolicy("coder", false); !policy.IsZero() {
		t.Fatalf("coder policy = %+v, want unrestricted workspace tools", policy)
	}
}

func TestGatewayToolPolicyWhenToolsDisabled(t *testing.T) {
	if policy := gatewayToolPolicy("chat", true); !policy.IsZero() {
		t.Fatalf("disabled policy = %+v, want zero policy", policy)
	}
}
