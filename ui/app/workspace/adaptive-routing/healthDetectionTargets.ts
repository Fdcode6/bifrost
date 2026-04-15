import type { HealthDetectionProbeState, HealthDetectionSupportStatus, HealthDetectionTarget } from "@/lib/types/routingRules";

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
			return "The target is enabled and waiting for an initial validation probe.";
		case "eligible":
			return "The target is enabled and eligible for background probing.";
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
