package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	mu     sync.RWMutex
	policy corerouter.Policy
	now    func() time.Time
}

type RoutePurpose string

const (
	RoutePurposeDefault       RoutePurpose = "default"
	RoutePurposeChat          RoutePurpose = "chat"
	RoutePurposeCodeAnalysis  RoutePurpose = "code_analysis"
	RoutePurposeCodeExecution RoutePurpose = "code_execution"
	RoutePurposeCompaction    RoutePurpose = "compaction"
)

type RoutePlan struct {
	RequestedModel string       `json:"requested_model,omitempty"`
	Purpose        RoutePurpose `json:"purpose,omitempty"`
	Candidates     []LLMRoute   `json:"candidates"`
	Reason         string       `json:"reason,omitempty"`
	PolicyRev      int64        `json:"policy_rev,omitempty"`
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

func normalizeProviderConfig(p Provider) Provider {
	if !p.Capabilities.Chat && !p.Capabilities.Reasoning && !p.Capabilities.Coding {
		p.Capabilities = ProviderCapabilities{
			Chat: true, Reasoning: true, Coding: true, Tools: true, Images: true, Thinking: true,
		}
	}
	if p.ContextWindow <= 0 {
		p.ContextWindow = 32768
	}
	if p.QualityTier < 1 {
		p.QualityTier = 3
	}
	if p.QualityTier > 5 {
		p.QualityTier = 5
	}
	if p.CostPerMTok < 0 {
		p.CostPerMTok = 0
	}
	return p
}

func validateProviderInput(p Provider) error {
	if p.ContextWindow != 0 && p.ContextWindow < 4096 {
		return fmt.Errorf("context_window must be at least 4096")
	}
	if p.QualityTier != 0 && (p.QualityTier < 1 || p.QualityTier > 5) {
		return fmt.Errorf("quality_tier must be between 1 and 5")
	}
	if p.CostPerMTok < 0 {
		return fmt.Errorf("cost_per_mtok must not be negative")
	}
	return nil
}

func normalizeRoutePurpose(purpose RoutePurpose) RoutePurpose {
	switch purpose {
	case RoutePurposeChat, RoutePurposeCodeAnalysis, RoutePurposeCodeExecution, RoutePurposeCompaction:
		return purpose
	default:
		return RoutePurposeDefault
	}
}

func requirementsForPurpose(purpose RoutePurpose, minContextWindow int) corerouter.Capabilities {
	required := corerouter.Capabilities{ContextWindow: minContextWindow}
	switch normalizeRoutePurpose(purpose) {
	case RoutePurposeChat:
		required.Chat = true
		required.QualityTier = 1
	case RoutePurposeCodeAnalysis:
		required.Reasoning = true
		required.Tools = true
		required.QualityTier = 3
	case RoutePurposeCodeExecution:
		required.Coding = true
		required.Tools = true
		required.QualityTier = 2
	case RoutePurposeCompaction:
		required.Chat = true
		required.QualityTier = 2
	}
	return required
}

func policyForPurpose(base corerouter.Policy, purpose RoutePurpose) corerouter.Policy {
	policy := base
	if policy.QualityWeight <= 0 {
		policy.QualityWeight = 20
	}
	switch normalizeRoutePurpose(purpose) {
	case RoutePurposeChat:
		policy.Mode = "cost-first"
		policy.CostWeight = max(policy.CostWeight, 45)
		policy.QualityWeight = 30
		policy.PreferredQualityTier = 1
	case RoutePurposeCodeAnalysis:
		policy.Mode = "quality-first"
		policy.QualityWeight = max(policy.QualityWeight, 50)
		policy.PreferredQualityTier = 5
	case RoutePurposeCodeExecution:
		policy.Mode = "cost-first"
		policy.CostWeight = max(policy.CostWeight, 35)
		policy.QualityWeight = max(policy.QualityWeight, 20)
		policy.PreferredQualityTier = 3
	case RoutePurposeCompaction:
		policy.Mode = "cost-first"
		policy.CostWeight = max(policy.CostWeight, 50)
		policy.QualityWeight = 25
		policy.PreferredQualityTier = 2
	}
	return policy
}

func (r *ProviderRouter) Plan(providers []Provider, requestedModel string, fallback LLMRoute) (RoutePlan, error) {
	return r.PlanForPurpose(providers, requestedModel, fallback, RoutePurposeDefault, 0)
}

func (r *ProviderRouter) PlanForPurpose(providers []Provider, requestedModel string, fallback LLMRoute, purpose RoutePurpose, minContextWindow int) (RoutePlan, error) {
	endpoints := make([]corerouter.Endpoint, 0, len(providers))
	providersByID := make(map[int]Provider, len(providers))
	for i := range providers {
		provider := normalizeProviderConfig(providers[i])
		providersByID[provider.ID] = provider
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
			CostPerMTok: provider.CostPerMTok,
			Capabilities: corerouter.Capabilities{
				Chat: provider.Capabilities.Chat, Reasoning: provider.Capabilities.Reasoning,
				Coding: provider.Capabilities.Coding, Tools: provider.Capabilities.Tools,
				Images: provider.Capabilities.Images, Thinking: provider.Capabilities.Thinking,
				QualityTier: provider.QualityTier, ContextWindow: provider.ContextWindow,
			},
		})
	}
	r.mu.RLock()
	policy := policyForPurpose(r.policy, purpose)
	r.mu.RUnlock()
	request := corerouter.RouteRequest{
		RequestedModel: requestedModel, Required: requirementsForPurpose(purpose, minContextWindow),
		Policy: policy, PreferDefault: requestedModel == "" && purpose == RoutePurposeDefault,
	}
	plan, err := r.engine.Plan(context.Background(), request, endpoints)
	if err != nil && normalizeRoutePurpose(purpose) == RoutePurposeCodeExecution {
		// A reasoning model with tool support can continue a coding run even when
		// it is not explicitly tagged for coding. This matches the eligibility
		// rule used to size the run context window and prevents a route from
		// disappearing when Coder moves from analysis to execution turns.
		request.Required = requirementsForPurpose(RoutePurposeCodeAnalysis, minContextWindow)
		if reasoningPlan, reasoningErr := r.engine.Plan(context.Background(), request, endpoints); reasoningErr == nil {
			plan, err = reasoningPlan, nil
			plan.Reason = "code execution fallback to reasoning provider: " + plan.Reason
		}
	}
	if err != nil {
		if strings.TrimSpace(fallback.Model) == "" || (minContextWindow > 0 && fallback.ContextWindow < minContextWindow) {
			purpose = normalizeRoutePurpose(purpose)
			if strings.TrimSpace(requestedModel) == "" {
				return RoutePlan{}, fmt.Errorf("no available provider route for automatic model selection (purpose %q)", purpose)
			}
			return RoutePlan{}, fmt.Errorf("no available provider route for model %q (purpose %q)", requestedModel, purpose)
		}
		if requestedModel != "" {
			fallback.Model = requestedModel
		}
		fallback.Purpose = purpose
		if fallback.ContextWindow <= 0 {
			fallback.ContextWindow = 32768
		}
		return RoutePlan{RequestedModel: requestedModel, Purpose: purpose, Candidates: []LLMRoute{fallback}, Reason: "startup fallback"}, nil
	}
	out := RoutePlan{RequestedModel: requestedModel, Purpose: purpose, Reason: plan.Reason, PolicyRev: plan.PolicyRev}
	for _, candidate := range plan.Candidates {
		endpoint := candidate.Endpoint
		_, driverName := mapChannelType(providersByID[endpoint.ProviderID].Type, endpoint.BaseURL)
		out.Candidates = append(out.Candidates, LLMRoute{
			Model: candidate.Model, BaseURL: endpoint.BaseURL, Protocol: endpoint.Protocol,
			ProviderName: driverName, APIKey: endpoint.Credential, ProviderID: endpoint.ProviderID,
			ProviderLabel: endpoint.ProviderName, Priority: endpoint.Priority,
			Weight: endpoint.Weight, IsDefault: endpoint.Default, EndpointID: endpoint.ID,
			ContextWindow: endpoint.Capabilities.ContextWindow, QualityTier: endpoint.Capabilities.QualityTier,
			CostPerMTok: endpoint.CostPerMTok, Purpose: purpose,
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

func (r *ProviderRouter) SetPolicy(policy corerouter.Policy) {
	r.mu.Lock()
	r.policy = policy
	r.mu.Unlock()
}

func (r *ProviderRouter) ObserveResult(result corerouter.AttemptResult) error {
	return r.engine.Observe(context.Background(), result)
}

func classifyProviderError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return runStatusCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "context canceled"), strings.Contains(text, "context cancelled"):
		return runStatusCancelled
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
