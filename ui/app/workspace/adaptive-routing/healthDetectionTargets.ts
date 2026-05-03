import type {
	HealthDetectionProbeState,
	HealthDetectionSupportStatus,
	HealthDetectionTarget,
	HealthSnapshot,
	RouteGroupReference,
} from "@/lib/types/routingRules";

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
