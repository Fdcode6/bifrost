import { describe, expect, it } from "vitest";

import type { HealthDetectionTarget } from "@/lib/types/routingRules";

import {
	getHealthDetectionProbeStateDescription,
	getHealthDetectionProbeStateLabel,
	getHealthDetectionSupportStatusLabel,
	getHealthLevelBadgeClass,
	getHealthLevelDescription,
	getHealthLevelLabel,
	getRouteGroupLabel,
	getWorstRouteGroupHealthLevel,
	formatSlowRatio,
	isHealthDetectionTargetEditable,
} from "./healthDetectionTargets";

const baseTarget: HealthDetectionTarget = {
	target_id: "target-1",
	provider: "openai",
	model: "gpt-4.1",
	key_id: "relay-a",
	referenced_rule_ids: ["rule-a"],
	referenced_rule_names: ["Rule A"],
	route_groups: [
		{
			rule_id: "rule-a",
			rule_name: "Rule A",
			group_name: "Primary",
			group_index: 1,
			fallback_only: false,
			retry_limit: 0,
			health_level: "healthy",
		},
	],
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
			"The target is enabled and waiting for an initial liveness probe.",
		);
		expect(getHealthDetectionProbeStateDescription("eligible")).toBe("The target is enabled and eligible for background liveness probing.");
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
			}),
		).toBe(false);
	});

	it("formats three-state health levels", () => {
		expect(getHealthLevelLabel("healthy")).toBe("Healthy");
		expect(getHealthLevelLabel("degraded")).toBe("Degraded");
		expect(getHealthLevelLabel("cooldown")).toBe("Cooldown");
		expect(getHealthLevelDescription("degraded")).toContain("routed after healthy");
		expect(getHealthLevelBadgeClass("cooldown")).toContain("red");
	});

	it("returns the worst health level across route group references", () => {
		expect(getWorstRouteGroupHealthLevel(baseTarget.route_groups)).toBe("healthy");
		expect(
			getWorstRouteGroupHealthLevel([
				...baseTarget.route_groups,
				{
					rule_id: "rule-b",
					rule_name: "Rule B",
					group_name: "Fallback",
					group_index: 3,
					fallback_only: true,
					retry_limit: 2,
					health_level: "cooldown",
				},
			]),
		).toBe("cooldown");
	});

	it("formats route group labels with fallback markers", () => {
		expect(getRouteGroupLabel(baseTarget.route_groups[0])).toBe("G1: Primary");
		expect(
			getRouteGroupLabel({
				group_name: "Rescue",
				group_index: 3,
				fallback_only: true,
			}),
		).toBe("G3: Rescue · Fallback");
		expect(
			getRouteGroupLabel({
				group_name: "Fallback",
				group_index: 3,
				fallback_only: true,
			}),
		).toBe("G3: Fallback");
	});

	it("formats slow ratios for health snapshots", () => {
		expect(formatSlowRatio(0.6)).toBe("60%");
		expect(formatSlowRatio(undefined)).toBe("—");
	});
});
