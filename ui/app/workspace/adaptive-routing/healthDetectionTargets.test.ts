import { describe, expect, it } from "vitest";

import type { HealthDetectionTarget } from "@/lib/types/routingRules";

import {
	formatHealthPolicyLatencyThreshold,
	formatHealthPolicyRatioThreshold,
	formatHealthPolicySeconds,
	formatPerMillionTokenPrice,
	getHealthDetectionProbeStateDescription,
	getHealthDetectionProbeStateLabel,
	getHealthDetectionSupportStatusLabel,
	getFailurePolicySummary,
	getHealthLevelBadgeClass,
	getHealthLevelDescription,
	getHealthLevelLabel,
	getSlowPolicySummary,
	getProviderTokenPricingOverrideForModel,
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
		expect(getHealthDetectionSupportStatusLabel("supported")).toBe("支持");
		expect(getHealthDetectionSupportStatusLabel("unsupported")).toBe("不支持");
	});

	it("returns readable labels for probe state", () => {
		expect(getHealthDetectionProbeStateLabel("unsupported")).toBe("不支持");
		expect(getHealthDetectionProbeStateLabel("off")).toBe("关闭");
		expect(getHealthDetectionProbeStateLabel("pending_first_probe")).toBe("等待首次探测");
		expect(getHealthDetectionProbeStateLabel("eligible")).toBe("可探测");
		expect(getHealthDetectionProbeStateLabel("paused_idle")).toBe("空闲暂停");
	});

	it("returns probe state descriptions that match the design copy", () => {
		expect(getHealthDetectionProbeStateDescription("unsupported")).toBe("该目标可见，但不能执行后台存活探测。");
		expect(getHealthDetectionProbeStateDescription("off")).toBe("该目标未启用后台存活探测。");
		expect(getHealthDetectionProbeStateDescription("pending_first_probe")).toBe("该目标已启用，正在等待首次存活探测。");
		expect(getHealthDetectionProbeStateDescription("eligible")).toBe("该目标可以执行后台存活探测。");
		expect(getHealthDetectionProbeStateDescription("paused_idle")).toBe("该目标最近没有真实请求，存活探测已暂停。");
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
		expect(getHealthLevelLabel("healthy")).toBe("健康");
		expect(getHealthLevelLabel("degraded")).toBe("降级");
		expect(getHealthLevelLabel("cooldown")).toBe("冷却中");
		expect(getHealthLevelDescription("degraded")).toContain("排在健康目标之后");
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

	it("formats health policy thresholds for rule summaries", () => {
		expect(formatHealthPolicyLatencyThreshold(45000)).toBe("45 秒");
		expect(formatHealthPolicyLatencyThreshold(1250)).toBe("1.3 秒");
		expect(formatHealthPolicyLatencyThreshold(800)).toBe("800ms");
		expect(formatHealthPolicySeconds(0)).toBe("关闭");
		expect(formatHealthPolicyRatioThreshold(0.5)).toBe("50%");
		expect(formatHealthPolicyRatioThreshold(999)).toBe("关闭");
	});

	it("builds readable failure and slow policy summaries", () => {
		const policy = {
			failure_threshold: 5,
			failure_window_seconds: 120,
			cooldown_seconds: 120,
			consecutive_failures: 0,
			slow_threshold_ms: 45000,
			slow_window_size: 10,
			slow_ratio_threshold: 0.5,
			slow_recovery_seconds: 60,
			request_deadline_ms: 0,
			soft_cooldown_multiplier: 0.5,
			cooldown_backoff_factor: 2,
			cooldown_max_seconds: 600,
			half_open_probe: true,
		};

		expect(getFailurePolicySummary(policy)).toBe("故障降级：5 次失败 / 120 秒窗口 / 冷却 120 秒 / 连续失败 5 次");
		expect(getSlowPolicySummary(policy)).toBe("慢请求降级：超过 45 秒算慢 / 最近 10 次统计 / 慢请求 >= 50% 降级 / 60 秒后恢复观察");
	});

	it("formats token pricing in per-million units", () => {
		expect(formatPerMillionTokenPrice(0.00000125)).toBe("$1.25");
		expect(formatPerMillionTokenPrice(undefined)).toBe("—");
	});

	it("selects the best matching provider pricing override for a model", () => {
		const override = getProviderTokenPricingOverrideForModel(
			{
				pricing_overrides: [
					{
						model_pattern: "gemini-*",
						match_type: "wildcard",
						input_cost_per_token: 0.000002,
					},
					{
						model_pattern: "gemini-2.5-pro*",
						match_type: "wildcard",
						request_types: ["chat_completion", "chat_completion_stream"],
						input_cost_per_token: 0.00000125,
						output_cost_per_token: 0.00001,
					},
				],
			},
			"gemini-2.5-pro-preview",
		);

		expect(override?.input_cost_per_token).toBe(0.00000125);
		expect(override?.output_cost_per_token).toBe(0.00001);
	});

	it("ignores pricing overrides that are not for chat requests", () => {
		expect(
			getProviderTokenPricingOverrideForModel(
				{
					pricing_overrides: [
						{
							model_pattern: "gemini-2.5-pro*",
							match_type: "wildcard",
							request_types: ["embedding"],
							input_cost_per_token: 0.00000125,
						},
					],
				},
				"gemini-2.5-pro-preview",
			),
		).toBeUndefined();
	});

	it("matches contains pricing overrides case-insensitively", () => {
		const override = getProviderTokenPricingOverrideForModel(
			{
				pricing_overrides: [
					{
						model_pattern: "GEMINI",
						match_type: "contains",
						request_types: ["chat_completion", "chat_completion_stream"],
						input_cost_per_token: 0.000002,
					},
				],
			},
			"gemini-3.1-pro-preview-thinking-medium",
		);

		expect(override?.input_cost_per_token).toBe(0.000002);
	});
});
