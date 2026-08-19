// Package router plans provider/model endpoints independently from Gateway HTTP and LLM transports.
package router

import (
	"context"
	"time"
)

type Capabilities struct {
	Tools         bool `json:"tools"`
	Images        bool `json:"images"`
	Thinking      bool `json:"thinking"`
	Chat          bool `json:"chat"`
	Reasoning     bool `json:"reasoning"`
	Coding        bool `json:"coding"`
	QualityTier   int  `json:"quality_tier"`
	ContextWindow int  `json:"context_window"`
}

func (available Capabilities) Satisfies(required Capabilities) bool {
	return (!required.Tools || available.Tools) &&
		(!required.Images || available.Images) &&
		(!required.Thinking || available.Thinking) &&
		(!required.Chat || available.Chat) &&
		(!required.Reasoning || available.Reasoning) &&
		(!required.Coding || available.Coding) &&
		(required.QualityTier <= 0 || available.QualityTier >= required.QualityTier) &&
		(required.ContextWindow <= 0 || available.ContextWindow >= required.ContextWindow)
}

type Endpoint struct {
	ID           string       `json:"id"`
	ProviderID   int          `json:"provider_id"`
	ProviderName string       `json:"provider_name"`
	Enabled      bool         `json:"enabled"`
	Models       []string     `json:"models"`
	BaseURL      string       `json:"base_url,omitempty"`
	Protocol     string       `json:"protocol,omitempty"`
	Credential   string       `json:"-"`
	Priority     int          `json:"priority"`
	Weight       int          `json:"weight"`
	Default      bool         `json:"default"`
	Capabilities Capabilities `json:"capabilities"`
	CostPerMTok  float64      `json:"cost_per_mtok,omitempty"`
	ActiveLoad   int          `json:"active_load,omitempty"`
}

type Policy struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Mode                 string  `json:"mode"`
	Revision             int64   `json:"revision"`
	FixedEndpointID      string  `json:"fixed_endpoint_id,omitempty"`
	MaxAttempts          int     `json:"max_attempts"`
	HealthWeight         float64 `json:"health_weight"`
	LatencyWeight        float64 `json:"latency_weight"`
	CostWeight           float64 `json:"cost_weight"`
	LoadWeight           float64 `json:"load_weight"`
	QualityWeight        float64 `json:"quality_weight"`
	PreferredQualityTier int     `json:"preferred_quality_tier,omitempty"`
}

func DefaultPolicy() Policy {
	return Policy{ID: "balanced", Name: "Balanced", Mode: "balanced", Revision: 1,
		MaxAttempts: 3, HealthWeight: 30, LatencyWeight: 10, CostWeight: 15, LoadWeight: 20, QualityWeight: 20}
}

type RouteRequest struct {
	RunID            string
	SessionID        string
	RequestedModel   string
	Required         Capabilities
	Policy           Policy
	Attempt          int
	ExcludeEndpoints []string
	PreferDefault    bool
}

type Candidate struct {
	Endpoint Endpoint `json:"endpoint"`
	Model    string   `json:"model"`
	Score    float64  `json:"score"`
	Reason   string   `json:"reason"`
}

type RoutePlan struct {
	Primary    Candidate   `json:"primary"`
	Candidates []Candidate `json:"candidates"`
	Reason     string      `json:"reason"`
	PolicyRev  int64       `json:"policy_rev"`
}

type Health struct {
	EndpointID          string    `json:"endpoint_id"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	CircuitOpenUntil    time.Time `json:"circuit_open_until,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	LastSuccess         time.Time `json:"last_success,omitempty"`
	SuccessRate         float64   `json:"success_rate"`
	LatencyP95Ms        int64     `json:"latency_p95_ms"`
}

type AttemptResult struct {
	EndpointID    string
	RunID         string
	AttemptID     string
	Success       bool
	ErrorCategory string
	ErrorText     string
	Latency       time.Duration
	FirstToken    time.Duration
	OccurredAt    time.Time
}

type HealthStore interface {
	LoadRouterHealth(context.Context) (map[string]Health, error)
	SaveHealthSample(context.Context, AttemptResult, Health) error
}
