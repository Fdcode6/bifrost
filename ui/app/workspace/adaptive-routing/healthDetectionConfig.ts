import type { HealthDetectionConfigResponse, HealthDetectionMode, UpdateHealthDetectionConfigRequest } from "@/lib/types/routingRules";

export interface HealthDetectionFormState extends HealthDetectionConfigResponse {
	read_only_reason: string;
}

export function createHealthDetectionFormState(config: HealthDetectionConfigResponse): HealthDetectionFormState {
	return {
		...config,
		read_only_reason: config.read_only_reason ?? "",
	};
}

export function buildHealthDetectionUpdatePayload(form: HealthDetectionFormState): UpdateHealthDetectionConfigRequest {
	return {
		mode: form.mode,
		active_health_probe_interval_seconds: form.active_health_probe_interval_seconds,
		active_health_probe_passive_freshness_seconds: form.active_health_probe_passive_freshness_seconds,
		active_health_probe_timeout_seconds: form.active_health_probe_timeout_seconds,
		active_health_probe_max_concurrency: form.active_health_probe_max_concurrency,
	};
}

export function getDetectionModeLabel(mode: HealthDetectionMode): string {
	return mode === "hybrid" ? "Hybrid (Passive + Active)" : "Passive only";
}
