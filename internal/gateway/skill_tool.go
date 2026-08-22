package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
)

type SkillLoadTool struct {
	Snapshot SkillSnapshot
}

func (t *SkillLoadTool) Name() string { return "skill_load" }

func (t *SkillLoadTool) Description() string {
	return "Load the instructions for one enabled skill by name. This only reads the account-scoped skill catalog and cannot read arbitrary files."
}

func (t *SkillLoadTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","maxLength":64}},"required":["name"],"additionalProperties":false}`)
}

func (t *SkillLoadTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionParallel
}

func (t *SkillLoadTool) Execute(_ context.Context, _ string, args json.RawMessage, _ agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	var request struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return skillLoadError("invalid arguments"), nil
	}
	name := strings.TrimSpace(request.Name)
	for _, skill := range t.Snapshot.Skills {
		if skill.Frontmatter.Name == name {
			return agentcore.AgentToolResult{
				Content: agentcore.ContentList{agentcore.NewTextContent(fmt.Sprintf("Skill %s instructions:\n\n%s", name, skill.Body))},
				Details: map[string]any{"name": name, "generation": t.Snapshot.Generation},
			}, nil
		}
	}
	return skillLoadError("skill is not enabled or does not exist"), nil
}

func skillLoadError(message string) agentcore.AgentToolResult {
	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent("skill_load: " + message)},
		Details: map[string]any{"isError": true},
	}
}

const gatewaySkillCatalogStart = "\n\n<!-- jarvis-gateway-skills:start -->\n"
const gatewaySkillCatalogEnd = "\n<!-- jarvis-gateway-skills:end -->"

func withGatewaySkillCatalog(base string, snapshot SkillSnapshot) string {
	base = stripGatewaySkillCatalog(base)
	visible := make([]string, 0)
	for _, skill := range snapshot.Skills {
		if skill.Frontmatter.DisableModelInvocation {
			continue
		}
		visible = append(visible, fmt.Sprintf("- %s: %s", skill.Frontmatter.Name, skill.Frontmatter.Description))
	}
	if len(visible) == 0 {
		return base
	}
	return base + gatewaySkillCatalogStart +
		"Available skills are account-scoped. When a skill matches the user's request, call skill_load with its name before following it.\n" +
		strings.Join(visible, "\n") + gatewaySkillCatalogEnd
}

func stripGatewaySkillCatalog(prompt string) string {
	for {
		start := strings.Index(prompt, gatewaySkillCatalogStart)
		if start < 0 {
			break
		}
		endRelative := strings.Index(prompt[start+len(gatewaySkillCatalogStart):], gatewaySkillCatalogEnd)
		if endRelative < 0 {
			break
		}
		end := start + len(gatewaySkillCatalogStart) + endRelative + len(gatewaySkillCatalogEnd)
		prompt = prompt[:start] + prompt[end:]
	}
	legacyStart := "\n\nSkills: load a matching skill with the read tool; resolve relative paths from the directory containing SKILL.md.\n\n<available_skills>"
	if start := strings.Index(prompt, legacyStart); start >= 0 {
		if endRelative := strings.Index(prompt[start:], "</available_skills>"); endRelative >= 0 {
			end := start + endRelative + len("</available_skills>")
			prompt = prompt[:start] + prompt[end:]
		}
	}
	return prompt
}

func expandGatewaySkillCommand(message string, snapshot SkillSnapshot) string {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "/") {
		return message
	}
	command, args, _ := strings.Cut(strings.TrimPrefix(trimmed, "/"), " ")
	for _, skill := range snapshot.Skills {
		if skill.Frontmatter.Name != command {
			continue
		}
		expanded := "Follow this enabled skill for the current request:\n\n" + skill.Body
		if args = strings.TrimSpace(args); args != "" {
			expanded += "\n\nUser arguments:\n" + args
		}
		return expanded
	}
	return message
}
