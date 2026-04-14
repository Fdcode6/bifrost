import { describe, expect, it } from "vitest";

import {
	buildHealthDetectionUpdatePayload,
	createHealthDetectionFormState,
	getDetectionModeLabel,
	type HealthDetectionFormState,
} from "./healthDetectionConfig";

describe("healthDetectionConfig helpers", () => {
	it("creates editable form state from API response", () => {
		const form = createHealthDetectionFormState({
			mode: "hybrid",
			active_health_probe_interval_seconds: 18,
			active_health_probe_passive_freshness_seconds: 42,
			active_health_probe_timeout_seconds: 6,
			active_health_probe_max_concurrency: 7,
			editable: true,
		});

		expect(form.mode).toBe("hybrid");
		expect(form.active_health_probe_interval_seconds).toBe(18);
		expect(form.active_health_probe_passive_freshness_seconds).toBe(42);
		expect(form.active_health_probe_timeout_seconds).toBe(6);
		expect(form.active_health_probe_max_concurrency).toBe(7);
		expect(form.editable).toBe(true);
		expect(form.read_only_reason).toBe("");
	});

	it("normalizes missing read-only reason to an empty string", () => {
		const form = createHealthDetectionFormState({
			mode: "passive",
			active_health_probe_interval_seconds: 15,
			active_health_probe_passive_freshness_seconds: 30,
			active_health_probe_timeout_seconds: 5,
			active_health_probe_max_concurrency: 4,
			editable: false,
		});

		expect(form.read_only_reason).toBe("");
	});

	it("builds the update payload without response-only fields", () => {
		const payload = buildHealthDetectionUpdatePayload({
			mode: "hybrid",
			active_health_probe_interval_seconds: 20,
			active_health_probe_passive_freshness_seconds: 40,
			active_health_probe_timeout_seconds: 5,
			active_health_probe_max_concurrency: 6,
			editable: false,
			read_only_reason: "managed elsewhere",
		} satisfies HealthDetectionFormState);

		expect(payload).toEqual({
			mode: "hybrid",
			active_health_probe_interval_seconds: 20,
			active_health_probe_passive_freshness_seconds: 40,
			active_health_probe_timeout_seconds: 5,
			active_health_probe_max_concurrency: 6,
		});
	});

	it("returns readable labels for detection modes", () => {
		expect(getDetectionModeLabel("passive")).toBe("Passive only");
		expect(getDetectionModeLabel("hybrid")).toBe("Hybrid (Passive + Active)");
	});
});
