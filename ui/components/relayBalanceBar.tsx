"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import {
	AlertTriangle,
	Bell,
	BellOff,
	CheckCircle2,
	ExternalLink,
	Loader2,
	RefreshCw,
	Settings2,
	Wallet,
	XCircle,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
	getErrorMessage,
	useGetRelayBalanceSettingsQuery,
	useGetRelayBalancesQuery,
	useRefreshRelayBalancesMutation,
	useTestRelayBalanceBarkMutation,
	useUpdateRelayBalanceSettingsMutation,
} from "@/lib/store";
import type {
	RelayBalanceSiteSettingsView,
	RelayBalanceSiteSnapshot,
	RelayBalanceSiteStatus,
	UpdateRelayBalanceSiteInput,
} from "@/lib/types/relayBalance";
import { cn } from "@/lib/utils";

interface RelayBalanceBarProps {
	mode: "full" | "compact";
}

function formatCNY(val: number | null | undefined): string {
	if (val === null || val === undefined || Number.isNaN(val)) return "—";
	return `¥${val.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatUSD(val: number | null | undefined): string {
	if (val === null || val === undefined || Number.isNaN(val)) return "—";
	return `$${val.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatRelativeTime(iso?: string | null): string {
	if (!iso) return "尚未轮询";
	const ts = Date.parse(iso);
	if (Number.isNaN(ts)) return "—";
	const diffSec = Math.max(0, Math.floor((Date.now() - ts) / 1000));
	if (diffSec < 60) return "刚刚";
	const diffMin = Math.floor(diffSec / 60);
	if (diffMin < 60) return `${diffMin} 分钟前`;
	const diffHr = Math.floor(diffMin / 60);
	if (diffHr < 24) return `${diffHr} 小时前`;
	return new Date(ts).toLocaleString("zh-CN", {
		month: "2-digit",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
	});
}

function getStatusMeta(status: RelayBalanceSiteStatus) {
	switch (status) {
		case "urgent":
			return {
				label: "余额告急",
				shortLabel: "告急",
				badgeClass: "border border-rose-500/40 bg-rose-500/15 text-rose-700 dark:text-rose-300",
				cardClass: "border-rose-500/45 bg-rose-500/[0.04]",
				pillClass: "border-rose-500/45 bg-rose-500/10 text-rose-700 dark:text-rose-300",
				dotClass: "bg-rose-500",
			};
		case "low":
			return {
				label: "低于阈值",
				shortLabel: "偏低",
				badgeClass: "border border-amber-500/40 bg-amber-500/15 text-amber-700 dark:text-amber-300",
				cardClass: "border-amber-500/40 bg-amber-500/[0.04]",
				pillClass: "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300",
				dotClass: "bg-amber-500",
			};
		case "ok":
			return {
				label: "余额充裕",
				shortLabel: "充裕",
				badgeClass: "border border-emerald-500/30 bg-emerald-500/12 text-emerald-700 dark:text-emerald-300",
				cardClass: "border-border bg-card",
				pillClass: "border-emerald-500/30 bg-emerald-500/[0.06] text-foreground",
				dotClass: "bg-emerald-500",
			};
		case "unconfigured":
			return {
				label: "待配置令牌",
				shortLabel: "待配置",
				badgeClass: "border border-border bg-muted/70 text-muted-foreground",
				cardClass: "border-border/80 bg-muted/20",
				pillClass: "border-border bg-muted/40 text-muted-foreground",
				dotClass: "bg-muted-foreground",
			};
		case "error":
			return {
				label: "查询异常",
				shortLabel: "异常",
				badgeClass: "border border-destructive/40 bg-destructive/10 text-destructive",
				cardClass: "border-destructive/35 bg-destructive/[0.03]",
				pillClass: "border-destructive/40 bg-destructive/10 text-destructive",
				dotClass: "bg-destructive",
			};
		default:
			return {
				label: "等待巡检",
				shortLabel: "待检",
				badgeClass: "border border-border bg-muted/70 text-muted-foreground",
				cardClass: "border-border bg-card",
				pillClass: "border-border bg-muted/40 text-muted-foreground",
				dotClass: "bg-muted-foreground",
			};
	}
}

function formatKeySourceLabel(site: RelayBalanceSiteSnapshot): string {
	if (!site.key_source) {
		return site.needs_access_token ? "需系统访问令牌" : "自动复用系统密钥";
	}
	if (site.key_source.startsWith("auto:")) {
		return `自动复用 ${site.key_source.slice(5)}`;
	}
	if (site.key_source.startsWith("access_token")) {
		return site.user_id ? `系统令牌 · UID ${site.user_id}` : "系统访问令牌";
	}
	return site.key_source;
}

interface SiteFormDraft {
	id: string;
	name: string;
	kind: RelayBalanceSiteSettingsView["kind"];
	base_url: string;
	provider_prefix: string;
	quota_per_unit: number;
	cny_per_site_unit: string;
	threshold_cny: string;
	urgent_threshold_cny: string;
	enabled: boolean;
	user_id: string;
	access_token: string;
	has_access_token: boolean;
	access_token_source?: string;
	access_token_masked?: string;
}

function RelayBalanceSettingsDialog({
	open,
	onOpenChange,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const { data: settings, isLoading } = useGetRelayBalanceSettingsQuery(undefined, { skip: !open });
	const [updateSettings, { isLoading: isSaving }] = useUpdateRelayBalanceSettingsMutation();

	const [enabled, setEnabled] = useState(true);
	const [pollMinutes, setPollMinutes] = useState("10");
	const [barkEnabled, setBarkEnabled] = useState(true);
	const [barkGroup, setBarkGroup] = useState("bifrost-balance");
	const [barkURLInput, setBarkURLInput] = useState("");
	const [cooldownHours, setCooldownHours] = useState("6");
	const [urgentCooldownHours, setUrgentCooldownHours] = useState("2");
	const [quietHoursEnabled, setQuietHoursEnabled] = useState(true);
	const [quietStartHour, setQuietStartHour] = useState("23");
	const [quietEndHour, setQuietEndHour] = useState("8");
	const [sitesDraft, setSitesDraft] = useState<SiteFormDraft[]>([]);

	useEffect(() => {
		if (!settings || !open) return;
		setEnabled(settings.enabled);
		setPollMinutes(String(Math.max(1, Math.round(settings.poll_interval_seconds / 60))));
		setBarkEnabled(settings.bark_enabled);
		setBarkGroup(settings.bark_group || "bifrost-balance");
		setBarkURLInput("");
		setCooldownHours(String(settings.cooldown_hours));
		setUrgentCooldownHours(String(settings.urgent_cooldown_hours));
		setQuietHoursEnabled(settings.quiet_hours_enabled);
		setQuietStartHour(String(settings.quiet_start_hour));
		setQuietEndHour(String(settings.quiet_end_hour));
		setSitesDraft(
			settings.sites.map((s) => ({
				id: s.id,
				name: s.name,
				kind: s.kind,
				base_url: s.base_url,
				provider_prefix: s.provider_prefix,
				quota_per_unit: s.quota_per_unit,
				cny_per_site_unit: String(s.cny_per_site_unit),
				threshold_cny: String(s.threshold_cny),
				urgent_threshold_cny: String(s.urgent_threshold_cny),
				enabled: s.enabled,
				user_id: s.user_id || "",
				access_token: "",
				has_access_token: s.has_access_token,
				access_token_source: s.access_token_source,
				access_token_masked: s.access_token_masked,
			})),
		);
	}, [settings, open]);

	const updateSiteField = <K extends keyof SiteFormDraft>(idx: number, key: K, value: SiteFormDraft[K]) => {
		setSitesDraft((prev) => prev.map((item, i) => (i === idx ? { ...item, [key]: value } : item)));
	};

	const handleSave = async () => {
		const pollSec = Math.max(60, Math.round((Number(pollMinutes) || 10) * 60));
		const coolH = Math.max(1, Math.round(Number(cooldownHours) || 6));
		const urgentCoolH = Math.max(1, Math.round(Number(urgentCooldownHours) || 2));
		const qStart = Math.min(23, Math.max(0, Math.round(Number(quietStartHour) || 0)));
		const qEnd = Math.min(23, Math.max(0, Math.round(Number(quietEndHour) || 0)));

		const sitesPayload: UpdateRelayBalanceSiteInput[] = sitesDraft.map((s) => ({
			id: s.id,
			name: s.name,
			kind: s.kind,
			base_url: s.base_url,
			provider_prefix: s.provider_prefix,
			quota_per_unit: s.quota_per_unit,
			cny_per_site_unit: Number(s.cny_per_site_unit) || 1,
			threshold_cny: Math.max(0, Number(s.threshold_cny) || 0),
			urgent_threshold_cny: Math.max(0, Number(s.urgent_threshold_cny) || 0),
			enabled: s.enabled,
			user_id: s.user_id.trim(),
			...(s.access_token.trim() ? { access_token: s.access_token.trim() } : {}),
		}));

		try {
			await updateSettings({
				enabled,
				poll_interval_seconds: pollSec,
				timezone: settings?.timezone || "Asia/Shanghai",
				bark_enabled: barkEnabled,
				bark_group: barkGroup.trim() || "bifrost-balance",
				...(barkURLInput.trim() ? { bark_url: barkURLInput.trim() } : {}),
				cooldown_hours: coolH,
				urgent_cooldown_hours: urgentCoolH,
				quiet_hours_enabled: quietHoursEnabled,
				quiet_start_hour: qStart,
				quiet_end_hour: qEnd,
				sites: sitesPayload,
			}).unwrap();
			toast.success("余额监控与 Bark 告警设置已保存");
			onOpenChange(false);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-h-[88vh] max-w-3xl overflow-y-auto">
				<DialogHeader>
					<DialogTitle className="flex items-center gap-2 text-base">
						<Settings2 className="h-4 w-4" />
						上游余额监控与 Bark 告警设置
					</DialogTitle>
					<DialogDescription className="text-xs">
						所有阈值与折算均按实际充值人民币口径计算；敏感地址与令牌留空将保留现有配置（支持服务端环境变量兜底）。
					</DialogDescription>
				</DialogHeader>

				{isLoading ? (
					<div className="flex items-center justify-center py-10 text-sm text-muted-foreground">
						<Loader2 className="mr-2 h-4 w-4 animate-spin" />
						加载配置中...
					</div>
				) : (
					<div className="space-y-5 py-2 text-xs">
						{/* Global polling & Bark section */}
						<div className="grid gap-3 rounded-lg border p-3.5 sm:grid-cols-2">
							<div className="flex items-center justify-between gap-2 sm:col-span-2">
								<div>
									<div className="font-medium text-foreground">后台定时巡检与 Bark 低余额推送</div>
									<div className="text-muted-foreground">
										低于阈值触发首次推送；未充值期间遵循冷却间隔防刷屏，充值回升至阈值 110% 后自动解除告警状态。
									</div>
								</div>
								<div className="flex items-center gap-4">
									<label className="flex items-center gap-2">
										<span className="text-muted-foreground">巡检</span>
										<Switch checked={enabled} onCheckedChange={setEnabled} />
									</label>
									<label className="flex items-center gap-2">
										<span className="text-muted-foreground">Bark</span>
										<Switch checked={barkEnabled} onCheckedChange={setBarkEnabled} />
									</label>
								</div>
							</div>

							<div className="space-y-1 sm:col-span-2">
								<Label className="text-xs">
									Bark 推送地址{" "}
									{settings?.bark_configured && (
										<span className="font-normal text-emerald-600 dark:text-emerald-400">
											（已配置 · 来源 {settings.bark_source === "env" ? "环境变量" : "数据库"}：{settings.bark_url_masked}）
										</span>
									)}
								</Label>
								<Input
									type="password"
									value={barkURLInput}
									onChange={(e) => setBarkURLInput(e.target.value)}
									placeholder={
										settings?.bark_configured
											? "留空保持现有 Bark 地址不变；填写新地址可覆盖"
											: "https://api.day.app/<你的DeviceKey>"
									}
									className="h-8 text-xs font-mono"
								/>
							</div>

							<div className="space-y-1">
								<Label className="text-xs">Bark 分组 (group)</Label>
								<Input
									value={barkGroup}
									onChange={(e) => setBarkGroup(e.target.value)}
									className="h-8 text-xs font-mono"
								/>
							</div>

							<div className="space-y-1">
								<Label className="text-xs">巡检周期（分钟）</Label>
								<Input
									type="number"
									min={1}
									max={1440}
									value={pollMinutes}
									onChange={(e) => setPollMinutes(e.target.value)}
									className="h-8 text-xs tabular-nums"
								/>
							</div>

							<div className="space-y-1">
								<Label className="text-xs">常规低余额冷却（小时）</Label>
								<Input
									type="number"
									min={1}
									max={72}
									value={cooldownHours}
									onChange={(e) => setCooldownHours(e.target.value)}
									className="h-8 text-xs tabular-nums"
								/>
							</div>

							<div className="space-y-1">
								<Label className="text-xs">告急冷却（小时）</Label>
								<Input
									type="number"
									min={1}
									max={24}
									value={urgentCooldownHours}
									onChange={(e) => setUrgentCooldownHours(e.target.value)}
									className="h-8 text-xs tabular-nums"
								/>
							</div>

							<div className="flex flex-wrap items-center justify-between gap-2 border-t pt-2.5 sm:col-span-2">
								<label className="flex items-center gap-2 font-medium">
									<Switch checked={quietHoursEnabled} onCheckedChange={setQuietHoursEnabled} />
									<span>夜间免打扰（静默时段延后至次晨统一推送）</span>
								</label>
								<div className="flex items-center gap-1.5">
									<Input
										type="number"
										min={0}
										max={23}
										value={quietStartHour}
										onChange={(e) => setQuietStartHour(e.target.value)}
										disabled={!quietHoursEnabled}
										className="h-7 w-16 text-center text-xs tabular-nums"
									/>
									<span className="text-muted-foreground">:00 至</span>
									<Input
										type="number"
										min={0}
										max={23}
										value={quietEndHour}
										onChange={(e) => setQuietEndHour(e.target.value)}
										disabled={!quietHoursEnabled}
										className="h-7 w-16 text-center text-xs tabular-nums"
									/>
									<span className="text-muted-foreground">:00</span>
								</div>
							</div>
						</div>

						{/* Per-site configuration */}
						<div className="space-y-2.5">
							<div className="font-medium text-foreground">站点折算比例、预警阈值与凭证</div>
							{sitesDraft.map((site, idx) => (
								<div key={site.id} className="space-y-2.5 rounded-lg border p-3">
									<div className="flex flex-wrap items-center justify-between gap-2">
										<div className="flex items-center gap-2">
											<Switch
												checked={site.enabled}
												onCheckedChange={(v) => updateSiteField(idx, "enabled", v)}
											/>
											<span className="font-semibold text-foreground">{site.name}</span>
											<span className="text-muted-foreground font-mono text-[11px]">{site.base_url}</span>
										</div>
										<span className="rounded bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
											{site.kind === "new_api_user_self" ? "系统访问令牌" : `自动复用 ${site.provider_prefix}* 密钥`}
										</span>
									</div>

									<div className="grid gap-2.5 sm:grid-cols-3">
										<div className="space-y-1">
											<Label className="text-[11px] text-muted-foreground">充值折算系数（¥ / 站内$1）</Label>
											<Input
												type="number"
												step="0.001"
												value={site.cny_per_site_unit}
												onChange={(e) => updateSiteField(idx, "cny_per_site_unit", e.target.value)}
												className="h-8 text-xs font-mono tabular-nums"
											/>
										</div>
										<div className="space-y-1">
											<Label className="text-[11px] text-muted-foreground">预警阈值（¥）</Label>
											<Input
												type="number"
												step="1"
												value={site.threshold_cny}
												onChange={(e) => updateSiteField(idx, "threshold_cny", e.target.value)}
												className="h-8 text-xs font-mono tabular-nums"
											/>
										</div>
										<div className="space-y-1">
											<Label className="text-[11px] text-muted-foreground">告急阈值（¥）</Label>
											<Input
												type="number"
												step="1"
												value={site.urgent_threshold_cny}
												onChange={(e) => updateSiteField(idx, "urgent_threshold_cny", e.target.value)}
												className="h-8 text-xs font-mono tabular-nums"
											/>
										</div>
									</div>

									{site.kind === "new_api_user_self" && (
										<div className="grid gap-2.5 border-t pt-2.5 sm:grid-cols-3">
											<div className="space-y-1">
												<Label className="text-[11px] text-muted-foreground">New-Api-User (UID)</Label>
												<Input
													value={site.user_id}
													onChange={(e) => updateSiteField(idx, "user_id", e.target.value)}
													placeholder="例如 10001"
													className="h-8 text-xs font-mono"
												/>
											</div>
											<div className="space-y-1 sm:col-span-2">
												<Label className="text-[11px] text-muted-foreground">
													系统访问令牌{" "}
													{site.has_access_token && (
														<span className="text-emerald-600 dark:text-emerald-400">
															（已配置 · {site.access_token_source === "env" ? "环境变量" : "数据库"}：{site.access_token_masked}）
														</span>
													)}
												</Label>
												<Input
													type="password"
													value={site.access_token}
													onChange={(e) => updateSiteField(idx, "access_token", e.target.value)}
													placeholder={site.has_access_token ? "留空保持现有令牌不变" : "粘贴系统访问令牌"}
													className="h-8 text-xs font-mono"
												/>
											</div>
										</div>
									)}
								</div>
							))}
						</div>
					</div>
				)}

				<DialogFooter>
					<Button type="button" variant="outline" size="sm" onClick={() => onOpenChange(false)}>
						取消
					</Button>
					<Button type="button" size="sm" onClick={handleSave} disabled={isSaving || isLoading}>
						{isSaving ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : null}
						保存设置
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

export function RelayBalanceBar({ mode }: RelayBalanceBarProps) {
	const [settingsOpen, setSettingsOpen] = useState(false);

	const { data, isLoading, isFetching } = useGetRelayBalancesQuery(undefined, {
		pollingInterval: 60_000,
		refetchOnFocus: true,
	});
	const [refreshBalances, { isLoading: isRefreshing }] = useRefreshRelayBalancesMutation();
	const [testBark, { isLoading: isTestingBark }] = useTestRelayBalanceBarkMutation();

	const handleRefresh = async () => {
		try {
			await refreshBalances({ force: true }).unwrap();
			toast.success("已刷新 4 家中转站实时余额");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleTestBark = async () => {
		try {
			const res = await testBark().unwrap();
			toast.success(res.message || "测试通知已发送至 Bark");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const sites = data?.sites ?? [];
	const enabledSites = sites.filter((s) => s.enabled);
	const hasAlert = (data?.urgent_count ?? 0) > 0 || (data?.low_count ?? 0) > 0;

	if (mode === "compact") {
		return (
			<>
				<div
					className={cn(
						"flex flex-col gap-2.5 rounded-lg border px-4 py-2.5 text-xs lg:flex-row lg:items-center lg:justify-between",
						(data?.urgent_count ?? 0) > 0
							? "border-rose-500/40 bg-rose-500/[0.04]"
							: (data?.low_count ?? 0) > 0
								? "border-amber-500/40 bg-amber-500/[0.04]"
								: "border-border bg-card",
					)}
					data-testid="relay-balance-compact-strip"
				>
					<div className="flex flex-wrap items-center gap-2">
						<div className="flex items-center gap-1.5 font-medium text-foreground">
							<Wallet className="h-4 w-4 text-muted-foreground" />
							<span>上游余额</span>
							<span className="font-mono font-semibold tabular-nums">
								{isLoading ? "加载中..." : formatCNY(data?.total_balance_cny ?? 0)}
							</span>
						</div>

						<span className="text-muted-foreground hidden sm:inline">·</span>

						<div className="flex flex-wrap items-center gap-1.5">
							{enabledSites.map((site) => {
								const meta = getStatusMeta(site.status);
								const runway =
									site.estimated_days_left !== null && site.estimated_days_left !== undefined
										? ` · 约 ${site.estimated_days_left.toFixed(1)} 天`
										: "";
								const tooltip = `${site.name}：真实余额 ${formatCNY(site.balance_cny)}（站内 ${formatUSD(site.site_balance_usd)} × ${site.cny_per_site_unit}），24h 消耗 ${formatCNY(site.spend_24h_cny)}${runway}`;
								return (
									<span
										key={site.id}
										title={tooltip}
										className={cn(
											"inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-[11px] font-medium tabular-nums",
											meta.pillClass,
										)}
									>
										<span className={cn("h-1.5 w-1.5 rounded-full", meta.dotClass)} />
										<span>{site.name.replace("（云雾）", "")}</span>
										<span className="font-mono font-semibold">
											{site.balance_cny !== null && site.balance_cny !== undefined
												? formatCNY(site.balance_cny)
												: meta.shortLabel}
										</span>
									</span>
								);
							})}
						</div>
					</div>

					<div className="flex shrink-0 items-center gap-1.5">
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-foreground"
							onClick={handleRefresh}
							disabled={isRefreshing || isFetching}
						>
							<RefreshCw className={cn("h-3.5 w-3.5", (isRefreshing || isFetching) && "animate-spin")} />
							刷新
						</Button>
						<Button
							type="button"
							variant="ghost"
							size="sm"
							className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-foreground"
							onClick={() => setSettingsOpen(true)}
						>
							<Settings2 className="h-3.5 w-3.5" />
							告警
						</Button>
						<Link
							href="/workspace/adaptive-routing"
							className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
						>
							<span>详情</span>
							<ExternalLink className="h-3 w-3" />
						</Link>
					</div>
				</div>
				<RelayBalanceSettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} />
			</>
		);
	}

	// Full 4-card panel for /workspace/adaptive-routing
	return (
		<>
			<div className="border-border bg-card space-y-3 rounded-xl border p-4" data-testid="relay-balance-full-panel">
				{/* Panel Header */}
				<div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
					<div className="flex flex-wrap items-center gap-2">
						<div className="flex items-center gap-2">
							<Wallet className="h-4 w-4 text-primary" />
							<h2 className="text-sm font-semibold tracking-tight">上游中转站余额监控</h2>
						</div>
						<span className="rounded-full border border-border bg-muted/50 px-2.5 py-0.5 font-mono text-xs font-semibold tabular-nums">
							合计 {isLoading ? "..." : formatCNY(data?.total_balance_cny ?? 0)}
						</span>
						{(data?.urgent_count ?? 0) > 0 ? (
							<span className="inline-flex items-center gap-1 rounded-full border border-rose-500/40 bg-rose-500/15 px-2.5 py-0.5 text-xs font-medium text-rose-700 dark:text-rose-300">
								<AlertTriangle className="h-3 w-3" />
								{data?.urgent_count} 家余额告急
							</span>
						) : (data?.low_count ?? 0) > 0 ? (
							<span className="inline-flex items-center gap-1 rounded-full border border-amber-500/40 bg-amber-500/15 px-2.5 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300">
								<AlertTriangle className="h-3 w-3" />
								{data?.low_count} 家低于阈值
							</span>
						) : !isLoading && enabledSites.length > 0 ? (
							<span className="inline-flex items-center gap-1 rounded-full border border-emerald-500/30 bg-emerald-500/12 px-2.5 py-0.5 text-xs font-medium text-emerald-700 dark:text-emerald-300">
								<CheckCircle2 className="h-3 w-3" />
								余额充裕
							</span>
						) : null}
						<span className="text-muted-foreground text-xs">
							{data?.bark_enabled && data?.bark_configured ? (
								<span className="inline-flex items-center gap-1">
									<Bell className="h-3 w-3 text-emerald-500" />
									Bark 预警已启用
								</span>
							) : (
								<span className="inline-flex items-center gap-1">
									<BellOff className="h-3 w-3" />
									Bark 未启用
								</span>
							)}
							{" · "}最近更新：{formatRelativeTime(data?.last_poll_at)}
						</span>
					</div>

					<div className="flex flex-wrap items-center gap-1.5">
						<Button
							type="button"
							variant="outline"
							size="sm"
							className="h-7 gap-1.5 text-xs"
							onClick={handleRefresh}
							disabled={isRefreshing || isFetching}
							data-testid="relay-balance-refresh-btn"
						>
							<RefreshCw className={cn("h-3.5 w-3.5", (isRefreshing || isFetching) && "animate-spin")} />
							刷新余额
						</Button>
						<Button
							type="button"
							variant="outline"
							size="sm"
							className="h-7 gap-1.5 text-xs"
							onClick={handleTestBark}
							disabled={isTestingBark || !data?.bark_configured}
							title={data?.bark_configured ? "向 Bark 发送一条当前 4 站余额摘要测试通知" : "请先配置 Bark 推送地址"}
							data-testid="relay-balance-test-bark-btn"
						>
							{isTestingBark ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Bell className="h-3.5 w-3.5" />}
							发测试推送
						</Button>
						<Button
							type="button"
							variant={hasAlert ? "default" : "outline"}
							size="sm"
							className="h-7 gap-1.5 text-xs"
							onClick={() => setSettingsOpen(true)}
							data-testid="relay-balance-settings-btn"
						>
							<Settings2 className="h-3.5 w-3.5" />
							告警设置
						</Button>
					</div>
				</div>

				{/* 4 Site Cards */}
				<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
					{sites.map((site) => {
						const meta = getStatusMeta(site.status);
						return (
							<div
								key={site.id}
								className={cn(
									"flex flex-col justify-between gap-2.5 rounded-lg border p-3.5 transition-colors",
									meta.cardClass,
									!site.enabled && "opacity-60",
								)}
								data-testid={`relay-balance-card-${site.id}`}
							>
								<div className="flex items-start justify-between gap-2">
									<div className="min-w-0">
										<div className="flex items-center gap-1.5">
											<span className="truncate text-sm font-semibold text-foreground">{site.name}</span>
										</div>
										<div className="text-muted-foreground mt-0.5 truncate font-mono text-[11px]">
											折算 ×{site.cny_per_site_unit} · 阈值 ¥{site.threshold_cny}
										</div>
									</div>
									<span
										className={cn(
											"inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium",
											meta.badgeClass,
										)}
									>
										<span className={cn("h-1.5 w-1.5 rounded-full", meta.dotClass)} />
										{site.enabled ? meta.label : "已停用"}
									</span>
								</div>

								<div>
									<div className="flex items-baseline gap-2">
										<span className="font-mono text-2xl font-bold tracking-tight tabular-nums text-foreground">
											{formatCNY(site.balance_cny)}
										</span>
										{site.site_balance_usd !== null && site.site_balance_usd !== undefined && (
											<span className="text-muted-foreground font-mono text-xs tabular-nums">
												站内 {formatUSD(site.site_balance_usd)}
											</span>
										)}
									</div>
									<div className="text-muted-foreground mt-1 text-xs tabular-nums">
										近 24h 消耗 {formatCNY(site.spend_24h_cny)}
										{site.estimated_days_left !== null && site.estimated_days_left !== undefined
											? ` · 约可撑 ${site.estimated_days_left >= 99 ? "99+" : site.estimated_days_left.toFixed(1)} 天`
											: " · 近 24h 无显著消耗"}
									</div>
								</div>

								<div className="flex items-center justify-between gap-2 border-t border-border/60 pt-2 text-[11px] text-muted-foreground">
									<span className="truncate" title={site.key_source || ""}>
										{formatKeySourceLabel(site)}
									</span>
									{site.last_error ? (
										<span
											className="inline-flex shrink-0 items-center gap-1 text-destructive"
											title={site.last_error}
										>
											<XCircle className="h-3 w-3" />
											{site.status === "unconfigured" ? "待填令牌" : "上次报错"}
										</span>
									) : site.pending_after_quiet ? (
										<span className="shrink-0 text-amber-600 dark:text-amber-400">免打扰待推</span>
									) : site.alert_active && site.last_alert_at ? (
										<span className="shrink-0 text-rose-600 dark:text-rose-400" title={`上次推送：${site.last_alert_at}`}>
											已推送 {formatRelativeTime(site.last_alert_at)}
										</span>
									) : (
										<span className="shrink-0">{formatRelativeTime(site.last_success_at)}</span>
									)}
								</div>
							</div>
						);
					})}
				</div>
			</div>

			<RelayBalanceSettingsDialog open={settingsOpen} onOpenChange={setSettingsOpen} />
		</>
	);
}
