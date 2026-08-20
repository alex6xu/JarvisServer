package gateway

import (
	"slices"
	"testing"

	"github.com/alex6xu/jarvisserver/internal/cli/run"
)

func TestGatewayToolPolicyForChat(t *testing.T) {
	policy := gatewayToolPolicy("chat", false)
	if !slices.Equal(policy.Allow, []string{"websearch", "webfetch"}) {
		t.Fatalf("chat allow-list = %v", policy.Allow)
	}

	tools := run.ApplyToolPolicy(run.BuiltinTools(t.TempDir(), false), policy)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	if !slices.Equal(names, []string{"webfetch", "websearch"}) {
		t.Fatalf("chat tools = %v, want only web retrieval tools", names)
	}
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
