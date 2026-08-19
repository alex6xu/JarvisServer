package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	corerouter "github.com/alex6xu/jarvisserver/internal/router"
)

const (
	providerFailureThreshold = 3
	providerCircuitDuration  = 30 * time.Second
)

type ProviderHealth = corerouter.Health

type ProviderRouter struct {
	engine *corerouter.Engine
	policy corerouter.Policy
	now    func() time.Time
}

type RoutePlan struct {
	RequestedModel string     `json:"requested_model,omitempty"`
	Candidates     []LLMRoute `json:"candidates"`
	Reason         string     `json:"reason,omitempty"`
	PolicyRev      int64      `json:"policy_rev,omitempty"`
}

func NewProviderRouter() *ProviderRouter {
	engine, _ := corerouter.New(nil)
	router := &ProviderRouter{engine: engine, policy: corerouter.DefaultPolicy(), now: time.Now}
	engine.SetClock(func() time.Time { return router.now() })
	return router
}

func NewPersistentProviderRouter(store corerouter.HealthStore, policy corerouter.Policy) (*ProviderRouter, error) {
	engine, err := corerouter.New(store)
	if err != nil {
		return nil, err
	}
	router := &ProviderRouter{engine: engine, policy: policy, now: time.Now}
	engine.SetClock(func() time.Time { return router.now() })
	return router, nil
}

func (r *ProviderRouter) Plan(providers []Provider, requestedModel string, fallback LLMRoute) (RoutePlan, error) {
	endpoints := make([]corerouter.Endpoint, 0, len(providers))
	for i := range providers {
		provider := providers[i]
		if !providerUsable(&provider) {
			continue
		}
		protocol, providerName := mapChannelType(provider.Type, provider.BaseURL)
		if protocol == "openai" && strings.TrimSpace(provider.BaseURL) == "" {
			continue
		}
		label := provider.Name
		if label == "" {
			label = providerName
		}
		endpoints = append(endpoints, corerouter.Endpoint{
			ID: fmt.Sprintf("provider_%d", provider.ID), ProviderID: provider.ID,
			ProviderName: label, Enabled: provider.Status != 0,
			Models: parseProviderModels(provider.Models), BaseURL: strings.TrimSpace(provider.BaseURL),
			Protocol: protocol, Credential: provider.Key, Priority: provider.Priority,
			Weight: max(provider.Weight, 1), Default: provider.IsDefault == 1,
			Capabilities: corerouter.Capabilities{Tools: true, Images: true, Thinking: true},
		})
	}
	plan, err := r.engine.Plan(context.Background(), corerouter.RouteRequest{
		RequestedModel: requestedModel, Policy: r.policy, PreferDefault: requestedModel == "",
	}, endpoints)
	if err != nil {
		if strings.TrimSpace(fallback.Model) == "" {
			return RoutePlan{}, fmt.Errorf("no available provider route for model %q", requestedModel)
		}
		if requestedModel != "" {
			fallback.Model = requestedModel
		}
		return RoutePlan{RequestedModel: requestedModel, Candidates: []LLMRoute{fallback}, Reason: "startup fallback"}, nil
	}
	out := RoutePlan{RequestedModel: requestedModel, Reason: plan.Reason, PolicyRev: plan.PolicyRev}
	for _, candidate := range plan.Candidates {
		endpoint := candidate.Endpoint
		out.Candidates = append(out.Candidates, LLMRoute{
			Model: candidate.Model, BaseURL: endpoint.BaseURL, Protocol: endpoint.Protocol,
			APIKey: endpoint.Credential, ProviderID: endpoint.ProviderID,
			ProviderLabel: endpoint.ProviderName, Priority: endpoint.Priority,
			Weight: endpoint.Weight, IsDefault: endpoint.Default,
		})
	}
	return out, nil
}

func (r *ProviderRouter) Observe(providerID int, err error) {
	if providerID == 0 {
		return
	}
	result := corerouter.AttemptResult{
		EndpointID: fmt.Sprintf("provider_%d", providerID), Success: err == nil, OccurredAt: r.now().UTC(),
	}
	if err != nil {
		result.ErrorText = err.Error()
		result.ErrorCategory = classifyProviderError(err)
	}
	_ = r.engine.Observe(context.Background(), result)
}

func (r *ProviderRouter) Health(providerID int) ProviderHealth {
	return r.engine.Health(fmt.Sprintf("provider_%d", providerID))
}

func (r *ProviderRouter) SetPolicy(policy corerouter.Policy) { r.policy = policy }

func (r *ProviderRouter) ObserveResult(result corerouter.AttemptResult) error {
	return r.engine.Observe(context.Background(), result)
}

func classifyProviderError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "401"), strings.Contains(text, "403"), strings.Contains(text, "auth"):
		return "authentication"
	case strings.Contains(text, "429"), strings.Contains(text, "rate"):
		return "rate_limit"
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline"):
		return "timeout"
	default:
		return "upstream"
	}
}
