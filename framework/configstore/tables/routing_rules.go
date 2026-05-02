package tables

import (
	"strings"
	"time"

	"github.com/bytedance/sonic"
	bifrost "github.com/maximhq/bifrost/core"
	"gorm.io/gorm"
)

// HealthPolicy defines failure-threshold / cooldown parameters for grouped health routing
type HealthPolicy struct {
	FailureThreshold     int `json:"failure_threshold"`      // failures within window before cooldown (default: 2)
	FailureWindowSeconds int `json:"failure_window_seconds"` // sliding window in seconds (default: 30)
	CooldownSeconds      int `json:"cooldown_seconds"`       // cooldown duration in seconds (default: 30)
	ConsecutiveFailures  int `json:"consecutive_failures"`   // consecutive failures (ignoring time) before cooldown; 0 = use failure_threshold (default: 0)

	SlowThresholdMs        int     `json:"slow_threshold_ms,omitempty"`        // latency threshold for slow success (default: 45000)
	SlowWindowSize         int     `json:"slow_window_size,omitempty"`         // recent samples used for slow ratio (default: 10)
	SlowRatioThreshold     float64 `json:"slow_ratio_threshold,omitempty"`     // slow/window >= threshold enters degraded; >1 disables ratio (default: 0.5)
	SlowRecoverySeconds    *int    `json:"slow_recovery_seconds,omitempty"`    // nil = default 60; explicit 0 disables last-slow recency degraded
	RequestDeadlineMs      int     `json:"request_deadline_ms,omitempty"`      // reserved; 0 = disabled
	SoftCooldownMultiplier float64 `json:"soft_cooldown_multiplier,omitempty"` // soft failure cooldown multiplier (default: 2.0)
	CooldownBackoffFactor  float64 `json:"cooldown_backoff_factor,omitempty"`  // cooldown exponential backoff factor (default: 2.0)
	CooldownMaxSeconds     int     `json:"cooldown_max_seconds,omitempty"`     // maximum cooldown duration (default: 600)
	HalfOpenProbe          *bool   `json:"half_open_probe,omitempty"`          // nil = default true
}

// RouteGroupTarget is a single weighted target inside a route group.
// Grouped health routing requires provider and model to be explicit.
type RouteGroupTarget struct {
	Provider *string `json:"provider,omitempty"`
	Model    *string `json:"model,omitempty"`
	KeyID    *string `json:"key_id,omitempty"`
	Weight   float64 `json:"weight"`
}

// RouteGroup is an ordered group of targets with its own retry budget
type RouteGroup struct {
	Name         string             `json:"name"`
	RetryLimit   int                `json:"retry_limit"` // extra attempts after the first attempt; regular groups use other targets, fallback-only groups may repeat targets
	Targets      []RouteGroupTarget `json:"targets"`
	FallbackOnly bool               `json:"fallback_only,omitempty"` // appended after regular groups as request fallback; primary only when regular groups are unavailable; skipped by active probing
}

// TableRoutingRule represents a routing rule in the database
type TableRoutingRule struct {
	ID            string `gorm:"primaryKey;type:varchar(255)" json:"id"`
	ConfigHash    string `gorm:"type:varchar(255)" json:"config_hash"` // Hash of config.json version, used for change detection
	Name          string `gorm:"type:varchar(255);not null;uniqueIndex:idx_routing_rule_scope_name" json:"name"`
	Description   string `gorm:"type:text" json:"description"`
	Enabled       bool   `gorm:"not null;default:true" json:"enabled"`
	CelExpression string `gorm:"type:text;not null" json:"cel_expression"`

	// Routing Targets (output) — 1:many relationship; weights must sum to 1
	Targets []TableRoutingTarget `gorm:"foreignKey:RuleID;constraint:OnDelete:CASCADE" json:"targets,omitempty"`

	Fallbacks       *string  `gorm:"type:text" json:"-"`           // JSON array of fallback chains
	ParsedFallbacks []string `gorm:"-" json:"fallbacks,omitempty"` // Parsed fallbacks from JSON

	Query       *string        `gorm:"type:text" json:"-"`
	ParsedQuery map[string]any `gorm:"-" json:"query,omitempty"`

	// Grouped health routing (new — all optional, backward-compatible)
	GroupedRoutingEnabled bool          `gorm:"not null;default:false" json:"grouped_routing_enabled"`
	HealthPolicyJSON      *string       `gorm:"type:text;column:health_policy" json:"-"`
	ParsedHealthPolicy    *HealthPolicy `gorm:"-" json:"health_policy,omitempty"`
	RouteGroupsJSON       *string       `gorm:"type:text;column:route_groups" json:"-"`
	ParsedRouteGroups     []RouteGroup  `gorm:"-" json:"route_groups,omitempty"`

	// Scope: where this rule applies
	Scope   string  `gorm:"type:varchar(50);not null;uniqueIndex:idx_routing_rule_scope_name" json:"scope"` // "global" | "team" | "customer" | "virtual_key"
	ScopeID *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_rule_scope_name" json:"scope_id"`      // nil for global, otherwise entity ID

	// Execution
	Priority int `gorm:"type:int;not null;default:0;index" json:"priority"` // Lower = evaluated first within scope

	// Timestamps
	CreatedAt time.Time `gorm:"index;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"index;not null" json:"updated_at"`
}

// TableName for TableRoutingRule
func (TableRoutingRule) TableName() string { return "routing_rules" }

// BeforeSave hook for TableRoutingRule to serialize JSON fields
func (r *TableRoutingRule) BeforeSave(tx *gorm.DB) error {
	if len(r.ParsedFallbacks) > 0 {
		data, err := sonic.Marshal(r.ParsedFallbacks)
		if err != nil {
			return err
		}
		r.Fallbacks = bifrost.Ptr(string(data))
	} else {
		r.Fallbacks = nil
	}
	if r.ParsedQuery != nil {
		data, err := sonic.Marshal(r.ParsedQuery)
		if err != nil {
			return err
		}
		r.Query = bifrost.Ptr(string(data))
	} else {
		r.Query = nil
	}
	if r.ParsedHealthPolicy != nil {
		data, err := sonic.Marshal(r.ParsedHealthPolicy)
		if err != nil {
			return err
		}
		r.HealthPolicyJSON = bifrost.Ptr(string(data))
	} else {
		r.HealthPolicyJSON = nil
	}
	if len(r.ParsedRouteGroups) > 0 {
		data, err := sonic.Marshal(r.ParsedRouteGroups)
		if err != nil {
			return err
		}
		r.RouteGroupsJSON = bifrost.Ptr(string(data))
	} else {
		r.RouteGroupsJSON = nil
	}
	return nil
}

// AfterFind hook for TableRoutingRule to deserialize JSON fields
func (r *TableRoutingRule) AfterFind(tx *gorm.DB) error {
	if r.Fallbacks != nil && strings.TrimSpace(*r.Fallbacks) != "" {
		if err := sonic.Unmarshal([]byte(*r.Fallbacks), &r.ParsedFallbacks); err != nil {
			return err
		}
	}
	if r.Query != nil && strings.TrimSpace(*r.Query) != "" {
		if err := sonic.Unmarshal([]byte(*r.Query), &r.ParsedQuery); err != nil {
			return err
		}
	}
	if r.HealthPolicyJSON != nil && strings.TrimSpace(*r.HealthPolicyJSON) != "" {
		var hp HealthPolicy
		if err := sonic.Unmarshal([]byte(*r.HealthPolicyJSON), &hp); err != nil {
			return err
		}
		r.ParsedHealthPolicy = &hp
	}
	if r.RouteGroupsJSON != nil && strings.TrimSpace(*r.RouteGroupsJSON) != "" {
		if err := sonic.Unmarshal([]byte(*r.RouteGroupsJSON), &r.ParsedRouteGroups); err != nil {
			return err
		}
	}
	return nil
}

// TableRoutingTarget represents a weighted routing target for probabilistic routing.
// Multiple targets can be associated with a single routing rule; weights determine
// the probability of each target being selected and must sum to 1 across all targets in a rule.
// The composite (RuleID, Provider, Model, KeyID) is unique to prevent duplicate target configs.
type TableRoutingTarget struct {
	RuleID   string  `gorm:"type:varchar(255);not null;index;uniqueIndex:idx_routing_target_config" json:"-"`
	Provider *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_target_config" json:"provider,omitempty"` // nil = use incoming provider
	Model    *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_target_config" json:"model,omitempty"`    // nil = use incoming model
	KeyID    *string `gorm:"type:varchar(255);uniqueIndex:idx_routing_target_config" json:"key_id,omitempty"`   // nil = no key pin
	Weight   float64 `gorm:"not null;default:1" json:"weight"`                                                  // must sum to 1 across all targets in a rule
}

// TableName for TableRoutingTarget
func (TableRoutingTarget) TableName() string { return "routing_targets" }
