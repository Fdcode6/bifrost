import type {
	HealthPolicy,
	HealthDetectionProbeState,
	HealthDetectionSupportStatus,
	HealthDetectionTarget,
	HealthSnapshot,
	RouteGroupReference,
} from "@/lib/types/routingRules";
import type { ModelProvider, ProviderPricingOverride } from "@/lib/types/config";

const TOKEN_PRICING_DENOMINATOR = 1_000_000;

export function getHealthDetectionSupportStatusLabel(status: HealthDetectionSupportStatus): string {
	return status === "supported" ? "支持" : "不支持";
}

export function getHealthDetectionProbeStateLabel(state: HealthDetectionProbeState): string {
	switch (state) {
		case "unsupported":
			return "不支持";
		case "off":
			return "关闭";
		case "pending_first_probe":
			return "等待首次探测";
		case "eligible":
			return "可探测";
		case "paused_idle":
			return "空闲暂停";
		default:
			return state;
	}
}

export function getHealthDetectionProbeStateDescription(state: HealthDetectionProbeState): string {
	switch (state) {
		case "unsupported":
			return "该目标可见，但不能执行后台存活探测。";
		case "off":
			return "该目标未启用后台存活探测。";
		case "pending_first_probe":
			return "该目标已启用，正在等待首次存活探测。";
		case "eligible":
			return "该目标可以执行后台存活探测。";
		case "paused_idle":
			return "该目标最近没有真实请求，存活探测已暂停。";
		default:
			return "";
	}
}

export function isHealthDetectionTargetEditable(target: Pick<HealthDetectionTarget, "support_status">): boolean {
	return target.support_status === "supported";
}

export function isHealthDetectionTargetUnused(
	target: Pick<HealthDetectionTarget, "referenced_rule_ids" | "route_groups" | "rule_health_summary">,
): boolean {
	return (
		(!target.referenced_rule_ids || target.referenced_rule_ids.length === 0) &&
		(!target.route_groups || target.route_groups.length === 0) &&
		(target.rule_health_summary?.total_rule_count ?? 0) === 0
	);
}

export function formatHealthDetectionTimestamp(value?: string): string {
	if (!value) {
		return "—";
	}

	const date = new Date(value);
	if (Number.isNaN(date.getTime())) {
		return "—";
	}

	return date.toLocaleString();
}

export function getHealthLevelLabel(level?: HealthSnapshot["health_level"]): string {
	switch (level) {
		case "cooldown":
			return "冷却中";
		case "degraded":
			return "降级";
		case "healthy":
		default:
			return "健康";
	}
}

export function getHealthLevelDescription(level?: HealthSnapshot["health_level"]): string {
	switch (level) {
		case "cooldown":
			return "Cooldown 结束前不会参与常规路由。";
		case "degraded":
			return "仍可使用，但会排在健康目标之后。";
		case "healthy":
		default:
			return "可正常参与路由。";
	}
}

export function getHealthLevelBadgeClass(level?: HealthSnapshot["health_level"]): string {
	switch (level) {
		case "cooldown":
			return "bg-red-100 text-red-700 dark:bg-red-950/40 dark:text-red-300";
		case "degraded":
			return "bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300";
		case "healthy":
		default:
			return "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400";
	}
}

export function getWorstRouteGroupHealthLevel(groups?: RouteGroupReference[]): HealthSnapshot["health_level"] {
	if (!groups || groups.length === 0) {
		return "healthy";
	}
	if (groups.some((group) => group.health_level === "cooldown")) {
		return "cooldown";
	}
	if (groups.some((group) => group.health_level === "degraded")) {
		return "degraded";
	}
	return "healthy";
}

export function getRouteGroupLabel(group: Pick<RouteGroupReference, "group_index" | "group_name" | "fallback_only">): string {
	const groupName = group.group_name || `Group ${group.group_index}`;
	const fallbackMarker = group.fallback_only && !groupName.toLowerCase().includes("fallback") ? " · Fallback" : "";
	return `G${group.group_index}: ${groupName}${fallbackMarker}`;
}

export function formatHealthMetric(value?: number, suffix = ""): string {
	if (value === undefined || value === null || Number.isNaN(value)) {
		return "—";
	}
	return `${value}${suffix}`;
}

export function formatSlowRatio(value?: number): string {
	if (value === undefined || value === null || Number.isNaN(value)) {
		return "—";
	}
	return `${Math.round(value * 100)}%`;
}

export function formatHealthPolicyLatencyThreshold(value?: number): string {
	if (value === undefined || value === null || Number.isNaN(value) || value <= 0) {
		return "未设置";
	}
	if (value >= 1000 && value % 1000 === 0) {
		return `${value / 1000} 秒`;
	}
	if (value >= 1000) {
		return `${(value / 1000).toFixed(1).replace(/\.0$/, "")} 秒`;
	}
	return `${value}ms`;
}

export function formatHealthPolicySeconds(value?: number): string {
	if (value === undefined || value === null || Number.isNaN(value)) {
		return "未设置";
	}
	if (value <= 0) {
		return "关闭";
	}
	return `${value} 秒`;
}

export function formatHealthPolicyRatioThreshold(value?: number): string {
	if (value === undefined || value === null || Number.isNaN(value) || value <= 0) {
		return "未设置";
	}
	if (value > 1) {
		return "关闭";
	}
	return `${Math.round(value * 100)}%`;
}

export function getFailurePolicySummary(policy: HealthPolicy): string {
	const consecutiveFailures = policy.consecutive_failures || policy.failure_threshold;
	return `故障降级：${policy.failure_threshold} 次失败 / ${policy.failure_window_seconds} 秒窗口 / 冷却 ${policy.cooldown_seconds} 秒 / 连续失败 ${consecutiveFailures} 次`;
}

export function getSlowPolicySummary(policy: HealthPolicy): string {
	const ratio = formatHealthPolicyRatioThreshold(policy.slow_ratio_threshold);
	const recovery = formatHealthPolicySeconds(policy.slow_recovery_seconds);
	const ratioText = ratio === "关闭" ? "慢请求比例降级关闭" : `慢请求 >= ${ratio} 降级`;
	const recoveryText = recovery === "关闭" ? "不使用最近慢请求保护" : `${recovery}后恢复观察`;
	return `慢请求降级：超过 ${formatHealthPolicyLatencyThreshold(policy.slow_threshold_ms)}算慢 / 最近 ${policy.slow_window_size} 次统计 / ${ratioText} / ${recoveryText}`;
}

function formatCompactPrice(value: number): string {
	if (!Number.isFinite(value)) {
		return "—";
	}
	return value.toFixed(6).replace(/\.?0+$/, "");
}

export function formatPerMillionTokenPrice(value?: number): string {
	if (value === undefined || value === null || Number.isNaN(value)) {
		return "—";
	}
	return `$${formatCompactPrice(value * TOKEN_PRICING_DENOMINATOR)}`;
}

function wildcardMatch(pattern: string, model: string): boolean {
	const parts = pattern.split("*");
	if (parts.length === 1) {
		return model === pattern;
	}

	let remaining = model;
	if (parts[0]) {
		if (!remaining.startsWith(parts[0])) {
			return false;
		}
		remaining = remaining.slice(parts[0].length);
	}

	for (let i = 1; i < parts.length - 1; i++) {
		const part = parts[i];
		if (!part) {
			continue;
		}
		const index = remaining.indexOf(part);
		if (index < 0) {
			return false;
		}
		remaining = remaining.slice(index + part.length);
	}

	const last = parts[parts.length - 1];
	return !last || remaining.endsWith(last);
}

function overrideMatchesModel(override: ProviderPricingOverride, model: string): boolean {
	switch (override.match_type) {
		case "exact":
			return override.model_pattern === model;
		case "wildcard":
			return wildcardMatch(override.model_pattern, model);
		case "contains":
			return model.toLowerCase().includes(override.model_pattern.toLowerCase());
		case "regex":
			try {
				return new RegExp(override.model_pattern).test(model);
			} catch {
				return false;
			}
		default:
			return false;
	}
}

function overrideAppliesToChatPricing(override: ProviderPricingOverride): boolean {
	if (!override.request_types || override.request_types.length === 0) {
		return true;
	}
	return override.request_types.includes("chat_completion") || override.request_types.includes("chat_completion_stream");
}

function overridePriority(override: ProviderPricingOverride): number {
	switch (override.match_type) {
		case "exact":
			return 0;
		case "wildcard":
			return 1;
		case "contains":
			return 2;
		case "regex":
			return 3;
		default:
			return 4;
	}
}

function literalChars(override: ProviderPricingOverride): number {
	return override.match_type === "wildcard" ? override.model_pattern.replaceAll("*", "").length : override.model_pattern.length;
}

function isBetterPricingOverride(candidate: ProviderPricingOverride, best?: ProviderPricingOverride): boolean {
	if (!best) {
		return true;
	}

	const candidatePriority = overridePriority(candidate);
	const bestPriority = overridePriority(best);
	if (candidatePriority !== bestPriority) {
		return candidatePriority < bestPriority;
	}

	const candidateHasRequestFilter = !!candidate.request_types?.length;
	const bestHasRequestFilter = !!best.request_types?.length;
	if (candidateHasRequestFilter !== bestHasRequestFilter) {
		return candidateHasRequestFilter;
	}

	const candidateLiteralChars = literalChars(candidate);
	const bestLiteralChars = literalChars(best);
	if (candidateLiteralChars !== bestLiteralChars) {
		return candidateLiteralChars > bestLiteralChars;
	}

	return false;
}

export function getProviderTokenPricingOverrideForModel(
	provider: Pick<ModelProvider, "pricing_overrides"> | undefined,
	model: string,
): ProviderPricingOverride | undefined {
	if (!provider?.pricing_overrides || !model) {
		return undefined;
	}

	let best: ProviderPricingOverride | undefined;
	for (const override of provider.pricing_overrides) {
		if (!overrideAppliesToChatPricing(override)) {
			continue;
		}
		if (!overrideMatchesModel(override, model)) {
			continue;
		}
		if (isBetterPricingOverride(override, best)) {
			best = override;
		}
	}
	return best;
}
