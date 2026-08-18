package prompts

// Tests for project-level .jarvis/prompts (US-006, #337): loaded at the project
// tier only when the project is trusted, overrides global same-name templates,
// and is suppressed by --no-prompt-templates.

import (
	"path/filepath"
	"testing"

	"github.com/alex6xu/jarvisserver/internal/cli"
	"github.com/alex6xu/jarvisserver/internal/cli/testutil"
	"github.com/alex6xu/jarvisserver/internal/runtime"
)

// TestBuildSlashRegistryLoadsProjectPromptsTrusted: with the project trusted,
// .jarvis/prompts/*.md loads at the project tier.
func TestBuildSlashRegistryLoadsProjectPromptsTrusted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JARVIS_HOME", home) // empty global
	cwdTmp := t.TempDir()
	testutil.WritePrompt(t, cwdTmp, filepath.Join(".jarvis", "prompts"), "review.md", "Review: $ARGUMENTS")

	reg, err := BuildSlashRegistry(&cli.LiveConfig{Model: "test", ProviderName: "test"}, nil, nil,
		PromptTemplateSources{
			ProjectDir:     filepath.Join(cwdTmp, ".jarvis", "prompts"),
			ProjectTrusted: true,
		})
	if err != nil {
		t.Fatalf("BuildSlashRegistry: %v", err)
	}
	out, err := reg.ResolveOutcome("/review diff")
	if err != nil {
		t.Fatalf("ResolveOutcome: %v", err)
	}
	if !out.Handled || out.Prompt != "Review: diff" {
		t.Errorf("/review = handled=%v prompt=%q, want \"Review: diff\"", out.Handled, out.Prompt)
	}
}

// TestBuildSlashRegistryProjectPromptsUntrustedSkipped: when the project is not
// trusted, .jarvis/prompts is not loaded.
func TestBuildSlashRegistryProjectPromptsUntrustedSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JARVIS_HOME", home)
	cwdTmp := t.TempDir()
	testutil.WritePrompt(t, cwdTmp, filepath.Join(".jarvis", "prompts"), "review.md", "Review: $ARGUMENTS")

	reg, err := BuildSlashRegistry(&cli.LiveConfig{Model: "test", ProviderName: "test"}, nil, nil,
		PromptTemplateSources{
			ProjectDir:     filepath.Join(cwdTmp, ".jarvis", "prompts"),
			ProjectTrusted: false,
		})
	if err != nil {
		t.Fatalf("BuildSlashRegistry: %v", err)
	}
	if _, ok := reg.Lookup("review"); ok {
		t.Error("/review should NOT load from an untrusted project")
	}
}

// TestBuildSlashRegistryProjectMissingDirNoError: a missing .jarvis/prompts is
// not an error (most projects don't have one).
func TestBuildSlashRegistryProjectMissingDirNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JARVIS_HOME", home)
	cwdTmp := t.TempDir() // no .jarvis/prompts created

	reg, err := BuildSlashRegistry(&cli.LiveConfig{Model: "test", ProviderName: "test"}, nil, nil,
		PromptTemplateSources{
			ProjectDir:     filepath.Join(cwdTmp, ".jarvis", "prompts"),
			ProjectTrusted: true,
		})
	if err != nil {
		t.Fatalf("missing .jarvis/prompts should not error, got %v", err)
	}
	if reg == nil {
		t.Fatal("registry is nil")
	}
}

// TestBuildSlashRegistryProjectOverridesGlobal: a project template overrides a
// same-named global one (project tier wins, global shadowed).
func TestBuildSlashRegistryProjectOverridesGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JARVIS_HOME", home)
	// global
	testutil.WritePrompt(t, home, "prompts", "dup.md", "FROM GLOBAL")
	// project
	cwdTmp := t.TempDir()
	testutil.WritePrompt(t, cwdTmp, filepath.Join(".jarvis", "prompts"), "dup.md", "FROM PROJECT")

	reg, err := BuildSlashRegistry(&cli.LiveConfig{Model: "test", ProviderName: "test"}, nil, nil,
		PromptTemplateSources{
			ProjectDir:     filepath.Join(cwdTmp, ".jarvis", "prompts"),
			ProjectTrusted: true,
		})
	if err != nil {
		t.Fatalf("BuildSlashRegistry: %v", err)
	}
	cmd, ok := reg.Lookup("dup")
	if !ok {
		t.Fatal("/dup not found")
	}
	if got := cmd.Expand(""); got != "FROM PROJECT" {
		t.Errorf("project should override global, got %q", got)
	}
	found := false
	for _, e := range reg.Shadowed() {
		if e.Name == "dup" && e.Tier == runtime.TierGlobal {
			found = true
		}
	}
	if !found {
		t.Errorf("global dup should be shadowed with TierGlobal, got %v", reg.Shadowed())
	}
}

// TestBuildSlashRegistryNoPromptTemplatesDisablesProject: --no-prompt-templates
// suppresses project prompts too.
func TestBuildSlashRegistryNoPromptTemplatesDisablesProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("JARVIS_HOME", home)
	cwdTmp := t.TempDir()
	testutil.WritePrompt(t, cwdTmp, filepath.Join(".jarvis", "prompts"), "review.md", "Review: $ARGUMENTS")

	reg, err := BuildSlashRegistry(&cli.LiveConfig{Model: "test", ProviderName: "test"}, nil, nil,
		PromptTemplateSources{
			Disable:        true,
			ProjectDir:     filepath.Join(cwdTmp, ".jarvis", "prompts"),
			ProjectTrusted: true,
		})
	if err != nil {
		t.Fatalf("BuildSlashRegistry: %v", err)
	}
	if _, ok := reg.Lookup("review"); ok {
		t.Error("/review should NOT load under --no-prompt-templates")
	}
}
