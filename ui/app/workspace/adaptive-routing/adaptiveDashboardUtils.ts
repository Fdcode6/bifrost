import type {
	AdaptiveDashboardTargetRow,
	AdaptiveDashboardWindow,
	AdaptiveEngineMode,
	AdaptiveTierID,
} from "@/lib/types/routingRules";

export const USD_TO_RMB_RATE = 6.8;

export type CurrencyDisplayMode = "rmb" | "usd";
export type AdaptiveTargetFilter = "all" | "active" | "tripwire" | "probation" | "used";

export interface DualMoneyFormatted {
	main: string;
	sub: string;
}

export interface TargetStatePresentation {
	label: string;
	tone: "success" | "warning" | "info" | "error" | "muted";
}

export interface LastEventPresentation {
	summary: string;
	detail: string;
	isWarning: boolean;
}

export interface TierMeta {
	title: string;
	badge: string;
	description: string;
}

export const ADAPTIVE_WINDOW_OPTIONS: { value: AdaptiveDashboardWindow; label: string; fullLabel: string }[] = [
	{ value: "1h", label: "近 1 小时", fullLabel: "近 1 小时" },
	{ value: "today", label: "今日", fullLabel: "今日 (00:00 至今)" },
	{ value: "24h", label: "近 24 小时", fullLabel: "近 24 小时" },
	{ value: "7d", label: "近 7 天", fullLabel: "近 7 天" },
];

export function getWindowFullLabel(window: AdaptiveDashboardWindow): string {
	return ADAPTIVE_WINDOW_OPTIONS.find((item) => item.value === window)?.fullLabel ?? "今日 (00:00 至今)";
}

export function formatDualMoney(
	usd: number | null | undefined,
	mode: CurrencyDisplayMode,
	signed = false,
): DualMoneyFormatted {
	if (usd === null || usd === undefined || !Number.isFinite(usd)) {
		return { main: "—", sub: "—" };
	}
	const rmb = usd * USD_TO_RMB_RATE;
	const sign = signed ? (usd > 0 ? "+" : usd < 0 ? "-" : "") : usd < 0 ? "-" : "";
	const absUsd = Math.abs(usd);
	const absRmb = Math.abs(rmb);

	const rmbDigits = absRmb >= 100 ? 1 : 2;
	const usdDigits = absUsd > 0 && absUsd < 0.01 ? 3 : 2;

	const rmbStr = `${sign}¥${absRmb.toFixed(rmbDigits)}`;
	const usdStr = `${sign}$${absUsd.toFixed(usdDigits)}`;

	if (mode === "rmb") {
		return { main: rmbStr, sub: usdStr };
	}
	return { main: usdStr, sub: `≈ ${rmbStr}` };
}

export function formatLatencyMs(ms: number | null | undefined): string {
	if (ms === null || ms === undefined || !Number.isFinite(ms) || ms <= 0) {
		return "—";
	}
	if (ms < 1000) {
		return `${Math.round(ms)}ms`;
	}
	return `${(ms / 1000).toFixed(1)}s`;
}

export function formatTimeoutLabel(ms: number | null | undefined, tier: AdaptiveTierID): string {
	if (tier === "L3" && (!ms || ms <= 0)) {
		return "末跳余量";
	}
	if (!ms || !Number.isFinite(ms) || ms <= 0) {
		return "默认";
	}
	const sec = ms / 1000;
	return Number.isInteger(sec) ? `${sec}s` : `${sec.toFixed(1)}s`;
}

export function formatOfficialPct(pct: number | null | undefined): string {
	if (pct === null || pct === undefined || !Number.isFinite(pct) || pct <= 0) {
		return "—";
	}
	return `${pct.toFixed(2)}%`;
}

export function formatPer1MPricePair(inputUsd?: number | null, outputUsd?: number | null): string {
	if (
		inputUsd === null ||
		inputUsd === undefined ||
		outputUsd === null ||
		outputUsd === undefined ||
		!Number.isFinite(inputUsd) ||
		!Number.isFinite(outputUsd)
	) {
		return "未配置单价";
	}
	const fmt = (v: number) => (v < 1 ? `$${v.toFixed(3)}` : `$${v.toFixed(2)}`);
	return `${fmt(inputUsd)} / ${fmt(outputUsd)}`;
}

export function formatCountdown(iso: string | null | undefined, nowMs: number): string {
	if (!iso) {
		return "";
	}
	const targetMs = Date.parse(iso);
	if (Number.isNaN(targetMs)) {
		return "";
	}
	const diffSec = Math.ceil((targetMs - nowMs) / 1000);
	if (diffSec <= 0) {
		return "即将";
	}
	if (diffSec < 60) {
		return `${diffSec}s`;
	}
	const mins = Math.floor(diffSec / 60);
	const secs = diffSec % 60;
	if (mins < 60) {
		return secs > 0 ? `${mins}m${secs}s` : `${mins}m`;
	}
	const hrs = Math.floor(mins / 60);
	const remMins = mins % 60;
	return remMins > 0 ? `${hrs}h${remMins}m` : `${hrs}h`;
}

export function formatRelativeTime(iso: string | null | undefined, nowMs: number): string {
	if (!iso) {
		return "";
	}
	const ts = Date.parse(iso);
	if (Number.isNaN(ts)) {
		return "";
	}
	const diffSec = Math.max(0, Math.floor((nowMs - ts) / 1000));
	if (diffSec < 60) {
		return `${diffSec}s 前`;
	}
	const mins = Math.floor(diffSec / 60);
	if (mins < 60) {
		return `${mins}m 前`;
	}
	const hrs = Math.floor(mins / 60);
	if (hrs < 24) {
		return `${hrs}h 前`;
	}
	const days = Math.floor(hrs / 24);
	return `${days}d 前`;
}

export function formatTargetStateBadge(
	row: AdaptiveDashboardTargetRow,
	engineMode: AdaptiveEngineMode,
	nowMs: number,
): TargetStatePresentation {
	if (row.state === "unavailable") {
		return { label: "渠道不可用", tone: "muted" };
	}

	if (engineMode === "grouped") {
		if (row.legacy_health_level === "cooldown") {
			const remain = formatCountdown(row.legacy_cooldown_until, nowMs);
			return { label: remain ? `冷却中 · 剩 ${remain}` : "冷却中", tone: "error" };
		}
		if (row.legacy_health_level === "degraded") {
			return { label: "慢速降级", tone: "warning" };
		}
		return { label: row.is_primary ? "健康在岗" : "健康待命", tone: "success" };
	}

	if (row.state === "fatal") {
		const remain = formatCountdown(row.fatal_until, nowMs);
		return { label: remain ? `致命隔离 · 剩 ${remain}` : "致命隔离", tone: "error" };
	}

	if (row.state === "tripwire") {
		const remain = formatCountdown(row.short_tripwire_until, nowMs);
		return { label: remain ? `60s 短熔断 · 剩 ${remain}` : "短熔断中", tone: "warning" };
	}

	if (row.state === "probation") {
		const recoverNeed = row.probation_recover_successes || 2;
		const streak = `${row.probation_consec_success}/${recoverNeed}`;
		if (row.tier === "L2") {
			return { label: `后备受限 · 观察中 (${streak})`, tone: "info" };
		}
		if (row.probation_probe_in_flight) {
			return { label: `观察席 · 探测进行中 (${streak})`, tone: "info" };
		}
		const remain = formatCountdown(row.probation_next_probe_at, nowMs);
		if (row.probation_probe_due || remain === "即将") {
			return { label: `观察席 · 待下一单探测 (${streak})`, tone: "info" };
		}
		if (remain) {
			return { label: `观察席 · ${remain} 后探测 (${streak})`, tone: "info" };
		}
		return { label: `观察席 (${streak})`, tone: "info" };
	}

	if (row.tier === "L2") {
		return { label: "后备待命", tone: "success" };
	}
	if (row.tier === "L3") {
		return { label: "底牌待命", tone: "success" };
	}
	if (row.min_active_promoted) {
		return { label: "保底在岗", tone: "warning" };
	}
	return { label: "主力在岗", tone: "success" };
}

export function formatLastEventSummary(row: AdaptiveDashboardTargetRow, nowMs: number): LastEventPresentation {
	const recoverNeed = row.probation_recover_successes || 2;
	const outcomeAt = row.last_outcome_at || row.last_failure_time || row.last_real_access_at;
	const rel = formatRelativeTime(outcomeAt, nowMs);

	if (row.last_outcome_kind === "success" || row.last_outcome_kind === "slow") {
		const suffix =
			row.state === "probation" && row.probation_consec_success > 0
				? ` (连胜 ${row.probation_consec_success}/${recoverNeed})`
				: row.last_outcome_kind === "slow"
					? " (慢成功)"
					: "";
		return {
			summary: `${rel ? `${rel}` : "最近"}成功${suffix}`,
			detail: "",
			isWarning: false,
		};
	}

	if (
		row.last_outcome_kind === "soft_fail" ||
		row.last_outcome_kind === "hard_fail" ||
		row.last_outcome_kind === "fatal_channel" ||
		row.last_failure_msg
	) {
		const failRel = formatRelativeTime(row.last_failure_time || outcomeAt, nowMs);
		const label =
			row.last_outcome_kind === "fatal_channel"
				? "致命报错"
				: row.state === "tripwire"
					? "连续超时"
					: "最近失败";
		return {
			summary: `${failRel ? `${failRel} ` : ""}${label}`,
			detail: row.last_failure_msg || "",
			isWarning: true,
		};
	}

	if (row.last_real_access_at) {
		return {
			summary: `${formatRelativeTime(row.last_real_access_at, nowMs)} 有访问`,
			detail: "",
			isWarning: false,
		};
	}

	return {
		summary: "暂无实时记录",
		detail: "",
		isWarning: false,
	};
}

export function getTierMeta(tier: AdaptiveTierID, engineMode: AdaptiveEngineMode, ruleName: string): TierMeta {
	if (engineMode === "grouped") {
		if (tier === "L2") {
			return {
				title: "备用回退分组（fallback_only）",
				badge: "仅主分组不可用时启用",
				description: "当默认分组进入冷却或全部失败时自动回退至该层。",
			};
		}
		if (tier === "L3") {
			return {
				title: "终极兜底分组（last_resort）",
				badge: "末跳兜底",
				description: "排在所有分组之后，仅用于最后兜底。",
			};
		}
		return {
			title: `默认分组 · ${ruleName}（传统分组健康路由）`,
			badge: "非 V2.2 规则",
			description: "该规则未启用 V2.2 自适应排名，按传统权重与健康熔断运行。",
		};
	}

	switch (tier) {
		case "L1":
			return {
				title: "L1 · 赚钱主力池（日常折扣渠道）",
				badge: "日常首跳主力",
				description:
					"合格达标渠道按综合性价比自动排位，#1 吃满首跳；跌破合格线自动退入观察席，每 5~15 分钟单并发真单体检。",
			};
		case "L2":
			return {
				title: "L2 · 薄利后备池（fallback_only）",
				badge: "平时 0 首跳 · 仅救场",
				description: "仅在 L1 渠道超时（≥15s）或当场全败时作为快枪手顶上，绝不抢占正常首跳流量。",
			};
		case "L3":
			return {
				title: "L3 · 100% 原价终极救命底牌（last_resort）",
				badge: "仅末跳救命",
				description: "严格排在所有折扣与后备渠道之后，只有前面全挂且只剩最后预留窗口时才出场兜底。",
			};
	}
}

export function filterTargetRow(row: AdaptiveDashboardTargetRow, filter: AdaptiveTargetFilter): boolean {
	switch (filter) {
		case "active":
			return row.state === "active";
		case "tripwire":
			return row.state === "tripwire";
		case "probation":
			return row.state === "probation" || row.state === "fatal" || row.state === "unavailable";
		case "used":
			return row.metrics.request_count > 0;
		default:
			return true;
	}
}

export function getRateToneClass(pct: number): string {
	if (pct >= 85) return "text-emerald-600 dark:text-emerald-400";
	if (pct >= 70) return "text-sky-600 dark:text-sky-400";
	if (pct >= 45) return "text-amber-600 dark:text-amber-400";
	return "text-rose-600 dark:text-rose-400";
}

export function getRateBarClass(pct: number): string {
	if (pct >= 85) return "bg-emerald-500";
	if (pct >= 70) return "bg-sky-500";
	if (pct >= 45) return "bg-amber-500";
	return "bg-rose-500";
}

export function getEngineBadge(engineMode: AdaptiveEngineMode): { label: string; tone: "success" | "info" | "muted" } {
	switch (engineMode) {
		case "adaptive_v22":
			return { label: "V2.2 运行中", tone: "success" };
		case "adaptive_v21":
			return { label: "V2.1 运行中", tone: "info" };
		default:
			return { label: "传统分组路由", tone: "muted" };
	}
}
