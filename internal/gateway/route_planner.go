package gateway

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	providerFailureThreshold = 3
	providerCircuitDuration  = 30 * time.Second
)

type ProviderHealth struct {
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CircuitOpenUntil    time.Time `json:"circuit_open_until,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	LastSuccess         time.Time `json:"last_success,omitempty"`
}

type ProviderRouter struct {
	mu       sync.RWMutex
	health   map[int]ProviderHealth
	now      func() time.Time
	sequence atomic.Uint64
}

type RoutePlan struct {
	RequestedModel string     `json:"requested_model,omitempty"`
	Candidates     []LLMRoute `json:"candidates"`
}

func NewProviderRouter() *ProviderRouter {
	return &ProviderRouter{health: make(map[int]ProviderHealth), now: time.Now}
}

func (r *ProviderRouter) Plan(providers []Provider, requestedModel string, fallback LLMRoute) (RoutePlan, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	now := r.now()
	type planned struct {
		route  LLMRoute
		health ProviderHealth
		match  bool
		open   bool
	}
	all := make([]planned, 0, len(providers))
	for i := range providers {
		p := providers[i]
		if !providerUsable(&p) {
			continue
		}
		models := parseProviderModels(p.Models)
		matched := requestedModel == "" || len(models) == 0 || containsModel(models, requestedModel)
		if !matched {
			continue
		}
		model := requestedModel
		if model == "" {
			if len(models) == 0 {
				continue
			}
			model = models[0]
		}
		protocol, providerName := mapChannelType(p.Type, p.BaseURL)
		if protocol == "openai" && strings.TrimSpace(p.BaseURL) == "" {
			continue
		}
		h := r.Health(p.ID)
		all = append(all, planned{
			route: LLMRoute{
				Model: model, BaseURL: strings.TrimSpace(p.BaseURL), Protocol: protocol,
				ProviderName: providerName, APIKey: p.Key, ProviderID: p.ID,
				ProviderLabel: p.Name, Priority: p.Priority, Weight: p.Weight,
				IsDefault: p.IsDefault == 1,
			},
			health: h,
			match:  matched,
			open:   h.CircuitOpenUntil.After(now),
		})
	}

	// Prefer closed circuits. If every configured candidate is open, keep them in
	// the plan so the gateway remains available and can probe the best candidate.
	closed := all[:0]
	for _, candidate := range all {
		if !candidate.open {
			closed = append(closed, candidate)
		}
	}
	if len(closed) > 0 {
		all = closed
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if requestedModel == "" && a.route.IsDefault != b.route.IsDefault {
			return a.route.IsDefault
		}
		if a.route.Priority != b.route.Priority {
			return a.route.Priority > b.route.Priority
		}
		if a.health.ConsecutiveFailures != b.health.ConsecutiveFailures {
			return a.health.ConsecutiveFailures < b.health.ConsecutiveFailures
		}
		if a.route.Weight != b.route.Weight {
			return a.route.Weight > b.route.Weight
		}
		return a.route.ProviderID < b.route.ProviderID
	})
	if len(all) > 1 {
		end := 1
		for end < len(all) && sameRouteClass(all[0], all[end], requestedModel == "") {
			end++
		}
		totalWeight := 0
		for i := 0; i < end; i++ {
			totalWeight += max(all[i].route.Weight, 1)
		}
		if totalWeight > 0 {
			slot := int((r.sequence.Add(1) - 1) % uint64(totalWeight))
			selected := 0
			for i := 0; i < end; i++ {
				slot -= max(all[i].route.Weight, 1)
				if slot < 0 {
					selected = i
					break
				}
			}
			if selected > 0 {
				chosen := all[selected]
				copy(all[1:selected+1], all[0:selected])
				all[0] = chosen
			}
		}
	}

	plan := RoutePlan{RequestedModel: requestedModel, Candidates: make([]LLMRoute, 0, len(all)+1)}
	for _, candidate := range all {
		plan.Candidates = append(plan.Candidates, candidate.route)
	}
	if len(plan.Candidates) == 0 && strings.TrimSpace(fallback.Model) != "" {
		if requestedModel != "" {
			fallback.Model = requestedModel
		}
		plan.Candidates = append(plan.Candidates, fallback)
	}
	if len(plan.Candidates) == 0 {
		return RoutePlan{}, fmt.Errorf("no available provider route for model %q", requestedModel)
	}
	return plan, nil
}

func sameRouteClass(a, b struct {
	route  LLMRoute
	health ProviderHealth
	match  bool
	open   bool
}, considerDefault bool) bool {
	return (!considerDefault || a.route.IsDefault == b.route.IsDefault) &&
		a.route.Priority == b.route.Priority &&
		a.health.ConsecutiveFailures == b.health.ConsecutiveFailures
}

func containsModel(models []string, requested string) bool {
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), requested) {
			return true
		}
	}
	return false
}

func (r *ProviderRouter) Observe(providerID int, err error) {
	if providerID == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.health[providerID]
	if err == nil {
		h.ConsecutiveFailures = 0
		h.CircuitOpenUntil = time.Time{}
		h.LastError = ""
		h.LastSuccess = r.now().UTC()
	} else {
		h.ConsecutiveFailures++
		h.LastError = err.Error()
		if h.ConsecutiveFailures >= providerFailureThreshold {
			h.CircuitOpenUntil = r.now().Add(providerCircuitDuration).UTC()
		}
	}
	r.health[providerID] = h
}

func (r *ProviderRouter) Health(providerID int) ProviderHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.health[providerID]
}
