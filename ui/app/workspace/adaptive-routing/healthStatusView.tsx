"use client";

import { useEffect, useMemo, useState } from "react";
import {
	AlertCircle,
	ArrowRight,
	RefreshCw,
	ShieldAlert,
	Zap,
} from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import {
	useGetAdaptiveDashboardQuery,
	useLazyGetAdaptiveDashboardQuery,
} from "@/lib/store";
import type {
	AdaptiveDashboardTargetRow,
	AdaptiveDashboardWindow,
	AdaptiveEngineMode,
} from "@/lib/types/routingRules";
import { cn } from "@/lib/utils";

import {
	ADAPTIVE_WINDOW_OPTIONS,
	type AdaptiveTargetFilter,
	type CurrencyDisplayMode,
	USD_TO_RMB_RATE,
	filterTargetRow,
	formatDualMoney,
	formatLastEventSummary,
	formatLatencyMs,
	formatOfficialPct,
	formatPer1MPricePair,
	formatTargetStateBadge,
	formatTimeoutLabel,
	getEngineBadge,
	getRateBarClass,
	getRateToneClass,
	getTierMeta,
	getWindowFullLabel,
} from "./adaptiveDashboardUtils";

const POLL_INTERVAL_MS = 10_000;

function pillClass(tone: "success" | "warning" | "info" | "error" | "muted") {
	switch (tone) {
		case "success":
			return "border border-emerald-500/30 bg-emerald-500/12 text-emerald-700 dark:text-emerald-300";
		case "warning":
			return "border border-amber-500/35 bg-amber-500/15 text-amber-700 dark:text-amber-300";
		case "info":
			return "border border-sky-500/30 bg-sky-500/12 text-sky-700 dark:text-sky-300";
		case "error":
			return "border border-rose-500/30 bg-rose-500/12 text-rose-700 dark:text-rose-300";
		default:
			return "border border-border bg-muted/60 text-muted-foreground";
	}
}

function dotClass(tone: "success" | "warning" | "info" | "error" | "muted") {
	switch (tone) {
		case "success":
			return "bg-emerald-500";
		case "warning":
			return "bg-amber-500";
		case "info":
			return "bg-sky-500";
		case "error":
			return "bg-rose-500";
		default:
			return "bg-muted-foreground";
	}
}

function formatNumber(n: number): string {
	return new Intl.NumberFormat("zh-CN").format(n);
}

function formatPolicyMsToSec(ms?: number): string {
	if (!ms || ms <= 0) return "—";
	const sec = ms / 1000;
	return Number.isInteger(sec) ? `${sec}s` : `${sec.toFixed(1)}s`;
}

export default function HealthStatusView() {
	const [windowRange, setWindowRange] = useState<AdaptiveDashboardWindow>("today");
	const [selectedRuleId, setSelectedRuleId] = useState<string>("");
	const [currencyMode, setCurrencyMode] = useState<CurrencyDisplayMode>("rmb");
	const [targetFilter, setTargetFilter] = useState<AdaptiveTargetFilter>("all");
	const [nowMs, setNowMs] = useState<number>(() => Date.now());
	const [justRefreshed, setJustRefreshed] = useState(false);

	const queryArgs = useMemo(
		() => ({
			window: windowRange,
			...(selectedRuleId ? { rule_id: selectedRuleId } : {}),
		}),
		[windowRange, selectedRuleId],
	);

	const {
		data: polledData,
		isLoading,
		isFetching,
		error,
		refetch,
	} = useGetAdaptiveDashboardQuery(queryArgs, {
		pollingInterval: POLL_INTERVAL_MS,
		refetchOnFocus: true,
	});

	const [triggerForceRefresh, { data: forcedData, isFetching: isForceRefreshing }] =
		useLazyGetAdaptiveDashboardQuery();

	// Prefer whichever snapshot has the newer generated_at timestamp
	const dashboard = useMemo(() => {
		if (!forcedData) return polledData;
		if (!polledData) return forcedData;
		const forcedTs = Date.parse(forcedData.generated_at || "");
		const polledTs = Date.parse(polledData.generated_at || "");
		if (
			forcedData.window === windowRange &&
			(!selectedRuleId || forcedData.selected_rule?.rule_id === selectedRuleId) &&
			forcedTs >= polledTs
		) {
			return forcedData;
		}
		return polledData;
	}, [polledData, forcedData, windowRange, selectedRuleId]);

	// Keep countdown timers ticking smoothly between 10s polls
	useEffect(() => {
		const timer = setInterval(() => {
			setNowMs(Date.now());
		}, 1000);
		return () => clearInterval(timer);
	}, []);

	// Sync local selectedRuleId if currently empty and backend picked a default rule
	useEffect(() => {
		if (!selectedRuleId && dashboard?.selected_rule?.rule_id) {
			setSelectedRuleId(dashboard.selected_rule.rule_id);
		}
	}, [dashboard?.selected_rule?.rule_id, selectedRuleId]);

	const handleManualRefresh = async () => {
		try {
			await triggerForceRefresh({
				window: windowRange,
				...(selectedRuleId ? { rule_id: selectedRuleId } : {}),
				refresh: true,
			}).unwrap();
			setNowMs(Date.now());
			setJustRefreshed(true);
			setTimeout(() => setJustRefreshed(false), 1800);
		} catch {
			refetch();
		}
	};

	const selectedRule = dashboard?.selected_rule ?? null;
	const engineMode: AdaptiveEngineMode = selectedRule?.engine_mode ?? "grouped";
	const isAdaptive = engineMode === "adaptive_v22" || engineMode === "adaptive_v21";
	const engineBadge = getEngineBadge(engineMode);
	const tiers = useMemo(() => dashboard?.tiers ?? [], [dashboard?.tiers]);
	const kpi = dashboard?.kpi;

	const allRows = useMemo<AdaptiveDashboardTargetRow[]>(
		() => tiers.flatMap((t) => t.targets),
		[tiers],
	);

	const primaryRow = useMemo(() => {
		return allRows.find((r) => r.is_primary) ?? allRows[0] ?? null;
	}, [allRows]);

	const filterCounts = useMemo(() => {
		let active = 0;
		let tripwire = 0;
		let probation = 0;
		let used = 0;
		for (const row of allRows) {
			if (filterTargetRow(row, "active")) active++;
			if (filterTargetRow(row, "tripwire")) tripwire++;
			if (filterTargetRow(row, "probation")) probation++;
			if (filterTargetRow(row, "used")) used++;
		}
		return { all: allRows.length, active, tripwire, probation, used };
	}, [allRows]);

	const winFullLabel = getWindowFullLabel(windowRange);
	const totalCostFmt = formatDualMoney(kpi?.total_cost_usd ?? 0, currencyMode);
	const totalRevFmt = formatDualMoney(kpi?.total_revenue_usd ?? 0, currencyMode);
	const totalProfitFmt = formatDualMoney(kpi?.total_profit_usd ?? 0, currencyMode, true);
	const overallMarginPct =
		kpi?.gross_margin !== null && kpi?.gross_margin !== undefined
			? kpi.gross_margin * 100
			: null;

	const policy = selectedRule?.policy;
	const minQualifiedPct = policy?.min_qualified_success_rate
		? Math.round(policy.min_qualified_success_rate * 100)
		: 70;

	return (
		<div className="mx-auto w-full max-w-[1480px] space-y-5 pb-8" data-testid="adaptive-routing-dashboard">
			{/* Header */}
			<div className="border-border flex flex-col gap-4 border-b pb-4 lg:flex-row lg:items-center lg:justify-between">
				<div className="space-y-1.5">
					<div className="flex flex-wrap items-center gap-2.5">
						<h1 className="text-xl font-semibold tracking-tight">智能分流与经营看板</h1>
						{selectedRule && (
							<span className={cn("rounded-full px-2.5 py-0.5 text-xs font-medium", pillClass(engineBadge.tone))}>
								{engineBadge.label}
							</span>
						)}
						<span className={cn("rounded-full px-2.5 py-0.5 text-xs", pillClass("muted"))}>纯只读监控</span>
					</div>
					<p className="text-muted-foreground text-xs">
						实时健康态每 10 秒自动刷新（纯内存零库压）；经营与利润口径与 Profit 账本 100% 同源，默认按固定汇率{" "}
						<span className="font-mono">1 USD = {USD_TO_RMB_RATE} RMB</span> 主显人民币。
					</p>
				</div>

				<div className="flex flex-wrap items-center gap-2">
					<Button
						type="button"
						variant="outline"
						size="sm"
						className="h-8 text-xs font-medium"
						onClick={() => setCurrencyMode((m) => (m === "rmb" ? "usd" : "rmb"))}
						title="点击切换主显货币"
						data-testid="adaptive-currency-toggle"
					>
						{currencyMode === "rmb" ? "金额：¥ 主 / $ 副" : "金额：$ 主 / ¥ 副"}
					</Button>

					<Button
						type="button"
						variant="outline"
						size="sm"
						className="h-8 gap-1.5 text-xs font-medium"
						onClick={handleManualRefresh}
						disabled={isForceRefreshing}
						data-testid="adaptive-refresh-btn"
					>
						{isForceRefreshing || isFetching ? (
							<RefreshCw className="h-3.5 w-3.5 animate-spin text-emerald-500" />
						) : (
							<span className="inline-block h-2 w-2 rounded-full bg-emerald-500" />
						)}
						<span>{justRefreshed ? "已刷新 · 刚刚" : "实时 · 10s 自动刷新"}</span>
					</Button>
				</div>
			</div>

			{/* Error state */}
			{Boolean(error) && (
				<Alert variant="destructive">
					<AlertCircle className="h-4 w-4" />
					<AlertDescription className="flex items-center justify-between gap-4">
						<span>加载智能分流与经营看板失败，请检查网关服务状态后重试。</span>
						<Button size="sm" variant="outline" onClick={handleManualRefresh}>
							重试
						</Button>
					</AlertDescription>
				</Alert>
			)}

			{/* Initial Loading state */}
			{isLoading && !dashboard && (
				<div className="border-border bg-card flex min-h-[260px] flex-col items-center justify-center gap-2 rounded-xl border p-8">
					<RefreshCw className="text-muted-foreground h-5 w-5 animate-spin" />
					<span className="text-muted-foreground text-sm">正在加载实时分流状态与经营数据...</span>
				</div>
			)}

			{/* Empty state when no grouped rules exist */}
			{dashboard && dashboard.rules.length === 0 && (
				<div className="border-border bg-card flex min-h-[220px] flex-col items-center justify-center gap-2 rounded-xl border p-8 text-center">
					<ShieldAlert className="text-muted-foreground h-7 w-7" />
					<div className="text-sm font-medium">暂无已启用分组/自适应路由的规则</div>
					<p className="text-muted-foreground max-w-md text-xs">
						请在路由规则中开启分组健康路由或自适应分流策略后，返回此看板查看实时顺位、健康态与渠道利润表现。
					</p>
				</div>
			)}

			{dashboard && selectedRule && (
				<>
					{/* Control Bar: Rule Selector + Policy Strip + Time Window */}
					<div className="border-border bg-card flex flex-col gap-3 rounded-xl border px-4 py-3 xl:flex-row xl:items-center xl:justify-between">
						<div className="flex flex-wrap items-center gap-3">
							<div className="flex items-center gap-2">
								<span className="text-muted-foreground text-xs whitespace-nowrap">路由规则</span>
								<Select
									value={selectedRule.rule_id}
									onValueChange={(value) => {
										setSelectedRuleId(value);
										setTargetFilter("all");
									}}
								>
									<SelectTrigger
										className="h-8 min-w-[230px] text-xs font-medium"
										data-testid="adaptive-rule-selector"
									>
										<SelectValue placeholder="选择路由规则" />
									</SelectTrigger>
									<SelectContent>
										{dashboard.rules.map((r) => (
											<SelectItem key={r.rule_id} value={r.rule_id} className="text-xs">
												<span>{r.rule_name}</span>
												<span className="text-muted-foreground ml-1.5">
													（{r.engine_mode === "adaptive_v22" ? "V2.2" : r.engine_mode === "adaptive_v21" ? "V2.1" : "传统分组"} ·{" "}
													{r.target_count} 分组）
												</span>
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>

							{isAdaptive && (
								<div className="text-muted-foreground flex flex-wrap items-center gap-1.5 text-xs">
									{selectedRule.effective_budget_ms > 0 && (
										<span className="border-border bg-muted/40 rounded-md border px-2 py-0.5">
											总预算{" "}
											<strong className="text-foreground font-mono">
												{formatPolicyMsToSec(selectedRule.effective_budget_ms)}
											</strong>
										</span>
									)}
									<span className="border-border bg-muted/40 rounded-md border px-2 py-0.5">
										最多 <strong className="text-foreground font-mono">{selectedRule.effective_max_attempts}</strong> 跳
									</span>
									{selectedRule.final_hop_reserve_ms ? (
										<span className="border-border bg-muted/40 rounded-md border px-2 py-0.5">
											末跳预留{" "}
											<strong className="text-foreground font-mono">
												{formatPolicyMsToSec(selectedRule.final_hop_reserve_ms)}
											</strong>
										</span>
									) : null}
									<span className="border-border bg-muted/40 rounded-md border px-2 py-0.5">
										快/慢分界{" "}
										<strong className="text-foreground font-mono">
											{formatPolicyMsToSec(policy?.fast_rescue_elapsed_ms ?? 15000)}
										</strong>
									</span>
									<span className="border-border bg-muted/40 rounded-md border px-2 py-0.5">
										合格线 <strong className="text-foreground font-mono">&gt;{minQualifiedPct}%</strong>
									</span>
								</div>
							)}
						</div>

						<div className="flex items-center gap-2">
							<span className="text-muted-foreground text-xs whitespace-nowrap">统计窗口</span>
							<div
								className="border-border bg-background inline-flex rounded-lg border p-0.5 text-xs"
								data-testid="adaptive-window-switcher"
							>
								{ADAPTIVE_WINDOW_OPTIONS.map((opt) => {
									const active = windowRange === opt.value;
									return (
										<button
											key={opt.value}
											type="button"
											onClick={() => setWindowRange(opt.value)}
											className={cn(
												"rounded-md px-2.5 py-1 transition-colors",
												active
													? "bg-primary text-primary-foreground font-medium"
													: "text-muted-foreground hover:text-foreground",
											)}
										>
											{opt.label}
										</button>
									);
								})}
							</div>
						</div>
					</div>

					{/* Logs store disconnected notice */}
					{!dashboard.logs_connected && (
						<Alert>
							<AlertCircle className="h-4 w-4" />
							<AlertDescription className="text-xs">
								当前未连接日志库（logs_store），实时分流状态与健康态正常展示，区间调用次数、响应时间与利润指标暂为 0。
							</AlertDescription>
						</Alert>
					)}

					{/* 5 KPI Cards */}
					<div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-5">
						{/* Card 1: Primary Channel */}
						<div className="border-border bg-card space-y-1.5 rounded-xl border p-4">
							<div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
								<span>此刻头号主力 (#1 首跳)</span>
								{primaryRow?.official_cost_pct ? (
									<span className={cn("rounded px-1.5 py-0.5 font-mono text-[11px]", pillClass("success"))}>
										占官价 {formatOfficialPct(primaryRow.official_cost_pct)}
									</span>
								) : null}
							</div>
							<div className="truncate text-lg font-semibold" title={primaryRow?.provider ?? "暂无可用渠道"}>
								{primaryRow?.provider ?? "暂无可用渠道"}
							</div>
							<div className="text-muted-foreground text-xs tabular-nums">
								{primaryRow ? (
									isAdaptive ? (
										<>
											90m 信誉{" "}
											<strong className="text-foreground font-mono">
												{(primaryRow.effective_success_rate * 100).toFixed(1)}%
											</strong>{" "}
											· P80{" "}
											<strong className="text-foreground font-mono">
												{formatLatencyMs(primaryRow.p80_latency_ms)}
											</strong>{" "}
											· 限{" "}
											<strong className="text-foreground font-mono">
												{formatTimeoutLabel(primaryRow.attempt_timeout_ms, primaryRow.tier)}
											</strong>
										</>
									) : (
										<>
											模型 <strong className="text-foreground font-mono">{primaryRow.model}</strong> · 权重{" "}
											<strong className="text-foreground font-mono">{primaryRow.weight}</strong>
										</>
									)
								) : (
									"当前规则无可用首跳渠道"
								)}
							</div>
						</div>

						{/* Card 2: Customer Final Success Rate */}
						<div className="border-border bg-card space-y-1.5 rounded-xl border p-4">
							<div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
								<span>客户最终成功率 ({winFullLabel})</span>
								<span className="font-mono text-[11px] tabular-nums">
									{formatNumber(kpi?.root_request_count ?? 0)} 单
								</span>
							</div>
							<div
								className={cn(
									"text-2xl font-bold tabular-nums",
									kpi?.root_success_rate !== null && kpi?.root_success_rate !== undefined
										? getRateToneClass(kpi.root_success_rate)
										: "text-muted-foreground",
								)}
							>
								{kpi?.root_success_rate !== null && kpi?.root_success_rate !== undefined
									? `${kpi.root_success_rate.toFixed(1)}%`
									: "—"}
							</div>
							<div className="text-muted-foreground text-xs tabular-nums">
								首跳直接成功{" "}
								<strong className="text-foreground font-mono">
									{kpi?.first_hop_success_rate !== null && kpi?.first_hop_success_rate !== undefined
										? `${kpi.first_hop_success_rate.toFixed(1)}%`
										: "—"}
								</strong>{" "}
								· 救援挽回{" "}
								<strong className="text-foreground font-mono">{formatNumber(kpi?.rescued_count ?? 0)}</strong> 单
							</div>
						</div>

						{/* Card 3: Latency Watermark */}
						<div className="border-border bg-card space-y-1.5 rounded-xl border p-4">
							<div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
								<span>响应时间水位 ({winFullLabel})</span>
								<span className="text-[11px]">成功单</span>
							</div>
							<div className="text-2xl font-bold tabular-nums">
								P50 {formatLatencyMs(kpi?.p50_latency_ms)}
							</div>
							<div className="text-muted-foreground text-xs tabular-nums">
								P80 <strong className="text-foreground font-mono">{formatLatencyMs(kpi?.p80_latency_ms)}</strong> ·
								P95 <strong className="text-foreground font-mono">{formatLatencyMs(kpi?.p95_latency_ms)}</strong> ·
								均 <strong className="text-foreground font-mono">{formatLatencyMs(kpi?.avg_latency_ms)}</strong>
							</div>
						</div>

						{/* Card 4: Window Cost */}
						<div className="border-border bg-card space-y-1.5 rounded-xl border p-4">
							<div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
								<span>区间总成本 ({winFullLabel})</span>
								{kpi?.effective_official_cost_pct !== null && kpi?.effective_official_cost_pct !== undefined ? (
									<span className={cn("rounded px-1.5 py-0.5 font-mono text-[11px]", pillClass("info"))}>
										≈ 官价 {kpi.effective_official_cost_pct.toFixed(1)}%
									</span>
								) : null}
							</div>
							<div className="text-2xl font-bold tabular-nums">{totalCostFmt.main}</div>
							<div className="text-muted-foreground font-mono text-xs tabular-nums">
								{totalCostFmt.sub} · 含全部成功单成本
							</div>
						</div>

						{/* Card 5: Net Profit & Gross Margin */}
						<div className="border-border bg-card space-y-1.5 rounded-xl border p-4">
							<div className="text-muted-foreground flex items-center justify-between gap-2 text-xs">
								<span>区间净利润 &amp; 毛利率</span>
								{overallMarginPct !== null ? (
									<span
										className={cn(
											"rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold",
											pillClass(overallMarginPct >= 0 ? "success" : "error"),
										)}
									>
										毛利 {overallMarginPct.toFixed(1)}%
									</span>
								) : null}
							</div>
							<div
								className={cn(
									"text-2xl font-bold tabular-nums",
									(kpi?.total_profit_usd ?? 0) > 0
										? "text-emerald-600 dark:text-emerald-400"
										: (kpi?.total_profit_usd ?? 0) < 0
											? "text-rose-600 dark:text-rose-400"
											: "text-foreground",
								)}
							>
								{totalProfitFmt.main}
							</div>
							<div className="text-muted-foreground font-mono text-xs tabular-nums">
								{totalProfitFmt.sub} · 收入 {totalRevFmt.main} ({totalRevFmt.sub})
							</div>
						</div>
					</div>

					{/* Next-Request Route Preview Strip (adaptive rules) */}
					{isAdaptive && selectedRule.route_preview && (
						<div className="border-border bg-card space-y-2.5 rounded-xl border px-4 py-3.5">
							<div className="flex flex-wrap items-center justify-between gap-2">
								<div className="flex items-center gap-2 text-xs font-semibold">
									<span className="bg-primary inline-block h-2 w-2 rounded-full" />
									<span>此刻下一单路由预览（按当前实时内存状态推演）</span>
								</div>
								<div className="flex flex-wrap items-center gap-2 text-xs">
									<span className={cn("rounded-full px-2 py-0.5", pillClass("success"))}>
										L1 活跃 {selectedRule.pool_counts.l1_active}
									</span>
									<span className={cn("rounded-full px-2 py-0.5", pillClass("warning"))}>
										L1 短熔断 {selectedRule.pool_counts.l1_tripwire}
									</span>
									<span className={cn("rounded-full px-2 py-0.5", pillClass("info"))}>
										L1 观察席 {selectedRule.pool_counts.l1_probation}
									</span>
									{selectedRule.pool_counts.l1_fatal > 0 && (
										<span className={cn("rounded-full px-2 py-0.5", pillClass("error"))}>
											L1 隔离 {selectedRule.pool_counts.l1_fatal}
										</span>
									)}
									<span className={cn("rounded-full px-2 py-0.5", pillClass("muted"))}>
										L2 待命 {selectedRule.pool_counts.l2_ready} / L3 底牌 {selectedRule.pool_counts.l3_ready}
									</span>
								</div>
							</div>

							<div className="flex flex-wrap items-center gap-2 text-xs">
								{selectedRule.route_preview.primary && (
									<div className="border-border bg-muted/40 rounded-lg border px-3 py-1.5">
										<span className="text-muted-foreground">① 首跳</span>
										<strong className="ml-1.5">{selectedRule.route_preview.primary.provider}</strong>
										{selectedRule.route_preview.primary.official_cost_pct ? (
											<span className="ml-1.5 font-mono text-emerald-600 dark:text-emerald-400">
												{formatOfficialPct(selectedRule.route_preview.primary.official_cost_pct)}
											</span>
										) : null}
										<span className="text-muted-foreground ml-1.5">
											· 限 {formatPolicyMsToSec(selectedRule.route_preview.primary.timeout_ms)}
										</span>
									</div>
								)}

								{selectedRule.route_preview.fast_fail_continuation && (
									<>
										<ArrowRight className="text-muted-foreground h-3.5 w-3.5 shrink-0" />
										<div className="border-border bg-muted/40 rounded-lg border px-3 py-1.5">
											<span className="text-muted-foreground">
												② 快错(&lt;{formatPolicyMsToSec(policy?.fast_rescue_elapsed_ms ?? 15000)})顺延
											</span>
											<strong className="ml-1.5">
												{selectedRule.route_preview.fast_fail_continuation.provider}
											</strong>
											{selectedRule.route_preview.fast_fail_continuation.official_cost_pct ? (
												<span className="ml-1.5 font-mono">
													{formatOfficialPct(
														selectedRule.route_preview.fast_fail_continuation.official_cost_pct,
													)}
												</span>
											) : null}
											<span className="text-muted-foreground ml-1.5">
												· 限 {formatPolicyMsToSec(selectedRule.route_preview.fast_fail_continuation.timeout_ms)}
											</span>
										</div>
									</>
								)}

								{selectedRule.route_preview.timeout_rescue &&
									selectedRule.route_preview.timeout_rescue.length > 0 && (
										<>
											<ArrowRight className="text-muted-foreground h-3.5 w-3.5 shrink-0" />
											<div className="border-border bg-muted/40 rounded-lg border px-3 py-1.5">
												<span className="text-muted-foreground">② 超时快救</span>
												<strong className="ml-1.5">
													{selectedRule.route_preview.timeout_rescue.map((h) => h.provider).join(" / ")}
												</strong>
												<span className="text-muted-foreground ml-1.5">
													· 限 {formatPolicyMsToSec(policy?.rescue_attempt_timeout_ms ?? 20000)}
												</span>
											</div>
										</>
									)}

								{selectedRule.route_preview.final_hop_fallback &&
									selectedRule.route_preview.final_hop_fallback.length > 0 && (
										<>
											<ArrowRight className="text-muted-foreground h-3.5 w-3.5 shrink-0" />
											<div className="border-border bg-muted/40 rounded-lg border px-3 py-1.5">
												<span className="text-muted-foreground">③ 末跳兜底</span>
												<strong className="ml-1.5">
													{selectedRule.route_preview.final_hop_fallback.map((h) => h.provider).join(" → ")}
												</strong>
												{selectedRule.route_preview.final_hop_reserve_ms ? (
													<span className="text-muted-foreground ml-1.5">
														· 预留 ≥{formatPolicyMsToSec(selectedRule.route_preview.final_hop_reserve_ms)}
													</span>
												) : null}
											</div>
										</>
									)}
							</div>

							{selectedRule.route_preview.probation_probe_pending && (
								<div className="text-muted-foreground flex items-center gap-1.5 text-xs">
									<span className={cn("rounded px-1.5 py-0.5 text-[11px]", pillClass("info"))}>观察席待测</span>
									<span>
										下一单将择机对{" "}
										<strong className="text-foreground font-mono">
											{selectedRule.route_preview.probation_probe_pending.provider}
										</strong>{" "}
										发起单并发真单体验（限{" "}
										{formatPolicyMsToSec(selectedRule.route_preview.probation_probe_pending.timeout_ms)}
										，由快枪手通道兜底护航）
									</span>
								</div>
							)}
						</div>
					)}

					{/* Quick Filter Bar */}
					<div className="flex flex-wrap items-center justify-between gap-3">
						{isAdaptive ? (
							<div className="flex flex-wrap items-center gap-1.5 text-xs">
								<span className="text-muted-foreground mr-1">筛选分组：</span>
								{(
									[
										{ id: "all", label: `全部 (${filterCounts.all})` },
										{ id: "active", label: `主力/待命在岗 (${filterCounts.active})` },
										{ id: "tripwire", label: `短熔断 (${filterCounts.tripwire})` },
										{ id: "probation", label: `观察席 / 隔离 (${filterCounts.probation})` },
										{ id: "used", label: `区间有调用 (${filterCounts.used})` },
									] as { id: AdaptiveTargetFilter; label: string }[]
								).map((f) => {
									const active = targetFilter === f.id;
									return (
										<button
											key={f.id}
											type="button"
											onClick={() => setTargetFilter(f.id)}
											className={cn(
												"rounded-full px-2.5 py-1 transition-colors",
												active
													? "bg-primary text-primary-foreground font-medium"
													: "border-border bg-card text-muted-foreground hover:text-foreground border",
											)}
										>
											{f.label}
										</button>
									);
								})}
							</div>
						) : (
							<div />
						)}
						<div className="text-muted-foreground text-xs">
							注：顶栏成功率为<strong className="text-foreground">客户最终成功率（合并重试）</strong>；下表为
							<strong className="text-foreground">各分组单跳成功率</strong>
						</div>
					</div>

					{/* Tier Tables (L1 / L2 / L3) */}
					<div className="space-y-4">
						{tiers.map((tier) => {
							const meta = getTierMeta(tier.tier, engineMode, selectedRule.rule_name);
							const tierCostFmt = formatDualMoney(tier.cost_usd, currencyMode);
							const tierProfitFmt = formatDualMoney(tier.profit_usd, currencyMode, true);
							const tierMarginPct =
								tier.gross_margin !== null && tier.gross_margin !== undefined
									? tier.gross_margin * 100
									: null;

							const visibleRows = isAdaptive
								? tier.targets.filter((r) => filterTargetRow(r, targetFilter))
								: tier.targets;

							return (
								<section
									key={tier.tier}
									className="border-border bg-card overflow-hidden rounded-xl border"
									data-testid={`adaptive-tier-${tier.tier}`}
								>
									{/* Tier Header */}
									<div className="border-border bg-muted/30 flex flex-col gap-2 border-b px-4 py-3 lg:flex-row lg:items-center lg:justify-between">
										<div className="space-y-0.5">
											<div className="flex flex-wrap items-center gap-2">
												<h2 className="text-sm font-semibold">{meta.title}</h2>
												<span className={cn("rounded-full px-2 py-0.5 text-xs", pillClass("muted"))}>
													{meta.badge}
												</span>
											</div>
											<p className="text-muted-foreground text-xs">{meta.description}</p>
										</div>

										<div className="flex flex-wrap items-center gap-3 text-xs tabular-nums">
											<span className="text-muted-foreground">
												成功单{" "}
												<strong className="text-foreground font-mono">
													{formatNumber(tier.success_count)}
												</strong>{" "}
												({tier.success_share.toFixed(1)}%)
											</span>
											<span className="text-muted-foreground">
												层成本{" "}
												<strong className="text-foreground font-mono">{tierCostFmt.main}</strong>{" "}
												<span className="font-mono">({tierCostFmt.sub})</span>
											</span>
											<span className="text-muted-foreground">
												层利润{" "}
												<strong
													className={cn(
														"font-mono",
														tier.profit_usd > 0
															? "text-emerald-600 dark:text-emerald-400"
															: tier.profit_usd < 0
																? "text-rose-600 dark:text-rose-400"
																: "text-foreground",
													)}
												>
													{tierProfitFmt.main}
												</strong>{" "}
												<span className="font-mono">
													(毛利率 {tierMarginPct !== null ? `${tierMarginPct.toFixed(1)}%` : "—"})
												</span>
											</span>
										</div>
									</div>

									{/* Full 9-Column Table */}
									<div className="overflow-x-auto">
										<table className="w-full min-w-[1220px] border-collapse text-left">
											<thead>
												<tr className="text-muted-foreground bg-card text-xs">
													<th className="px-3.5 py-2.5 font-medium">顺位 &amp; 分组</th>
													<th className="px-3 py-2.5 font-medium">占官价单价</th>
													<th className="px-3 py-2.5 font-medium">当前实时状态</th>
													<th className="px-3 py-2.5 font-medium">90m 信誉 &amp; 10m 救援</th>
													<th className="px-3 py-2.5 font-medium">区间调用 &amp; 成功率</th>
													<th className="px-3 py-2.5 font-medium">响应时间</th>
													<th className="px-3 py-2.5 text-right font-medium">区间成本</th>
													<th className="px-3 py-2.5 text-right font-medium">区间利润 &amp; 毛利</th>
													<th className="px-3.5 py-2.5 font-medium">最近动态 / 报错</th>
												</tr>
											</thead>
											<tbody>
												{visibleRows.length === 0 ? (
													<tr className="border-border border-t">
														<td colSpan={9} className="text-muted-foreground py-6 text-center text-xs">
															当前筛选条件下该层无匹配分组
														</td>
													</tr>
												) : (
													visibleRows.map((row) => {
														const statePres = formatTargetStateBadge(row, engineMode, nowMs);
														const lastEvt = formatLastEventSummary(row, nowMs);
														const rep90Pct = row.effective_success_rate * 100;
														const hopRate = row.metrics.single_hop_success_rate;
														const r10 = row.rescue_10m_total;
														const r10Pct =
															r10.attempts > 0 ? Math.round((r10.fast / r10.attempts) * 100) : null;
														const r10Tooltip = `非流式 10m: ${row.rescue_10m_non_stream.fast}/${row.rescue_10m_non_stream.attempts} · 流式 10m: ${row.rescue_10m_stream.fast}/${row.rescue_10m_stream.attempts}`;

														const rowCostFmt = formatDualMoney(row.metrics.cost_usd, currencyMode);
														const rowProfitFmt = formatDualMoney(row.metrics.profit_usd, currencyMode, true);
														const rowMarginPct =
															row.metrics.gross_margin !== null && row.metrics.gross_margin !== undefined
																? row.metrics.gross_margin * 100
																: null;

														return (
															<tr
																key={row.target_key}
																className={cn(
																	"border-border hover:bg-muted/40 border-t transition-colors",
																	row.is_primary ? "bg-muted/25" : "",
																)}
															>
																{/* 1. Rank & Provider */}
																<td className="px-3.5 py-2.5 align-top">
																	<div className="flex items-start gap-2">
																		<span
																			className={cn(
																				"inline-flex min-w-[28px] items-center justify-center rounded px-1.5 py-0.5 font-mono text-xs font-semibold",
																				row.is_primary
																					? "bg-primary text-primary-foreground"
																					: "border-border bg-muted/40 text-muted-foreground border",
																			)}
																		>
																			{row.rank_label}
																		</span>
																		<div className="min-w-0">
																			<div className="truncate text-[13px] leading-tight font-medium">
																				{row.provider}
																			</div>
																			<div className="text-muted-foreground mt-0.5 text-xs">
																				<span className="font-mono">{row.model}</span>
																				<span className="mx-1">·</span>
																				<span>
																					限{" "}
																					<span className="font-mono">
																						{formatTimeoutLabel(row.attempt_timeout_ms, row.tier)}
																					</span>
																				</span>
																			</div>
																		</div>
																	</div>
																</td>

																{/* 2. Official % & Pricing */}
																<td className="px-3 py-2.5 align-top whitespace-nowrap tabular-nums">
																	<div
																		className={cn(
																			"font-mono text-[13px] font-semibold",
																			row.official_cost_pct !== null &&
																				row.official_cost_pct !== undefined
																				? row.official_cost_pct <= 30
																					? "text-emerald-600 dark:text-emerald-400"
																					: row.official_cost_pct >= 70
																						? "text-amber-600 dark:text-amber-400"
																						: ""
																				: "text-muted-foreground",
																		)}
																	>
																		{formatOfficialPct(row.official_cost_pct)}
																	</div>
																	<div className="text-muted-foreground mt-0.5 font-mono text-xs">
																		{formatPer1MPricePair(
																			row.input_cost_per_1m_usd,
																			row.output_cost_per_1m_usd,
																		)}
																	</div>
																</td>

																{/* 3. Live State */}
																<td className="px-3 py-2.5 align-top whitespace-nowrap">
																	<div className="flex flex-wrap items-center gap-1">
																		<span
																			className={cn(
																				"inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium",
																				pillClass(statePres.tone),
																			)}
																		>
																			<span
																				className={cn(
																					"h-1.5 w-1.5 rounded-full",
																					dotClass(statePres.tone),
																				)}
																			/>
																			{statePres.label}
																		</span>
																		{isAdaptive && row.is_fast_rescue && (
																			<span
																				className={cn(
																					"inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[11px] font-medium",
																					pillClass("warning"),
																				)}
																			>
																				<Zap className="h-3 w-3" />
																				快枪手
																			</span>
																		)}
																	</div>
																</td>

																{/* 4. 90m Reputation & 10m Rescue */}
																<td
																	className="px-3 py-2.5 align-top whitespace-nowrap tabular-nums"
																	title={r10Tooltip}
																>
																	<div className="text-xs">
																		<span className="text-muted-foreground">90m:</span>
																		<strong className={cn("ml-1 font-mono", getRateToneClass(rep90Pct))}>
																			{rep90Pct.toFixed(1)}%
																		</strong>
																		<span className="text-muted-foreground ml-1.5 font-mono">
																			P80 {formatLatencyMs(row.p80_latency_ms)}
																		</span>
																	</div>
																	<div className="mt-1 text-xs">
																		<span className="text-muted-foreground">10m≤16s:</span>
																		<span className="ml-1 font-mono">
																			{r10.attempts >= 6 && r10Pct !== null ? (
																				<span className={cn("font-medium", getRateToneClass(r10Pct))}>
																					{r10.fast}/{r10.attempts} ({r10Pct}%)
																				</span>
																			) : r10.attempts > 0 ? (
																				<span className="text-muted-foreground">
																					{r10.fast}/{r10.attempts} · 样本不足
																				</span>
																			) : (
																				<span className="text-muted-foreground">无近期样本</span>
																			)}
																		</span>
																	</div>
																</td>

																{/* 5. Window Calls & Single-Hop Success Rate */}
																<td className="px-3 py-2.5 align-top whitespace-nowrap tabular-nums">
																	<div className="flex items-baseline justify-between gap-2 text-xs">
																		<span className="font-semibold">
																			{formatNumber(row.metrics.request_count)} 次
																		</span>
																		<span
																			className={cn(
																				"font-mono font-medium",
																				hopRate !== null && hopRate !== undefined
																					? getRateToneClass(hopRate)
																					: "text-muted-foreground",
																			)}
																		>
																			{hopRate !== null && hopRate !== undefined
																				? `${hopRate.toFixed(1)}%`
																				: "—"}
																		</span>
																	</div>
																	<div className="bg-muted mt-1 h-1.5 w-28 overflow-hidden rounded-full">
																		<div
																			className={cn(
																				"h-full rounded-full transition-all",
																				hopRate !== null && hopRate !== undefined
																					? getRateBarClass(hopRate)
																					: "",
																			)}
																			style={{
																				width: `${hopRate !== null && hopRate !== undefined ? Math.min(100, Math.max(0, hopRate)) : 0}%`,
																			}}
																		/>
																	</div>
																	<div className="text-muted-foreground mt-0.5 font-mono text-xs">
																		成 {formatNumber(row.metrics.success_count)} / 败{" "}
																		{formatNumber(row.metrics.error_count)}
																	</div>
																</td>

																{/* 6. Latency Percentiles */}
																<td className="px-3 py-2.5 align-top font-mono text-xs whitespace-nowrap tabular-nums">
																	<div>
																		<span className="text-muted-foreground">P50</span>{" "}
																		<strong className="text-foreground">
																			{formatLatencyMs(row.metrics.p50_latency_ms)}
																		</strong>{" "}
																		· <span className="text-muted-foreground">P80</span>{" "}
																		<strong className="text-foreground">
																			{formatLatencyMs(row.metrics.p80_latency_ms)}
																		</strong>
																	</div>
																	<div className="text-muted-foreground mt-0.5">
																		P95 {formatLatencyMs(row.metrics.p95_latency_ms)} · 均{" "}
																		{formatLatencyMs(row.metrics.avg_latency_ms)}
																	</div>
																</td>

																{/* 7. Cost */}
																<td className="px-3 py-2.5 text-right align-top font-mono whitespace-nowrap tabular-nums">
																	<div className="text-[13px] font-semibold">
																		{row.metrics.request_count > 0 || row.metrics.cost_usd > 0
																			? rowCostFmt.main
																			: "—"}
																	</div>
																	<div className="text-muted-foreground mt-0.5 text-xs">
																		{row.metrics.request_count > 0 || row.metrics.cost_usd > 0
																			? rowCostFmt.sub
																			: "—"}
																	</div>
																</td>

																{/* 8. Profit & Margin */}
																<td className="px-3 py-2.5 text-right align-top font-mono whitespace-nowrap tabular-nums">
																	<div
																		className={cn(
																			"text-[13px] font-semibold",
																			row.metrics.success_count > 0 || row.metrics.revenue_usd > 0
																				? row.metrics.profit_usd > 0
																					? "text-emerald-600 dark:text-emerald-400"
																					: row.metrics.profit_usd < 0
																						? "text-rose-600 dark:text-rose-400"
																						: "text-foreground"
																				: "text-muted-foreground",
																		)}
																	>
																		{row.metrics.success_count > 0 || row.metrics.revenue_usd > 0
																			? rowProfitFmt.main
																			: "—"}
																	</div>
																	<div className="text-muted-foreground mt-0.5 text-xs">
																		{rowMarginPct !== null
																			? `${rowProfitFmt.sub} · 率 ${rowMarginPct.toFixed(1)}%`
																			: "—"}
																	</div>
																</td>

																{/* 9. Recent Activity / Error */}
																<td className="max-w-[210px] px-3.5 py-2.5 align-top">
																	<div
																		className={cn(
																			"truncate text-xs",
																			lastEvt.isWarning
																				? "text-amber-600 dark:text-amber-400"
																				: "text-muted-foreground",
																		)}
																		title={lastEvt.detail || lastEvt.summary}
																	>
																		{lastEvt.summary}
																	</div>
																	{lastEvt.detail ? (
																		<div
																			className="text-muted-foreground mt-0.5 truncate text-xs"
																			title={lastEvt.detail}
																		>
																			{lastEvt.detail}
																		</div>
																	) : null}
																</td>
															</tr>
														);
													})
												)}
											</tbody>
										</table>
									</div>
								</section>
							);
						})}
					</div>
				</>
			)}
		</div>
	);
}
