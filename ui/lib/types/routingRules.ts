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
	failure_threshold: number;
	failure_window_seconds: number;
	cooldown_seconds: number;
	consecutive_failures?: number;
}

export interface RouteGroupTarget {
	provider: string;
	model: string;
	key_id?: string;
	weight: number;
}

export interface RouteGroup {
	name: string;
	retry_limit: number;
	targets: RouteGroupTarget[];
}

export interface HealthSnapshot {
	key: string;
	status: "available" | "cooldown";
	failure_count: number;
	consecutive_failures: number;
	cooldown_until?: string;
	last_failure_time?: string;
	last_failure_msg?: string;
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
	provider: string;
	model: string;
	key_id: string;
	weight: number;
}

export interface RouteGroupFormData {
	name: string;
	retry_limit: number;
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
	{ value: RoutingRuleScope.Global, label: "Global" },
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
	consecutive_failures: 2,
};

export const DEFAULT_ROUTE_GROUP: RouteGroupFormData = {
	name: "",
	retry_limit: 0,
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
