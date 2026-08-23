package router

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	failureThreshold = 3
	circuitDuration  = 30 * time.Second
)

type Engine struct {
	mu       sync.RWMutex
	health   map[string]Health
	store    HealthStore
	now      func() time.Time
	sequence atomic.Uint64
}

func New(store HealthStore) (*Engine, error) {
	engine := &Engine{health: make(map[string]Health), store: store, now: time.Now}
	if store != nil {
		loaded, err := store.LoadRouterHealth(context.Background())
		if err != nil {
			return nil, err
		}
		engine.health = loaded
	}
	return engine, nil
}

func (e *Engine) Plan(_ context.Context, request RouteRequest, endpoints []Endpoint) (RoutePlan, error) {
	policy := request.Policy
	if policy.ID == "" {
		policy = DefaultPolicy()
	}
	excluded := make(map[string]struct{}, len(request.ExcludeEndpoints))
	for _, id := range request.ExcludeEndpoints {
		excluded[id] = struct{}{}
	}
	now := e.now()
	candidates := make([]Candidate, 0, len(endpoints))
	var open []Candidate
	for _, endpoint := range endpoints {
		if !endpoint.Enabled {
			continue
		}
		if _, skip := excluded[endpoint.ID]; skip {
			continue
		}
		if policy.FixedEndpointID != "" && endpoint.ID != policy.FixedEndpointID {
			continue
		}
		if !endpoint.Capabilities.Satisfies(request.Required) {
			continue
		}
		model, ok := selectModel(endpoint.Models, request.RequestedModel)
		if !ok {
			continue
		}
		health := e.Health(endpoint.ID)
		candidate := Candidate{Endpoint: endpoint, Model: model}
		candidate.Score = scoreEndpoint(endpoint, health, policy)
		if request.PreferDefault && endpoint.Default {
			candidate.Score += 1_000_000
		}
		candidate.Reason = fmt.Sprintf("policy=%s priority=%d health=%.2f latency_p95=%dms", policy.Mode, endpoint.Priority, health.SuccessRate, health.LatencyP95Ms)
		if health.CircuitOpenUntil.After(now) {
			open = append(open, candidate)
		} else {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		candidates = open
	}
	if len(candidates) == 0 {
		return RoutePlan{}, fmt.Errorf("router: no endpoint satisfies model and capabilities")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Endpoint.ID < candidates[j].Endpoint.ID
	})
	e.rotateEqualScore(candidates)
	if policy.MaxAttempts > 0 && len(candidates) > policy.MaxAttempts {
		candidates = candidates[:policy.MaxAttempts]
	}
	return RoutePlan{Primary: candidates[0], Candidates: candidates,
		Reason: "hard filters passed; candidates ranked by policy", PolicyRev: policy.Revision}, nil
}

func scoreEndpoint(endpoint Endpoint, health Health, policy Policy) float64 {
	successRate := health.SuccessRate
	if successRate == 0 && health.ConsecutiveFailures == 0 {
		successRate = 1
	}
	healthScore := successRate * 100
	latency := float64(health.LatencyP95Ms) / 1000
	qualityWeight := policy.QualityWeight
	if qualityWeight <= 0 {
		qualityWeight = 20
	}
	qualityTier := max(endpoint.Capabilities.QualityTier, 1)
	quality := float64(qualityTier)
	score := float64(endpoint.Priority*100) + healthScore*policy.HealthWeight/100
	// Context metadata is advisory. A known-fitting route wins over unknown and
	// undersized routes, while the latter remain available for compaction or
	// provider-side handling when no better candidate exists.
	score += float64(endpoint.ContextRank) * 10_000_000
	if policy.PreferredQualityTier > 0 {
		distance := qualityTier - policy.PreferredQualityTier
		if distance < 0 {
			distance = -distance
		}
		score -= float64(distance) * qualityWeight
	} else {
		score += quality * qualityWeight
	}
	switch strings.ToLower(policy.Mode) {
	case "quality-first":
		score += healthScore*.5 + quality*60
	case "cost-first":
		score += quality * 5
		score -= endpoint.CostPerMTok * max(policy.CostWeight, 30)
	case "latency-first":
		score -= latency * max(policy.LatencyWeight, 30)
	default:
		score -= latency*policy.LatencyWeight + endpoint.CostPerMTok*policy.CostWeight
	}
	score -= float64(endpoint.ActiveLoad) * policy.LoadWeight
	if endpoint.Default {
		score += 5
	}
	return score
}

func (e *Engine) rotateEqualScore(candidates []Candidate) {
	if len(candidates) < 2 {
		return
	}
	end := 1
	for end < len(candidates) && candidates[end].Score == candidates[0].Score {
		end++
	}
	if end == 1 {
		return
	}
	total := 0
	for i := 0; i < end; i++ {
		total += max(candidates[i].Endpoint.Weight, 1)
	}
	slot := int((e.sequence.Add(1) - 1) % uint64(total))
	selected := 0
	for i := 0; i < end; i++ {
		slot -= max(candidates[i].Endpoint.Weight, 1)
		if slot < 0 {
			selected = i
			break
		}
	}
	if selected > 0 {
		chosen := candidates[selected]
		copy(candidates[1:selected+1], candidates[:selected])
		candidates[0] = chosen
	}
}

func selectModel(models []string, requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if len(models) == 0 {
			return "", false
		}
		return models[0], true
	}
	if len(models) == 0 {
		return requested, true
	}
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model), requested) {
			return requested, true
		}
	}
	return "", false
}

func (e *Engine) Observe(ctx context.Context, result AttemptResult) error {
	if result.EndpointID == "" {
		return nil
	}
	if result.OccurredAt.IsZero() {
		result.OccurredAt = e.now().UTC()
	}
	e.mu.Lock()
	health := e.health[result.EndpointID]
	health.EndpointID = result.EndpointID
	if result.Success {
		health.ConsecutiveFailures = 0
		health.CircuitOpenUntil = time.Time{}
		health.LastError = ""
		health.LastSuccess = result.OccurredAt
		health.SuccessRate = movingAverage(health.SuccessRate, 1)
	} else {
		health.ConsecutiveFailures++
		health.LastError = result.ErrorText
		health.SuccessRate = movingAverage(health.SuccessRate, 0)
		if health.ConsecutiveFailures >= failureThreshold {
			health.CircuitOpenUntil = result.OccurredAt.Add(circuitDuration)
		}
	}
	if result.Latency > 0 {
		latency := result.Latency.Milliseconds()
		if health.LatencyP95Ms == 0 || latency > health.LatencyP95Ms {
			health.LatencyP95Ms = latency
		} else {
			health.LatencyP95Ms = (health.LatencyP95Ms*19 + latency) / 20
		}
	}
	e.health[result.EndpointID] = health
	e.mu.Unlock()
	if e.store != nil {
		return e.store.SaveHealthSample(ctx, result, health)
	}
	return nil
}

func movingAverage(current, sample float64) float64 {
	if current == 0 && sample == 1 {
		return 1
	}
	return current*.9 + sample*.1
}

func (e *Engine) Health(endpointID string) Health {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.health[endpointID]
}

// SetClock replaces the clock used for circuit-breaker decisions.
func (e *Engine) SetClock(now func() time.Time) {
	if now != nil {
		e.now = now
	}
}
