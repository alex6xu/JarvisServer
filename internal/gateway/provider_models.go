package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/distributedlog"
)

type upstreamModelList struct {
	Data []struct {
		ID              string `json:"id"`
		ContextLength   int    `json:"context_length"`
		ContextWindow   int    `json:"context_window"`
		MaxInputTokens  int    `json:"max_input_tokens"`
		MaxOutputTokens int    `json:"max_output_tokens"`
		TopProvider     struct {
			ContextLength int `json:"context_length"`
		} `json:"top_provider"`
	} `json:"data"`
	Models []struct {
		Name             string `json:"name"`
		Model            string `json:"model"`
		InputTokenLimit  int    `json:"inputTokenLimit"`
		OutputTokenLimit int    `json:"outputTokenLimit"`
		ContextLength    int    `json:"context_length"`
	} `json:"models"`
}

func fetchProviderModels(ctx context.Context, p Provider, allowPrivate bool) ([]string, error) {
	metadata, err := fetchProviderModelMetadata(ctx, p, allowPrivate)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(metadata))
	for _, model := range metadata {
		models = append(models, model.ID)
	}
	return models, nil
}

func fetchProviderModelMetadata(ctx context.Context, p Provider, allowPrivate bool) ([]ProviderModelMetadata, error) {
	base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if base == "" {
		switch p.Type {
		case 1:
			base = "https://api.openai.com/v1"
		case 2:
			base = "https://api.anthropic.com/v1"
		case 4:
			base = "https://api.deepseek.com/v1"
		case 5:
			base = "http://127.0.0.1:11434"
		default:
			return nil, fmt.Errorf("base_url is required for this provider type")
		}
	}
	endpoint := base + "/models"
	if p.Type == 5 && !strings.HasSuffix(base, "/v1") {
		endpoint = base + "/api/tags"
	}
	if err := validateProviderURL(ctx, endpoint, allowPrivate || p.Type == 5); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if p.Key != "" {
		if p.Type == 2 {
			req.Header.Set("x-api-key", p.Key)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else if p.Type == 3 && isGoogleGenerativeLanguageURL(endpoint) {
			req.Header.Set("x-goog-api-key", p.Key)
		} else {
			req.Header.Set("Authorization", "Bearer "+p.Key)
		}
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return fmt.Errorf("provider redirects are disabled")
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch provider models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	metadata, err := decodeProviderModelMetadata(body, p)
	if err != nil {
		return nil, err
	}
	if p.Type == 5 && !strings.HasSuffix(base, "/v1") {
		metadata = enrichOllamaModelMetadata(ctx, client, base, metadata)
	}
	return metadata, nil
}

func isGoogleGenerativeLanguageURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com")
}

func decodeProviderModelMetadata(body []byte, p Provider) ([]ProviderModelMetadata, error) {
	var payload upstreamModelList
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode provider models: %w", err)
	}
	models := make([]ProviderModelMetadata, 0, len(payload.Data)+len(payload.Models))
	seen := make(map[string]struct{})
	detectedAt := time.Now().UTC().Format(time.RFC3339Nano)
	add := func(model ProviderModelMetadata) {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			return
		}
		key := strings.ToLower(model.ID)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		model.MetadataSource = "auto:discovery"
		model.DetectedAt = detectedAt
		resolveProviderModelMetadata(&model, p.ContextWindow)
		models = append(models, model)
	}
	for _, model := range payload.Data {
		window := model.ContextLength
		if window <= 0 {
			window = model.ContextWindow
		}
		if window <= 0 {
			window = model.TopProvider.ContextLength
		}
		add(ProviderModelMetadata{ID: model.ID, ContextWindow: window,
			MaxInputTokens: model.MaxInputTokens, MaxOutputTokens: model.MaxOutputTokens})
	}
	for _, model := range payload.Models {
		id := model.Name
		if id == "" {
			id = model.Model
		}
		window := model.ContextLength
		if window <= 0 && model.InputTokenLimit > 0 && model.OutputTokenLimit > 0 {
			window = model.InputTokenLimit + model.OutputTokenLimit
		}
		add(ProviderModelMetadata{ID: id, ContextWindow: window,
			MaxInputTokens: model.InputTokenLimit, MaxOutputTokens: model.OutputTokenLimit})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("provider returned no models")
	}
	return models, nil
}

func enrichOllamaModelMetadata(ctx context.Context, client *http.Client, base string, models []ProviderModelMetadata) []ProviderModelMetadata {
	for i := range models {
		body, _ := json.Marshal(map[string]string{"name": models[i].ID})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/show", bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return models
			}
			continue
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		if window := decodeOllamaContextWindow(responseBody); window > 0 {
			models[i].ContextWindow = window
			models[i].MetadataSource = "auto:discovery"
			resolveProviderModelMetadata(&models[i], 0)
		}
	}
	return models
}

func decodeOllamaContextWindow(body []byte) int {
	var payload struct {
		ModelInfo map[string]json.RawMessage `json:"model_info"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return 0
	}
	for key, raw := range payload.ModelInfo {
		if key != "general.context_length" && !strings.HasSuffix(key, ".context_length") {
			continue
		}
		var value float64
		if json.Unmarshal(raw, &value) == nil && value > 0 {
			return int(value)
		}
	}
	return 0
}

// resolveProviderModelMetadata applies exact provider+model metadata precedence:
// manual model override, provider discovery, built-in catalog, provider default,
// then a conservative fallback. Unknown discovered values remain zero in storage.
func resolveProviderModelMetadata(model *ProviderModelMetadata, providerDefault int) {
	if model == nil {
		return
	}
	if model.ManualContextWindow > 0 {
		model.EffectiveContextWindow = model.ManualContextWindow
		model.EffectiveSource = "manual"
		return
	}
	if model.ContextWindow > 0 {
		model.EffectiveContextWindow = model.ContextWindow
		model.EffectiveSource = model.MetadataSource
		if model.EffectiveSource == "" {
			model.EffectiveSource = "auto:discovery"
		}
		return
	}
	if window := catalogContextWindow(model.ID); window > 0 {
		model.EffectiveContextWindow = window
		model.EffectiveSource = "catalog"
		return
	}
	if providerDefault > 0 {
		model.EffectiveContextWindow = providerDefault
		model.EffectiveSource = "provider_default"
		return
	}
	model.EffectiveContextWindow = 32768
	model.EffectiveSource = "conservative_fallback"
}

func catalogContextWindow(model string) int {
	id := strings.ToLower(strings.TrimSpace(model))
	// Exact IDs avoid incorrectly inheriting a family limit for a new model.
	return map[string]int{
		"gpt-4o": 128000, "gpt-4o-mini": 128000, "gpt-4.1": 1047576,
		"gpt-4.1-mini": 1047576, "gpt-4.1-nano": 1047576,
		"gemini-2.5-pro": 1048576, "gemini-2.5-flash": 1048576,
		"deepseek-chat": 128000, "deepseek-reasoner": 128000,
	}[id]
}

func (s *Service) startProviderMetadataReconciler() {
	ctx, cancel := context.WithCancel(context.Background())
	s.metadataCancel = cancel
	s.metadataDone = make(chan struct{})
	go func() {
		defer close(s.metadataDone)
		// Fetch shortly after startup without delaying readiness, then refresh
		// daily. Manual overrides are retained by the merge resolver.
		initial := time.NewTimer(time.Minute)
		defer initial.Stop()
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
			s.reconcileProviderModelMetadata(ctx)
		}
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileProviderModelMetadata(ctx)
			}
		}
	}()
}

func (s *Service) reconcileProviderModelMetadata(ctx context.Context) {
	changed := false
	for _, provider := range s.Mem.listProviders() {
		if !providerUsable(&provider) || len(parseProviderModels(provider.Models)) == 0 {
			continue
		}
		metadata, err := fetchProviderModelMetadata(ctx, provider, s.Opts.AllowPrivateProviderURLs)
		if err != nil {
			if ctx.Err() == nil && s.Logger != nil {
				s.Logger.Error(ctx, "provider model metadata reconciliation failed",
					distributedlog.F("provider_id", provider.ID), distributedlog.Err(err))
			}
			continue
		}
		if _, ok := s.Mem.mergeDiscoveredProviderMetadata(provider.ID, metadata); ok {
			changed = true
		}
	}
	if changed {
		if err := s.persistProviders(ctx); err != nil && ctx.Err() == nil && s.Logger != nil {
			s.Logger.Error(ctx, "persist provider model metadata failed", distributedlog.Err(err))
		}
	}
}

func validateProviderURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("invalid provider URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("provider URL scheme must be http or https")
	}
	if parsed.User != nil || parsed.Hostname() == "" {
		return fmt.Errorf("provider URL must not contain credentials and must include a host")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return fmt.Errorf("resolve provider host: %w", err)
	}
	if allowPrivate {
		return nil
	}
	for _, address := range addresses {
		ip := address.IP
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("provider URL resolves to a private or local address")
		}
	}
	return nil
}
