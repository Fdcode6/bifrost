import type {
	HealthDetectionProbeState,
	HealthDetectionSupportStatus,
	HealthDetectionTarget,
	HealthSnapshot,
} from "@/lib/types/routingRules";

export function getHealthDetectionSupportStatusLabel(status: HealthDetectionSupportStatus): string {
	return status === "supported" ? "Supported" : "Unsupported";
}

export function getHealthDetectionProbeStateLabel(state: HealthDetectionProbeState): string {
	switch (state) {
		case "unsupported":
			return "Unsupported";
		case "off":
			return "Off";
		case "pending_first_probe":
			return "Pending First Probe";
		case "eligible":
			return "Eligible";
		case "paused_idle":
			return "Paused (Idle)";
		default:
			return state;
	}
}

export function getHealthDetectionProbeStateDescription(state: HealthDetectionProbeState): string {
	switch (state) {
		case "unsupported":
			return "This target is visible but cannot be enrolled in active probing.";
		case "off":
			return "Active probing is turned off for this target.";
		case "pending_first_probe":
			return "The target is enabled and waiting for an initial liveness probe.";
		case "eligible":
			return "The target is enabled and eligible for background liveness probing.";
		case "paused_idle":
			return "Background probing is paused because this target has not received recent real traffic.";
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
			return "Cooldown";
		case "degraded":
			return "Degraded";
		case "healthy":
		default:
			return "Healthy";
	}
}

export function getHealthLevelDescription(level?: HealthSnapshot["health_level"]): string {
	switch (level) {
		case "cooldown":
			return "Unavailable until cooldown expires.";
		case "degraded":
			return "Still usable, but routed after healthy regular targets.";
		case "healthy":
		default:
			return "Eligible for normal routing.";
	}
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
