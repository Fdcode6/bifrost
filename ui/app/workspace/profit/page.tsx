"use client";

import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { AlertCircle, CheckCircle2, Clock3, Database, DollarSign, Loader2, Percent, RefreshCw, RotateCw, Save, TrendingUp } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
	getErrorMessage,
	useBackfillProfitEventsMutation,
	useGetProfitBreakdownQuery,
	useGetProfitDailyQuery,
	useGetProfitReconciliationStatusQuery,
	useGetProfitSettingsQuery,
	useGetProfitSummaryQuery,
	useUpdateProfitSettingsMutation,
} from "@/lib/store";
import type { ProfitDailyBucket, ProfitPreset, ProfitSummary } from "@/lib/types/profit";
import { cn } from "@/lib/utils";

import {
	type DisplayCurrency,
	USD_TO_CNY_RATE,
	formatCompactNumber,
	formatMargin,
	formatMoney,
	getDisplayCurrencyLabel,
	getProfitPresetLabel,
} from "./profitFormatting";

const PRESETS: ProfitPreset[] = ["today", "yesterday", "7d", "all"];
const DISPLAY_CURRENCIES: DisplayCurrency[] = ["CNY", "USD"];

const emptySummary: ProfitSummary = {
	revenue_usd: 0,
	cost_usd: 0,
	profit_usd: 0,
	gross_margin: null,
	request_count: 0,
	success_count: 0,
	error_count: 0,
	prompt_tokens: 0,
	completion_tokens: 0,
	total_tokens: 0,
	missing_cost_count: 0,
	missing_tokens_count: 0,
};

function parsePrice(value: string): number | null {
	const normalized = Number(value);
	if (!Number.isFinite(normalized) || normalized < 0) {
		return null;
	}
	return normalized;
}

function MetricCard({
	title,
	value,
	note,
	icon,
	tone = "default",
}: {
	title: string;
	value: string;
	note: string;
	icon: ReactNode;
	tone?: "default" | "profit" | "cost";
}) {
	return (
		<Card className="min-h-[132px] rounded-lg">
			<CardContent className="flex h-full flex-col justify-between gap-4 p-5">
				<div className="text-muted-foreground flex items-center justify-between gap-3 text-sm font-medium">
					<span>{title}</span>
					<span
						className={cn(
							"rounded-md border p-1.5",
							tone === "profit" ? "border-emerald-200 bg-emerald-50 text-emerald-700" : "",
							tone === "cost" ? "border-amber-200 bg-amber-50 text-amber-700" : "",
							tone === "default" ? "bg-muted text-muted-foreground" : "",
						)}
					>
						{icon}
					</span>
				</div>
				<div>
					<div className={cn("text-2xl font-semibold tabular-nums", tone === "profit" ? "text-emerald-700" : "")}>{value}</div>
					<div className="text-muted-foreground mt-1 text-xs">{note}</div>
				</div>
			</CardContent>
		</Card>
	);
}

function TrendTooltip({ active, payload, label, currency }: any) {
	if (!active || !payload?.length) {
		return null;
	}
	return (
		<div className="bg-background rounded-md border p-3 text-xs shadow-sm">
			<div className="mb-2 font-semibold">{label}</div>
			{payload.map((item: any) => (
				<div key={item.dataKey} className="flex min-w-36 justify-between gap-4">
					<span style={{ color: item.color }}>{item.name}</span>
					<span className="font-mono">{formatMoney(Number(item.value), currency)}</span>
				</div>
			))}
		</div>
	);
}

function dailyChartData(days: ProfitDailyBucket[]) {
	return [...days]
		.reverse()
		.map((day) => ({
			day: day.business_day.slice(5),
			收入: Number(day.revenue_usd.toFixed(4)),
			成本: Number(day.cost_usd.toFixed(4)),
			利润: Number(day.profit_usd.toFixed(4)),
		}))
		.slice(-30);
}

function formatStatusDateTime(value?: string | null) {
	if (!value) {
		return "尚未运行";
	}
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) {
		return "时间未知";
	}
	return date.toLocaleString("zh-CN", {
		month: "2-digit",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
	});
}

function formatDuration(seconds: number) {
	if (!Number.isFinite(seconds) || seconds <= 0) {
		return "即将";
	}
	if (seconds < 60) {
		return `${Math.ceil(seconds)} 秒`;
	}
	if (seconds < 3600) {
		return `${Math.ceil(seconds / 60)} 分钟`;
	}
	return `${Math.ceil(seconds / 3600)} 小时`;
}

function formatNextRun(value?: string | null) {
	if (!value) {
		return "等待首次安排";
	}
	const next = new Date(value).getTime();
	if (Number.isNaN(next)) {
		return "时间未知";
	}
	const seconds = Math.ceil((next - Date.now()) / 1000);
	return seconds <= 0 ? "即将运行" : `约 ${formatDuration(seconds)}后`;
}

export default function ProfitPage() {
	const [preset, setPreset] = useState<ProfitPreset>("today");
	const [displayCurrency, setDisplayCurrency] = useState<DisplayCurrency>("CNY");
	const [inputPrice, setInputPrice] = useState("2");
	const [outputPrice, setOutputPrice] = useState("12");

	const {
		data: settings,
		error: settingsError,
		isLoading: isSettingsLoading,
		isFetching: isSettingsFetching,
		refetch: refetchSettings,
	} = useGetProfitSettingsQuery();
	const {
		data: summaryResponse,
		error: summaryError,
		isLoading: isSummaryLoading,
		isFetching: isSummaryFetching,
		refetch: refetchSummary,
	} = useGetProfitSummaryQuery(preset);
	const {
		data: dailyResponse,
		error: dailyError,
		isLoading: isDailyLoading,
		isFetching: isDailyFetching,
		refetch: refetchDaily,
	} = useGetProfitDailyQuery(30);
	const {
		data: breakdownResponse,
		error: breakdownError,
		isLoading: isBreakdownLoading,
		isFetching: isBreakdownFetching,
		refetch: refetchBreakdown,
	} = useGetProfitBreakdownQuery(preset);
	const {
		data: reconciliationStatus,
		error: reconciliationStatusError,
		isFetching: isReconciliationStatusFetching,
		refetch: refetchReconciliationStatus,
	} = useGetProfitReconciliationStatusQuery();
	const [updateProfitSettings, { isLoading: isSaving }] = useUpdateProfitSettingsMutation();
	const [backfillProfitEvents, { isLoading: isBackfilling }] = useBackfillProfitEventsMutation();

	useEffect(() => {
		if (!settings) {
			return;
		}
		setInputPrice(String(settings.sell_input_per_1m_usd));
		setOutputPrice(String(settings.sell_output_per_1m_usd));
	}, [settings]);

	const summary = summaryResponse?.data ?? emptySummary;
	const chartData = useMemo(() => dailyChartData(dailyResponse?.days ?? []), [dailyResponse?.days]);
	const breakdownRows = breakdownResponse?.rows ?? [];
	const isRefreshing = isSettingsFetching || isSummaryFetching || isDailyFetching || isBreakdownFetching || isReconciliationStatusFetching;
	const pageError = settingsError || summaryError || dailyError || breakdownError || reconciliationStatusError;
	const money = (value: number | null | undefined) => formatMoney(value, displayCurrency);
	const missingProfitEvents = reconciliationStatus?.missing_event_count ?? 0;
	const isProfitLedgerComplete = missingProfitEvents === 0;

	const handleRefresh = () => {
		void Promise.all([refetchSettings(), refetchSummary(), refetchDaily(), refetchBreakdown(), refetchReconciliationStatus()]);
	};

	const handleSave = async (event: React.FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		const sellInput = parsePrice(inputPrice);
		const sellOutput = parsePrice(outputPrice);
		if (sellInput === null || sellOutput === null || (sellInput === 0 && sellOutput === 0)) {
			toast.error("出售价格必须为非负数，并且至少有一项大于 0。");
			return;
		}
		try {
			await updateProfitSettings({
				sell_input_per_1m_usd: sellInput,
				sell_output_per_1m_usd: sellOutput,
				timezone: settings?.timezone ?? "Asia/Shanghai",
			}).unwrap();
			toast.success("出售价格已保存，只影响之后的新请求。");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const handleBackfill = async () => {
		try {
			const result = await backfillProfitEvents({ limit: 5000 }).unwrap();
			toast.success(`回填完成：新增 ${result.created} 条，处理 ${result.processed} 条。`);
			void refetchReconciliationStatus();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<div className="flex flex-col gap-6 p-6" data-testid="profit-dashboard-page">
			<div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
				<div>
					<h2 className="text-2xl font-bold tracking-tight">利润统计</h2>
					<p className="text-muted-foreground mt-1 text-sm">按真实请求、成本和出售价格计算收入与利润。</p>
					<p className="text-muted-foreground mt-2 text-xs">利润账本独立保存，清空 LLM Logs 不会清空利润统计。</p>
				</div>
				<div className="flex flex-wrap items-center gap-2">
					<Badge variant="outline">时区：{settings?.timezone ?? "Asia/Shanghai"}</Badge>
					<Badge variant="outline">展示售价：{money(settings?.sell_input_per_1m_usd)} / {money(settings?.sell_output_per_1m_usd)}</Badge>
					<Badge variant="outline">汇率：1 USD = ¥{USD_TO_CNY_RATE}</Badge>
					<div className="flex items-center rounded-md border bg-background p-0.5" aria-label="利润展示单位">
						{DISPLAY_CURRENCIES.map((currency) => (
							<Button
								key={currency}
								type="button"
								variant={displayCurrency === currency ? "default" : "ghost"}
								size="sm"
								onClick={() => setDisplayCurrency(currency)}
								dataTestId={`profit-currency-${currency.toLowerCase()}`}
							>
								{getDisplayCurrencyLabel(currency)}
							</Button>
						))}
					</div>
					<Button variant="outline" size="sm" onClick={handleRefresh} disabled={isRefreshing} className="gap-2">
						<RefreshCw className={cn("h-4 w-4", isRefreshing ? "animate-spin" : "")} />
						刷新
					</Button>
				</div>
			</div>

			{pageError ? (
				<Alert variant="destructive">
					<AlertCircle className="h-4 w-4" />
					<AlertDescription>{getErrorMessage(pageError)}</AlertDescription>
				</Alert>
			) : null}

			<div className="flex flex-wrap gap-2">
				{PRESETS.map((item) => (
					<Button key={item} variant={preset === item ? "default" : "outline"} size="sm" onClick={() => setPreset(item)}>
						{getProfitPresetLabel(item)}
					</Button>
				))}
			</div>

			<Card className="rounded-lg">
				<CardContent className="grid gap-4 p-5 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,2fr)_auto] lg:items-center">
					<div className="flex items-center gap-3">
						<span
							className={cn(
								"rounded-md border p-2",
								isProfitLedgerComplete ? "border-emerald-200 bg-emerald-50 text-emerald-700" : "border-amber-200 bg-amber-50 text-amber-700",
							)}
						>
							{isProfitLedgerComplete ? <CheckCircle2 className="h-4 w-4" /> : <AlertCircle className="h-4 w-4" />}
						</span>
						<div>
							<CardTitle className="text-base">利润账本完整性</CardTitle>
							<CardDescription>
								{isProfitLedgerComplete ? "当前 logs 与利润事件已对齐。" : `当前还有 ${formatCompactNumber(missingProfitEvents)} 条日志未入账。`}
							</CardDescription>
						</div>
					</div>
					<div className="grid gap-3 text-sm sm:grid-cols-3">
						<div>
							<p className="text-muted-foreground text-xs font-medium">上次自动校对</p>
							<p className="mt-1 tabular-nums">{formatStatusDateTime(reconciliationStatus?.last_run_at)}</p>
						</div>
						<div>
							<p className="text-muted-foreground text-xs font-medium">下次自动校对</p>
							<p className="mt-1 flex items-center gap-1 tabular-nums">
								<Clock3 className="text-muted-foreground h-3.5 w-3.5" />
								{formatNextRun(reconciliationStatus?.next_run_at)}
							</p>
						</div>
						<div>
							<p className="text-muted-foreground text-xs font-medium">上次结果</p>
							<p className="mt-1 tabular-nums">
								{reconciliationStatus?.last_error
									? "上次失败"
									: reconciliationStatus?.last_result
										? `新增 ${formatCompactNumber(reconciliationStatus.last_result.created)} / 处理 ${formatCompactNumber(reconciliationStatus.last_result.processed)}`
										: `每 ${formatDuration(reconciliationStatus?.interval_seconds ?? 600)}校对，单批 ${formatCompactNumber(reconciliationStatus?.batch_limit ?? 1000)}`}
							</p>
						</div>
					</div>
					<div className="flex gap-2 lg:justify-end">
						<Button variant="outline" size="sm" onClick={() => void refetchReconciliationStatus()} disabled={isReconciliationStatusFetching} className="gap-2">
							<RefreshCw className={cn("h-4 w-4", isReconciliationStatusFetching ? "animate-spin" : "")} />
							刷新状态
						</Button>
						<Button size="sm" onClick={handleBackfill} disabled={isBackfilling || isProfitLedgerComplete} className="gap-2">
							{isBackfilling ? <Loader2 className="h-4 w-4 animate-spin" /> : <RotateCw className="h-4 w-4" />}
							补齐缺口
						</Button>
					</div>
					{reconciliationStatus?.last_error ? (
						<div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive lg:col-span-3">
							上次校对失败：{reconciliationStatus.last_error}
						</div>
					) : null}
				</CardContent>
			</Card>

			<div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
				<MetricCard
					title="收入"
					value={isSummaryLoading ? "加载中" : money(summary.revenue_usd)}
					note={`${formatCompactNumber(summary.success_count)} 次成功请求`}
					icon={<DollarSign className="h-4 w-4" />}
				/>
				<MetricCard
					title="成本"
					value={isSummaryLoading ? "加载中" : money(summary.cost_usd)}
					note={`${formatCompactNumber(summary.error_count)} 次失败尝试`}
					icon={<Database className="h-4 w-4" />}
					tone="cost"
				/>
				<MetricCard
					title="利润"
					value={isSummaryLoading ? "加载中" : money(summary.profit_usd)}
					note="收入 - provider 成本"
					icon={<TrendingUp className="h-4 w-4" />}
					tone="profit"
				/>
				<MetricCard
					title="毛利率"
					value={isSummaryLoading ? "加载中" : formatMargin(summary.gross_margin)}
					note="利润 / 收入"
					icon={<Percent className="h-4 w-4" />}
				/>
			</div>

			<div className="grid gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(360px,0.75fr)]">
				<Card className="rounded-lg">
					<CardHeader className="border-b">
						<div>
							<CardTitle>每日利润趋势</CardTitle>
							<CardDescription>收入、成本、利润按业务日期聚合。</CardDescription>
						</div>
					</CardHeader>
					<CardContent className="p-5">
						<div className="h-[300px]">
							{isDailyLoading ? (
								<div className="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
									<Loader2 className="h-4 w-4 animate-spin" />
									加载趋势数据
								</div>
							) : chartData.length === 0 ? (
								<div className="text-muted-foreground flex h-full items-center justify-center text-sm">暂无利润数据</div>
							) : (
								<ResponsiveContainer width="100%" height="100%">
									<BarChart data={chartData} margin={{ left: 0, right: 8, top: 8, bottom: 0 }}>
										<CartesianGrid strokeDasharray="3 3" vertical={false} />
										<XAxis dataKey="day" tickLine={false} axisLine={false} fontSize={12} />
										<YAxis tickLine={false} axisLine={false} fontSize={12} tickFormatter={(value) => formatMoney(Number(value), displayCurrency)} width={72} />
										<Tooltip content={<TrendTooltip currency={displayCurrency} />} />
										<Legend />
										<Bar dataKey="收入" fill="#2f5f8f" radius={[3, 3, 0, 0]} />
										<Bar dataKey="成本" fill="#a56208" radius={[3, 3, 0, 0]} />
										<Bar dataKey="利润" fill="#087b5b" radius={[3, 3, 0, 0]} />
									</BarChart>
								</ResponsiveContainer>
							)}
						</div>
						<div className="mt-4 grid gap-3 border-t pt-4 sm:grid-cols-2">
							<div>
								<p className="text-muted-foreground text-xs font-medium">缺失成本</p>
								<p className="mt-1 text-xl font-semibold tabular-nums">{formatCompactNumber(summary.missing_cost_count)}</p>
							</div>
							<div>
								<p className="text-muted-foreground text-xs font-medium">缺失 token</p>
								<p className="mt-1 text-xl font-semibold tabular-nums">{formatCompactNumber(summary.missing_tokens_count)}</p>
							</div>
						</div>
					</CardContent>
				</Card>

				<Card className="rounded-lg">
					<CardHeader className="border-b">
						<CardTitle>出售价格设置</CardTitle>
						<CardDescription>保存后只影响之后的新请求。</CardDescription>
					</CardHeader>
					<CardContent className="p-5">
						<form className="flex flex-col gap-4" onSubmit={handleSave}>
							<div className="grid gap-2">
								<label htmlFor="profit-input-price" className="text-sm font-medium">
									输入售价
								</label>
								<div className="grid gap-2">
									<Input
										id="profit-input-price"
										value={inputPrice}
										onChange={(event) => setInputPrice(event.target.value)}
										inputMode="decimal"
										disabled={isSettingsLoading || isSaving}
										data-testid="profit-input-price"
									/>
								<p className="text-muted-foreground text-xs">保存单位仍为 USD / 100 万 input tokens；上方统计可切换人民币或美元展示。</p>
								</div>
							</div>
							<div className="grid gap-2">
								<label htmlFor="profit-output-price" className="text-sm font-medium">
									输出售价
								</label>
								<div className="grid gap-2">
									<Input
										id="profit-output-price"
										value={outputPrice}
										onChange={(event) => setOutputPrice(event.target.value)}
										inputMode="decimal"
										disabled={isSettingsLoading || isSaving}
										data-testid="profit-output-price"
									/>
									<p className="text-muted-foreground text-xs">保存单位仍为 USD / 100 万 output tokens；历史利润不会随展示单位变化。</p>
								</div>
							</div>
							<Button type="submit" disabled={isSaving || isSettingsLoading} className="gap-2" dataTestId="profit-save-settings">
								{isSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
								保存出售价格
							</Button>
							<div className="rounded-md border border-amber-200 bg-amber-50 p-3 text-xs font-medium text-amber-900">
								历史利润已经按当时售价固化，保存新售价不会重算旧数据。
							</div>
						</form>
						<div className="mt-5 border-t pt-5">
							<Button variant="outline" onClick={handleBackfill} disabled={isBackfilling} className="w-full gap-2">
								{isBackfilling ? <Loader2 className="h-4 w-4 animate-spin" /> : <RotateCw className="h-4 w-4" />}
								从现有日志回填利润
							</Button>
							<p className="text-muted-foreground mt-2 text-xs">只回填当前还存在的 logs；清空过的日志无法补回。</p>
						</div>
					</CardContent>
				</Card>
			</div>

			<Card className="rounded-lg">
				<CardHeader className="border-b">
					<CardTitle>节点利润明细</CardTitle>
					<CardDescription>按中转站和模型聚合，快速定位高利润、低利润和缺失成本的节点。</CardDescription>
				</CardHeader>
				<CardContent className="p-0">
					<div className="overflow-x-auto">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead className="min-w-[150px]">中转站</TableHead>
									<TableHead className="min-w-[260px]">模型</TableHead>
									<TableHead>请求数</TableHead>
									<TableHead>成功</TableHead>
									<TableHead>输入 tokens</TableHead>
									<TableHead>输出 tokens</TableHead>
									<TableHead>收入</TableHead>
									<TableHead>成本</TableHead>
									<TableHead>利润</TableHead>
									<TableHead>毛利率</TableHead>
									<TableHead>缺失成本</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{isBreakdownLoading ? (
									<TableRow>
										<TableCell colSpan={11} className="text-muted-foreground h-24 text-center">
											<div className="flex items-center justify-center gap-2">
												<Loader2 className="h-4 w-4 animate-spin" />
												加载节点明细
											</div>
										</TableCell>
									</TableRow>
								) : breakdownRows.length === 0 ? (
									<TableRow>
										<TableCell colSpan={11} className="text-muted-foreground h-24 text-center">
											暂无节点利润数据
										</TableCell>
									</TableRow>
								) : (
									breakdownRows.map((row) => (
										<TableRow key={`${row.provider}:${row.model}`}>
											<TableCell className="font-medium">{row.provider || "未知中转站"}</TableCell>
											<TableCell className="font-mono text-xs">{row.model || "未知模型"}</TableCell>
											<TableCell>{formatCompactNumber(row.request_count)}</TableCell>
											<TableCell>{formatCompactNumber(row.success_count)}</TableCell>
											<TableCell>{formatCompactNumber(row.prompt_tokens)}</TableCell>
											<TableCell>{formatCompactNumber(row.completion_tokens)}</TableCell>
											<TableCell className="font-mono">{money(row.revenue_usd)}</TableCell>
											<TableCell className="font-mono">{money(row.cost_usd)}</TableCell>
											<TableCell className={cn("font-mono font-semibold", row.profit_usd >= 0 ? "text-emerald-700" : "text-destructive")}>
												{money(row.profit_usd)}
											</TableCell>
											<TableCell>{formatMargin(row.gross_margin)}</TableCell>
											<TableCell>
												{row.missing_cost_count > 0 ? (
													<Badge variant="outline" className="border-amber-300 bg-amber-50 text-amber-800">
														{formatCompactNumber(row.missing_cost_count)}
													</Badge>
												) : (
													<span className="text-muted-foreground">0</span>
												)}
											</TableCell>
										</TableRow>
									))
								)}
							</TableBody>
						</Table>
					</div>
				</CardContent>
			</Card>

			<Card className="rounded-lg">
				<CardHeader className="border-b">
					<CardTitle>每日明细</CardTitle>
					<CardDescription>用于核对收入、成本和利润是否符合预期。</CardDescription>
				</CardHeader>
				<CardContent className="p-0">
					<div className="overflow-x-auto">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>日期</TableHead>
									<TableHead>请求数</TableHead>
									<TableHead>成功</TableHead>
									<TableHead>输入 tokens</TableHead>
									<TableHead>输出 tokens</TableHead>
									<TableHead>收入</TableHead>
									<TableHead>成本</TableHead>
									<TableHead>利润</TableHead>
									<TableHead>毛利率</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{(dailyResponse?.days ?? []).length === 0 ? (
									<TableRow>
										<TableCell colSpan={9} className="text-muted-foreground h-24 text-center">
											暂无利润数据
										</TableCell>
									</TableRow>
								) : (
									(dailyResponse?.days ?? []).map((day) => (
										<TableRow key={day.business_day}>
											<TableCell className="font-medium">{day.business_day}</TableCell>
											<TableCell>{formatCompactNumber(day.request_count)}</TableCell>
											<TableCell>{formatCompactNumber(day.success_count)}</TableCell>
											<TableCell>{formatCompactNumber(day.prompt_tokens)}</TableCell>
											<TableCell>{formatCompactNumber(day.completion_tokens)}</TableCell>
											<TableCell className="font-mono">{money(day.revenue_usd)}</TableCell>
											<TableCell className="font-mono">{money(day.cost_usd)}</TableCell>
											<TableCell className={cn("font-mono font-semibold", day.profit_usd >= 0 ? "text-emerald-700" : "text-destructive")}>
												{money(day.profit_usd)}
											</TableCell>
											<TableCell>{formatMargin(day.gross_margin)}</TableCell>
										</TableRow>
									))
								)}
							</TableBody>
						</Table>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
