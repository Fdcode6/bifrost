import { describe, expect, it } from "vitest";

import type { HealthDetectionTarget } from "@/lib/types/routingRules";

import {
	getHealthDetectionProbeStateDescription,
	getHealthDetectionProbeStateLabel,
	getHealthDetectionSupportStatusLabel,
	isHealthDetectionTargetEditable,
} from "./healthDetectionTargets";

const baseTarget: HealthDetectionTarget = {
	target_id: "target-1",
	provider: "openai",
	model: "gpt-4.1",
	key_id: "relay-a",
	referenced_rule_ids: ["rule-a"],
	referenced_rule_names: ["Rule A"],
	support_status: "supported",
	detection_enabled: false,
	probe_state: "off",
	rule_health_summary: {
		total_rule_count: 1,
		cooldown_rule_count: 0,
	},
	runtime_scope: "node_local",
};

describe("healthDetectionTargets helpers", () => {
	it("returns readable labels for support status", () => {
		expect(getHealthDetectionSupportStatusLabel("supported")).toBe("Supported");
		expect(getHealthDetectionSupportStatusLabel("unsupported")).toBe("Unsupported");
	});

	it("returns readable labels for probe state", () => {
		expect(getHealthDetectionProbeStateLabel("unsupported")).toBe("Unsupported");
		expect(getHealthDetectionProbeStateLabel("off")).toBe("Off");
		expect(getHealthDetectionProbeStateLabel("pending_first_probe")).toBe("Pending First Probe");
		expect(getHealthDetectionProbeStateLabel("eligible")).toBe("Eligible");
		expect(getHealthDetectionProbeStateLabel("paused_idle")).toBe("Paused (Idle)");
	});

	it("returns probe state descriptions that match the design copy", () => {
		expect(getHealthDetectionProbeStateDescription("unsupported")).toBe("This target is visible but cannot be enrolled in active probing.");
		expect(getHealthDetectionProbeStateDescription("off")).toBe("Active probing is turned off for this target.");
		expect(getHealthDetectionProbeStateDescription("pending_first_probe")).toBe(
			"The target is enabled and waiting for an initial validation probe.",
		);
		expect(getHealthDetectionProbeStateDescription("eligible")).toBe("The target is enabled and eligible for background probing.");
		expect(getHealthDetectionProbeStateDescription("paused_idle")).toBe(
			"Background probing is paused because this target has not received recent real traffic.",
		);
	});

	it("treats unsupported targets as read-only rows", () => {
		expect(isHealthDetectionTargetEditable(baseTarget)).toBe(true);
		expect(
			isHealthDetectionTargetEditable({
				...baseTarget,
				support_status: "unsupported",
				probe_state: "unsupported",
			}),
		).toBe(false);
	});
});
