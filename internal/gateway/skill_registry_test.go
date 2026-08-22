package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/runtime"
)

func TestSkillRegistryCreateSnapshotAndRevision(t *testing.T) {
	store, err := OpenGatewayStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	account, err := store.CreateAccount(context.Background(), "skills-user", "", "admin", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	registry := NewSkillRegistryService(store, dir, []string{"stock_latest_digest", "skill_load"})
	content := []byte("---\nname: custom-digest\ndescription: Test digest\nallowed-tools:\n  - stock_latest_digest\n---\n\nUse the digest tool.")
	summary, err := registry.Create(context.Background(), account.ID, content)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Revision != 1 || summary.Source != "custom" {
		t.Fatalf("summary=%+v", summary)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom-digest", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot(context.Background(), account.ID)
	if err != nil || len(snapshot.Skills) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	updated := []byte("---\nname: custom-digest\ndescription: Updated digest\nallowed-tools: stock_latest_digest\n---\n\nUpdated body.")
	if _, err := registry.Update(context.Background(), account.ID, "custom-digest", summary.Revision+1, updated); !errors.Is(err, errSkillConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	summary, err = registry.Update(context.Background(), account.ID, "custom-digest", summary.Revision, updated)
	if err != nil || summary.Revision != 2 {
		t.Fatalf("updated=%+v err=%v", summary, err)
	}
	if err := registry.SetAccountEnabled(context.Background(), account.ID, "custom-digest", false); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = registry.Snapshot(context.Background(), account.ID)
	if len(snapshot.Skills) != 0 {
		t.Fatalf("disabled snapshot=%+v", snapshot)
	}
}

func TestSkillRegistryRejectsInvalidAndSymlinkFiles(t *testing.T) {
	store, err := OpenGatewayStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dir := t.TempDir()
	registry := NewSkillRegistryService(store, dir, []string{"skill_load"})
	invalid := registry.Validate(context.Background(), []byte("---\nname: ../bad\ndescription: bad\n---\nbody"))
	if invalid.Valid || invalid.Error == "" {
		t.Fatalf("validation=%+v", invalid)
	}
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte("---\nname: linked\ndescription: linked\n---\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "linked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked", "SKILL.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	result, err := registry.Reload(context.Background())
	if err != nil || result.Loaded != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSkillLoadAndCatalogAreAccountScoped(t *testing.T) {
	snapshot := SkillSnapshot{Generation: 7, Skills: []*runtime.Skill{{
		Frontmatter: runtime.SkillFrontmatter{Name: "digest", Description: "Get digest"}, Body: "Use stock tool",
	}}, Summaries: map[string]SkillSummary{}}
	tool := &SkillLoadTool{Snapshot: snapshot}
	result, err := tool.Execute(context.Background(), "call", []byte(`{"name":"digest"}`), nil)
	text, _ := result.Content[0].(agentcore.TextContent)
	if err != nil || !strings.Contains(text.Text, "Use stock tool") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	prompt := withGatewaySkillCatalog("base", snapshot)
	if !strings.Contains(prompt, "digest: Get digest") || strings.Count(withGatewaySkillCatalog(prompt, snapshot), gatewaySkillCatalogStart) != 1 {
		t.Fatalf("prompt=%q", prompt)
	}
	if expanded := expandGatewaySkillCommand("/digest AAPL", snapshot); !strings.Contains(expanded, "AAPL") || !strings.Contains(expanded, "Use stock tool") {
		t.Fatalf("expanded=%q", expanded)
	}
}
