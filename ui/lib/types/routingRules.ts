/**
 * Routing Rules Type Definitions
 * Defines all TypeScript interfaces for routing rules feature
 */

import { RuleGroupType } from "react-querybuilder";

export interface RoutingTarget {
	provider?: string;
	model?: string;
	key_id?: string;
	weight: number;
}

export interface HealthPolicy {
	adaptive_v22_enabled?: boolean;
	adaptive_ranking_enabled?: boolean;
	failure_threshold: number;
	failure_window_seconds: number;
	cooldown_seconds: number;
	consecutive_failures: number;
	slow_threshold_ms: number;
	slow_window_size: number;
	slow_ratio_threshold: number;
	slow_recovery_seconds: number;
	request_deadline_ms: number;
	max_fallback_attempts?: number;
	soft_cooldown_multiplier: number;
	cooldown_backoff_factor: number;
	cooldown_max_seconds: number;
	half_open_probe: boolean;
	reputation_window_seconds?: number;
	min_qualified_success_rate?: number;
	min_active_targets?: number;
	primary_attempt_timeout_ms?: number;
	fast_rescue_elapsed_ms?: number;
	fast_channel_threshold_ms?: number;
	rescue_attempt_timeout_ms?: number;
	probation_probe_base_seconds?: number;
	probation_probe_max_seconds?: number;
	probation_recover_successes?: number;
}

export interface RouteGroupTarget {
	primary_attempt_timeout_ms?: number;
	provider: string;
	model: string;
	key_id?: string;
	weight: number;
}

export interface RouteGroup {
	last_resort?: boolean;
	name: string;
	retry_limit: number;
	fallback_only?: boolean;
	targets: RouteGroupTarget[];
}

export interface RouteGroupReference {
	rule_id: string;
	rule_name: string;
	group_name: string;
	group_index: number;
	fallback_only: boolean;
	retry_limit: number;
	health_level?: HealthSnapshot["health_level"];
}

export interface HealthSnapshot {
	key: string;
	status: "available" | "cooldown";
	health_level: "healthy" | "degraded" | "cooldown";
	route_groups?: RouteGroupReference[];
	failure_count: number;
	consecutive_failures: number;
	slow_count: number;
	sample_count: number;
	slow_ratio: number;
	p95_latency_ms?: number;
	cooldown_streak: number;
	last_slow_at?: string;
	last_slow_latency_ms?: number;
	last_outcome_kind?: "success" | "slow" | "soft_fail" | "hard_fail";
	half_open_in_flight?: boolean;
	cooldown_until?: string;
	last_failure_time?: string;
	last_failure_msg?: string;
	last_observed_at?: string;
	last_observed_request_type?: string;
	last_observation_source?: "passive" | "active";
}

export interface RuleHealthStatus {
	rule_id: string;
	rule_name: string;
	policy: HealthPolicy;
	targets: HealthSnapshot[];
}

export interface HealthStatusResponse {
	rules: RuleHealthStatus[];
	count: number;
}

export type AdaptiveDashboardWindow = "1h" | "today" | "24h" | "7d";
export type AdaptiveEngineMode = "adaptive_v22" | "adaptive_v21" | "grouped";
export type AdaptiveTargetState = "active" | "tripwire" | "probation" | "fatal" | "unavailable";
export type AdaptiveTierID = "L1" | "L2" | "L3";

export interface GetAdaptiveDashboardParams {
	window?: AdaptiveDashboardWindow;
	rule_id?: string;
	refresh?: boolean;
}

export interface AdaptiveDashboardRuleOption {
	rule_id: string;
	rule_name: string;
	description?: string;
	enabled: boolean;
	engine_mode: AdaptiveEngineMode;
	target_count: number;
}

export interface RescueWindowStats {
	attempts: number;
	fast: number;
}

export interface AdaptivePoolCounts {
	total: number;
	l1_total: number;
	l1_active: number;
	l1_tripwire: number;
	l1_probation: number;
	l1_fatal: number;
	l2_total: number;
	l2_ready: number;
	l2_restricted: number;
	l3_total: number;
	l3_ready: number;
	l3_restricted: number;
}

export interface AdaptiveRouteHopPreview {
	target_key: string;
	provider: string;
	model: string;
	key_id?: string;
	tier: AdaptiveTierID;
	official_cost_pct?: number | null;
	timeout_ms?: number;
	is_fast_rescue?: boolean;
	is_last_resort?: boolean;
	is_probation_probe?: boolean;
}

export interface AdaptiveRoutePreview {
	primary?: AdaptiveRouteHopPreview | null;
	fast_fail_continuation?: AdaptiveRouteHopPreview | null;
	timeout_rescue?: AdaptiveRouteHopPreview[];
	final_hop_fallback?: AdaptiveRouteHopPreview[];
	probation_probe_pending?: AdaptiveRouteHopPreview | null;
	final_hop_reserve_ms?: number;
}

export interface AdaptiveDashboardTargetMetrics {
	request_count: number;
	success_count: number;
	error_count: number;
	single_hop_success_rate?: number | null;
	avg_latency_ms?: number | null;
	p50_latency_ms?: number | null;
	p80_latency_ms?: number | null;
	p95_latency_ms?: number | null;
	revenue_usd: number;
	cost_usd: number;
	profit_usd: number;
	gross_margin?: number | null;
	prompt_tokens: number;
	completion_tokens: number;
	total_tokens: number;
	missing_cost_count: number;
	missing_tokens_count: number;
}

export interface AdaptiveDashboardTargetRow {
	target_key: string;
	provider: string;
	model: string;
	key_id?: string;
	group_name: string;
	group_index: number;
	tier: AdaptiveTierID;
	fallback_only: boolean;
	last_resort: boolean;
	weight: number;
	attempt_timeout_ms?: number;
	primary_attempt_timeout_ms?: number;
	input_cost_per_1m_usd?: number | null;
	output_cost_per_1m_usd?: number | null;
	official_cost_pct?: number | null;
	score: number;
	rank_label: string;
	rank_order: number;
	is_primary: boolean;
	state: AdaptiveTargetState;
	is_fast_rescue: boolean;
	min_active_promoted?: boolean;
	effective_success_rate: number;
	total_successes: number;
	p80_latency_ms?: number | null;
	short_tripwire_until?: string | null;
	fatal_until?: string | null;
	probation_next_probe_at?: string | null;
	probation_consec_success: number;
	probation_recover_successes: number;
	probation_probe_in_flight?: boolean;
	probation_probe_due?: boolean;
	rescue_10m_non_stream: RescueWindowStats;
	rescue_10m_stream: RescueWindowStats;
	rescue_10m_total: RescueWindowStats;
	last_outcome_kind?: string;
	last_outcome_at?: string | null;
	last_failure_msg?: string;
	last_failure_time?: string | null;
	last_real_access_at?: string | null;
	legacy_health_level?: string;
	legacy_cooldown_until?: string | null;
	legacy_consecutive_failures?: number;
	metrics: AdaptiveDashboardTargetMetrics;
}

export interface AdaptiveDashboardRuleRuntime {
	rule_id: string;
	rule_name: string;
	description?: string;
	enabled: boolean;
	engine_mode: AdaptiveEngineMode;
	policy?: HealthPolicy;
	effective_budget_ms: number;
	effective_max_attempts: number;
	final_hop_reserve_ms?: number;
	official_baseline_input_per_1m_usd?: number | null;
	official_baseline_output_per_1m_usd?: number | null;
	pool_counts: AdaptivePoolCounts;
	route_preview?: AdaptiveRoutePreview | null;
}

export interface AdaptiveDashboardTierSummary {
	tier: AdaptiveTierID;
	success_count: number;
	request_count: number;
	success_share: number;
	revenue_usd: number;
	cost_usd: number;
	profit_usd: number;
	gross_margin?: number | null;
	targets: AdaptiveDashboardTargetRow[];
}

export interface AdaptiveDashboardKPI {
	root_request_count: number;
	root_success_count: number;
	first_hop_success_count: number;
	rescued_count: number;
	root_success_rate?: number | null;
	first_hop_success_rate?: number | null;
	avg_latency_ms?: number | null;
	p50_latency_ms?: number | null;
	p80_latency_ms?: number | null;
	p95_latency_ms?: number | null;
	total_revenue_usd: number;
	total_cost_usd: number;
	total_profit_usd: number;
	gross_margin?: number | null;
	effective_official_cost_pct?: number | null;
	total_prompt_tokens: number;
	total_completion_tokens: number;
	total_tokens: number;
	missing_cost_count: number;
	missing_tokens_count: number;
	total_target_successes: number;
}

export interface AdaptiveDashboardResponse {
	generated_at: string;
	window: AdaptiveDashboardWindow;
	window_start: string;
	window_end: string;
	timezone: string;
	logs_connected: boolean;
	metrics_cached_at?: string | null;
	rules: AdaptiveDashboardRuleOption[];
	selected_rule?: AdaptiveDashboardRuleRuntime | null;
	kpi: AdaptiveDashboardKPI;
	tiers: AdaptiveDashboardTierSummary[];
}

export type HealthDetectionMode = "passive" | "hybrid";

export interface HealthDetectionConfigResponse {
	mode: HealthDetectionMode;
	idle_pause_minutes: number;
	active_health_probe_interval_seconds: number;
	active_health_probe_timeout_seconds: number;
	active_health_probe_max_concurrency: number;
	editable: boolean;
	read_only_reason?: string;
}

export type UpdateHealthDetectionConfigRequest = Omit<HealthDetectionConfigResponse, "editable" | "read_only_reason">;

export type HealthDetectionSupportStatus = "supported" | "unsupported";

export type HealthDetectionProbeState = "unsupported" | "off" | "pending_first_probe" | "eligible" | "paused_idle";

export interface HealthDetectionRuleHealthSummary {
	total_rule_count: number;
	degraded_rule_count?: number;
	cooldown_rule_count: number;
}

export interface HealthDetectionTarget {
	target_id: string;
	provider: string;
	model: string;
	key_id?: string;
	referenced_rule_ids: string[];
	referenced_rule_names: string[];
	route_groups: RouteGroupReference[];
	support_status: HealthDetectionSupportStatus;
	support_reason?: string;
	detection_enabled: boolean;
	probe_state: HealthDetectionProbeState;
	rule_health_summary: HealthDetectionRuleHealthSummary;
	last_real_access_at?: string;
	last_probe_at?: string;
	last_probe_result?: string;
	last_probe_error?: string;
	runtime_scope: string;
}

export interface HealthDetectionTargetsResponse {
	targets: HealthDetectionTarget[];
	count: number;
}

export interface UpdateHealthDetectionTargetRequest {
	detection_enabled: boolean;
}

export interface RoutingRule {
	id: string;
	name: string;
	description: string;
	cel_expression: string;
	targets: RoutingTarget[];
	fallbacks?: string[];
	scope: "global" | "team" | "customer" | "virtual_key";
	scope_id?: string;
	priority: number;
	enabled: boolean;
	query?: RuleGroupType;
	grouped_routing_enabled?: boolean;
	health_policy?: HealthPolicy;
	route_groups?: RouteGroup[];
	created_at: string;
	updated_at: string;
}

export interface CreateRoutingRuleRequest {
	name: string;
	description?: string;
	cel_expression?: string;
	targets: RoutingTarget[];
	fallbacks?: string[];
	scope: string;
	scope_id?: string;
	priority: number;
	enabled?: boolean;
	query?: RuleGroupType;
	grouped_routing_enabled?: boolean;
	health_policy?: HealthPolicy;
	route_groups?: RouteGroup[];
}

/** Partial update: only sent fields are applied; allows clearing fields by sending "" or []. */
export type UpdateRoutingRuleRequest = Partial<CreateRoutingRuleRequest>;

export interface GetRoutingRulesParams {
	limit?: number;
	offset?: number;
	search?: string;
}

export interface GetRoutingRulesResponse {
	rules: RoutingRule[];
	count: number;
	total_count: number;
	limit: number;
	offset: number;
}

export interface GetRoutingRuleResponse {
	rule: RoutingRule;
}

export interface RoutingTargetFormData {
	primary_attempt_timeout_ms?: number;
	provider: string;
	model: string;
	key_id: string;
	weight: number;
}

export interface RouteGroupFormData {
	last_resort?: boolean;
	name: string;
	retry_limit: number;
	fallback_only: boolean;
	targets: RoutingTargetFormData[];
}

export interface RoutingRuleFormData {
	id?: string;
	name: string;
	description: string;
	cel_expression: string;
	targets: RoutingTargetFormData[];
	fallbacks: string[];
	scope: string;
	scope_id: string;
	priority: number;
	enabled: boolean;
	query?: RuleGroupType;
	isDirty?: boolean;
	grouped_routing_enabled: boolean;
	health_policy: HealthPolicy;
	route_groups: RouteGroupFormData[];
}

export enum RoutingRuleScope {
	Global = "global",
	Team = "team",
	Customer = "customer",
	VirtualKey = "virtual_key",
}

export const ROUTING_RULE_SCOPES = [
	{ value: RoutingRuleScope.Global, label: "全局" },
	{ value: RoutingRuleScope.Team, label: "Team" },
	{ value: RoutingRuleScope.Customer, label: "Customer" },
	{ value: RoutingRuleScope.VirtualKey, label: "Virtual Key" },
];

export const DEFAULT_ROUTING_TARGET: RoutingTargetFormData = {
	provider: "",
	model: "",
	key_id: "",
	weight: 1,
};

export const DEFAULT_HEALTH_POLICY: HealthPolicy = {
	failure_threshold: 2,
	failure_window_seconds: 30,
	cooldown_seconds: 30,
	consecutive_failures: 0,
	slow_threshold_ms: 45000,
	slow_window_size: 10,
	slow_ratio_threshold: 0.5,
	slow_recovery_seconds: 60,
	request_deadline_ms: 0,
	soft_cooldown_multiplier: 2,
	cooldown_backoff_factor: 2,
	cooldown_max_seconds: 600,
	half_open_probe: true,
};

export const DEFAULT_ROUTE_GROUP: RouteGroupFormData = {
	name: "",
	retry_limit: 0,
	fallback_only: false,
	targets: [{ provider: "", model: "", key_id: "", weight: 1 }],
};

export const DEFAULT_ROUTING_RULE_FORM_DATA: RoutingRuleFormData = {
	name: "",
	description: "",
	cel_expression: "",
	targets: [DEFAULT_ROUTING_TARGET],
	fallbacks: [],
	scope: RoutingRuleScope.Global,
	scope_id: "",
	priority: 0,
	enabled: true,
	isDirty: false,
	grouped_routing_enabled: false,
	health_policy: { ...DEFAULT_HEALTH_POLICY },
	route_groups: [],
};
