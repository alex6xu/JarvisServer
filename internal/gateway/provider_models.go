package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type upstreamModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

func fetchProviderModels(ctx context.Context, p Provider, allowPrivate bool) ([]string, error) {
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
	var payload upstreamModelList
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode provider models: %w", err)
	}
	models := make([]string, 0, len(payload.Data)+len(payload.Models))
	seen := make(map[string]struct{})
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, exists := seen[model]; exists {
			return
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	for _, model := range payload.Data {
		add(model.ID)
	}
	for _, model := range payload.Models {
		if model.Name != "" {
			add(model.Name)
		} else {
			add(model.Model)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("provider returned no models")
	}
	return models, nil
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
