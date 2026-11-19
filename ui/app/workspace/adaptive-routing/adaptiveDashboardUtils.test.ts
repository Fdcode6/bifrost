import { describe, expect, it } from "vitest";
import type { AdaptiveDashboardTargetRow } from "@/lib/types/routingRules";
import {
	filterTargetRow,
	formatCountdown,
	formatDualMoney,
	formatLastEventSummary,
	formatLatencyMs,
	formatOfficialPct,
	formatTargetStateBadge,
} from "./adaptiveDashboardUtils";

function makeTarget(overrides: Partial<AdaptiveDashboardTargetRow> = {}): AdaptiveDashboardTargetRow {
	return {
		target_key: "storeapi-aistudio:gemini-3.1-pro-preview",
		provider: "storeapi-aistudio",
		model: "gemini-3.1-pro-preview",
		group_name: "L1",
		group_index: 0,
		tier: "L1",
		fallback_only: false,
		last_resort: false,
		weight: 1,
		attempt_timeout_ms: 22000,
		score: 0.1,
		rank_label: "#1",
		rank_order: 1,
		is_primary: true,
		state: "active",
		is_fast_rescue: false,
		effective_success_rate: 0.842,
		total_successes: 42,
		probation_consec_success: 0,
		probation_recover_successes: 2,
		rescue_10m_non_stream: { attempts: 10, fast: 9 },
		rescue_10m_stream: { attempts: 0, fast: 0 },
		rescue_10m_total: { attempts: 10, fast: 9 },
		metrics: {
			request_count: 100,
			success_count: 85,
			error_count: 15,
			single_hop_success_rate: 85,
			revenue_usd: 10,
			cost_usd: 1,
			profit_usd: 9,
			gross_margin: 0.9,
			prompt_tokens: 1000,
			completion_tokens: 500,
			total_tokens: 1500,
			missing_cost_count: 0,
			missing_tokens_count: 0,
		},
		...overrides,
	};
}

describe("adaptiveDashboardUtils", () => {
	it("formats dual money with fixed 6.8 RMB rate and supports RMB/USD primary swap", () => {
		expect(formatDualMoney(10, "rmb")).toEqual({ main: "¥68.00", sub: "$10.00" });
		expect(formatDualMoney(100, "rmb", true)).toEqual({ main: "+¥680.0", sub: "+$100.00" });
		expect(formatDualMoney(10, "usd")).toEqual({ main: "$10.00", sub: "≈ ¥68.00" });
		expect(formatDualMoney(-2.5, "rmb", true)).toEqual({ main: "-¥17.00", sub: "-$2.50" });
	});

	it("formats latency and official price percentage cleanly", () => {
		expect(formatLatencyMs(9600)).toBe("9.6s");
		expect(formatLatencyMs(420)).toBe("420ms");
		expect(formatLatencyMs(null)).toBe("—");
		expect(formatOfficialPct(2.21)).toBe("2.21%");
		expect(formatOfficialPct(100)).toBe("100.00%");
		expect(formatOfficialPct(null)).toBe("—");
	});

	it("formats countdowns and live state badges for V2.2 and legacy grouped rules", () => {
		const now = Date.parse("2026-11-19T12:00:00.000Z");
		expect(formatCountdown("2026-11-19T12:00:34.000Z", now)).toBe("34s");
		expect(formatCountdown("2026-11-19T12:03:12.000Z", now)).toBe("3m12s");

		expect(
			formatTargetStateBadge(
				makeTarget({ state: "tripwire", short_tripwire_until: "2026-11-19T12:00:34.000Z" }),
				"adaptive_v22",
				now,
			),
		).toEqual({ label: "60s 短熔断 · 剩 34s", tone: "warning" });

		expect(
			formatTargetStateBadge(
				makeTarget({
					state: "probation",
					probation_consec_success: 1,
					probation_recover_successes: 2,
					probation_next_probe_at: "2026-11-19T12:03:12.000Z",
				}),
				"adaptive_v22",
				now,
			),
		).toEqual({ label: "观察席 · 3m12s 后探测 (1/2)", tone: "info" });

		expect(
			formatTargetStateBadge(
				makeTarget({ legacy_health_level: "cooldown", legacy_cooldown_until: "2026-11-19T12:00:45.000Z" }),
				"grouped",
				now,
			),
		).toEqual({ label: "冷却中 · 剩 45s", tone: "error" });
	});

	it("formats recent event summary and filters rows correctly", () => {
		const now = Date.parse("2026-11-19T12:00:00.000Z");
		const row = makeTarget({
			state: "probation",
			probation_consec_success: 1,
			last_outcome_kind: "success",
			last_outcome_at: "2026-11-19T11:58:00.000Z",
		});
		expect(formatLastEventSummary(row, now).summary).toBe("2m 前成功 (连胜 1/2)");

		expect(filterTargetRow(row, "probation")).toBe(true);
		expect(filterTargetRow(row, "active")).toBe(false);
		expect(filterTargetRow(row, "used")).toBe(true);
		expect(filterTargetRow(makeTarget({ metrics: { ...row.metrics, request_count: 0 } }), "used")).toBe(false);
	});
});
