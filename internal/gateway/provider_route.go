package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LLMRoute is the resolved model + wire settings for one chat run.
type LLMRoute struct {
	Model         string
	BaseURL       string
	Protocol      string
	ProviderName  string
	APIKey        string
	ProviderID    int
	ProviderLabel string
	Priority      int  `json:"priority"`
	Weight        int  `json:"weight"`
	IsDefault     bool `json:"is_default"`
}

// resolveLLM picks a MemStore provider (by model match, else default) and maps it
// onto SetupEnv arguments. Falls back to yaml/env Options when no usable provider exists.
func (s *Service) resolveLLM(requestedModel string) (LLMRoute, error) {
	plan, err := s.resolveLLMPlan(requestedModel)
	if err != nil {
		return LLMRoute{}, err
	}
	return plan.Candidates[0], nil
}

func (s *Service) resolveLLMPlan(requestedModel string) (RoutePlan, error) {
	model := strings.TrimSpace(requestedModel)
	if model == "" {
		model = s.Opts.Model
	}
	fallback := LLMRoute{Model: model, BaseURL: s.Opts.BaseURL, Protocol: s.Opts.Protocol, ProviderName: s.Opts.ProviderName, APIKey: s.Opts.APIKey}
	return s.Router.Plan(s.Mem.listProviders(), requestedModel, fallback)
}

func providerUsable(p *Provider) bool {
	if p == nil || p.Status == 0 {
		return false
	}
	// OAuth Claude is not wired yet; require API key for real calls.
	if strings.EqualFold(p.AuthMode, "oauth") {
		return false
	}
	return strings.TrimSpace(p.Key) != "" || p.Type == 5 // ollama may need no key
}

// mapChannelType converts the web channel type id into SetupEnv protocol/providerName.
func mapChannelType(t int, baseURL string) (protocol, providerName string) {
	baseURL = strings.TrimSpace(baseURL)
	switch t {
	case 2: // Claude / Anthropic Messages
		return "anthropic", ""
	case 5: // Ollama
		if baseURL == "" {
			return "", "ollama"
		}
		return "openai", ""
	case 4: // DeepSeek
		if baseURL != "" {
			return "openai", ""
		}
		return "", "deepseek"
	case 1: // OpenAI first-party
		if baseURL != "" {
			return "openai", ""
		}
		return "", "openai"
	default: // gemini/mimo/agnes/glm/custom — OpenAI-compatible HTTP
		return "openai", ""
	}
}

func parseProviderModels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			out := make([]string, 0, len(arr))
			for _, m := range arr {
				m = strings.TrimSpace(m)
				if m != "" {
					out = append(out, m)
				}
			}
			return out
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func providersPersistPath() string {
	if dir := os.Getenv("JARVIS_HOME"); dir != "" {
		return filepath.Join(dir, "gateway-providers.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".jarvis", "gateway-providers.json")
}

func (m *MemStore) loadProvidersFromDisk() error {
	path := providersPersistPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []Provider
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers = make(map[int]*Provider)
	maxID := 0
	for i := range list {
		p := list[i]
		if p.ID <= 0 {
			continue
		}
		cp := p
		m.providers[p.ID] = &cp
		if p.ID > maxID {
			maxID = p.ID
		}
	}
	m.nextProvID = int64(maxID)
	return nil
}

func parseProviderID(s string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid provider id")
	}
	return id, nil
}
