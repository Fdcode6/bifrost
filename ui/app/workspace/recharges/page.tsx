"use client";

import { useEffect, useMemo, useState } from "react";
import {
	AlertCircle,
	CheckCircle2,
	ExternalLink,
	Loader2,
	Pencil,
	Plus,
	RefreshCw,
	Trash2,
	Wallet,
	WalletCards,
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
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
	getErrorMessage,
	useCreateRelayRechargeMutation,
	useDeleteRelayRechargeMutation,
	useGetRelayRechargesQuery,
	useSyncRelayRechargesMutation,
	useUpdateRelayRechargeMutation,
} from "@/lib/store";
import type {
	RelayRechargeRecord,
	RelayRechargeSiteMeta,
	RelayRechargeSiteSummary,
	RelayRechargeSource,
	RelayRechargeStatus,
	RelayRechargeSyncMode,
} from "@/lib/types/relayBalance";
import { cn } from "@/lib/utils";

type DatePreset = "all" | "month" | "30d" | "90d";

const DATE_PRESETS: { key: DatePreset; label: string }[] = [
	{ key: "all", label: "全部时间" },
	{ key: "month", label: "本月" },
	{ key: "30d", label: "近 30 天" },
	{ key: "90d", label: "近 90 天" },
];

function formatCNY(v: number): string {
	return `¥${v.toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatUSD(v: number): string {
	return `$${v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

function formatDateTime(iso?: string | null): string {
	if (!iso) return "—";
	const d = new Date(iso);
	if (Number.isNaN(d.getTime())) return "—";
	return d.toLocaleString("zh-CN", {
		year: "numeric",
		month: "2-digit",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
		hour12: false,
	});
}

function toLocalDateInput(d: Date): string {
	const yyyy = d.getFullYear();
	const mm = String(d.getMonth() + 1).padStart(2, "0");
	const dd = String(d.getDate()).padStart(2, "0");
	return `${yyyy}-${mm}-${dd}`;
}

function toLocalDateTimeInput(iso?: string | null): string {
	const d = iso ? new Date(iso) : new Date();
	if (Number.isNaN(d.getTime())) return "";
	const yyyy = d.getFullYear();
	const mm = String(d.getMonth() + 1).padStart(2, "0");
	const dd = String(d.getDate()).padStart(2, "0");
	const hh = String(d.getHours()).padStart(2, "0");
	const mi = String(d.getMinutes()).padStart(2, "0");
	return `${yyyy}-${mm}-${dd}T${hh}:${mi}`;
}

function getDateRangeForPreset(preset: DatePreset): { start_date?: string; end_date?: string } {
	if (preset === "all") return {};
	const now = new Date();
	const end_date = toLocalDateInput(now);
	if (preset === "month") {
		const start = new Date(now.getFullYear(), now.getMonth(), 1);
		return { start_date: toLocalDateInput(start), end_date };
	}
	const days = preset === "30d" ? 30 : 90;
	const start = new Date(now.getTime() - (days - 1) * 86400_000);
	return { start_date: toLocalDateInput(start), end_date };
}

function getSyncModeLabel(mode: RelayRechargeSyncMode): { label: string; className: string } {
	switch (mode) {
		case "api_topup":
			return {
				label: "官方订单同步",
				className: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
			};
		case "api_log":
			return {
				label: "官方日志同步",
				className: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300",
			};
		case "balance_delta":
			return {
				label: "余额跳增自动记",
				className: "border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300",
			};
		default:
			return {
				label: "待配同步令牌",
				className: "border-border bg-muted/60 text-muted-foreground",
			};
	}
}

function formatPaymentMethod(method?: string): string {
	if (!method) return "—";
	const lower = method.toLowerCase();
	switch (lower) {
		case "alipay":
			return "支付宝";
		case "wxpay":
		case "wechat":
			return "微信支付";
		case "stripe":
		case "strap":
			return "Stripe";
		case "antom":
			return "Antom";
		case "sunrate":
			return "Sunrate";
		case "card":
			return "银行卡 / 网关";
		case "usdt":
			return "USDT";
		case "online":
			return "在线充值";
		case "auto_detected":
			return "自动捕获";
		case "manual":
			return "手工记账";
		default:
			return method;
	}
}

function getSourceBadge(source: RelayRechargeSource, status: RelayRechargeStatus) {
	if (status === "pending_confirm") {
		return {
			label: "自动捕获 · 待核对",
			className: "border border-amber-500/40 bg-amber-500/15 text-amber-700 dark:text-amber-300",
		};
	}
	switch (source) {
		case "api_sync":
			return {
				label: "官方同步",
				className: "border border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
			};
		case "balance_delta":
			return {
				label: "自动捕获 · 已核对",
				className: "border border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300",
			};
		default:
			return {
				label: "手工记账",
				className: "border border-border bg-muted/60 text-muted-foreground",
			};
	}
}

function RechargeRecordDialog({
	open,
	onOpenChange,
	sites,
	editing,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	sites: RelayRechargeSiteMeta[];
	editing: RelayRechargeRecord | null;
}) {
	const [createRecord, { isLoading: isCreating }] = useCreateRelayRechargeMutation();
	const [updateRecord, { isLoading: isUpdating }] = useUpdateRelayRechargeMutation();

	const [siteId, setSiteId] = useState("bai");
	const [rechargedAt, setRechargedAt] = useState("");
	const [siteAmountUSD, setSiteAmountUSD] = useState("");
	const [paidCNY, setPaidCNY] = useState("");
	const [paidCurrency, setPaidCurrency] = useState("CNY");
	const [paidAmount, setPaidAmount] = useState("");
	const [paymentMethod, setPaymentMethod] = useState("alipay");
	const [externalId, setExternalId] = useState("");
	const [note, setNote] = useState("");

	useEffect(() => {
		if (!open) return;
		if (editing) {
			setSiteId(editing.site_id);
			setRechargedAt(toLocalDateTimeInput(editing.recharged_at));
			setSiteAmountUSD(editing.site_amount_usd ? String(Number(editing.site_amount_usd.toFixed(4))) : "");
			setPaidCNY(editing.paid_cny ? String(Number(editing.paid_cny.toFixed(2))) : "");
			setPaidCurrency(editing.paid_currency || "CNY");
			setPaidAmount(editing.paid_amount ? String(Number(editing.paid_amount.toFixed(4))) : "");
			setPaymentMethod(editing.payment_method || "manual");
			setExternalId(editing.external_id || "");
			setNote(editing.note || "");
		} else {
			setSiteId(sites[0]?.id || "bai");
			setRechargedAt(toLocalDateTimeInput(null));
			setSiteAmountUSD("");
			setPaidCNY("");
			setPaidCurrency("CNY");
			setPaidAmount("");
			setPaymentMethod("alipay");
			setExternalId("");
			setNote("");
		}
	}, [open, editing, sites]);

	const selectedSite = useMemo(() => sites.find((s) => s.id === siteId), [sites, siteId]);

	const handleSubmit = async () => {
		const usd = Number(siteAmountUSD) || 0;
		const cny = Number(paidCNY) || 0;
		if (usd <= 0 && cny <= 0) {
			toast.error("请至少填写到账额度或实付人民币金额");
			return;
		}
		const computedCNY = cny > 0 ? cny : usd * (selectedSite?.cny_per_site_unit || 1);
		const computedUSD = usd > 0 ? usd : selectedSite && selectedSite.cny_per_site_unit > 0 ? computedCNY / selectedSite.cny_per_site_unit : computedCNY;

		try {
			if (editing) {
				await updateRecord({
					id: editing.id,
					recharged_at: rechargedAt || undefined,
					site_amount_usd: computedUSD,
					paid_cny: computedCNY,
					paid_currency: paidCurrency,
					paid_amount: Number(paidAmount) > 0 ? Number(paidAmount) : computedCNY,
					payment_method: paymentMethod.trim() || "manual",
					status: "success",
					external_id: externalId.trim(),
					note: note.trim(),
				}).unwrap();
				toast.success(editing.status === "pending_confirm" ? "已确认并更新实付金额" : "充值记录已更新");
			} else {
				await createRecord({
					site_id: siteId,
					recharged_at: rechargedAt || undefined,
					site_amount_usd: computedUSD,
					paid_cny: computedCNY,
					paid_currency: paidCurrency,
					paid_amount: Number(paidAmount) > 0 ? Number(paidAmount) : computedCNY,
					payment_method: paymentMethod.trim() || "manual",
					status: "success",
					external_id: externalId.trim(),
					note: note.trim(),
				}).unwrap();
				toast.success("已记录充值流水");
			}
			onOpenChange(false);
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const isBusy = isCreating || isUpdating;

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="max-w-lg">
				<DialogHeader>
					<DialogTitle className="text-base">
						{editing ? (editing.status === "pending_confirm" ? "核对并确认自动捕获的充值" : "编辑充值记录") : "手工记一笔充值"}
					</DialogTitle>
					<DialogDescription className="text-xs">
						{editing
							? "可按实际支付账单修正人民币实付金额（含支付网关手续费）和备注，保存后将标记为已核对。"
							: "支持为 B.AI 或任意站点补录历史 / 线下充值记录，与官方同步流水统一汇总对账。"}
					</DialogDescription>
				</DialogHeader>

				<div className="grid gap-3 py-2 text-xs sm:grid-cols-2">
					<div className="space-y-1">
						<Label className="text-xs">中转平台</Label>
						<Select value={siteId} onValueChange={setSiteId} disabled={Boolean(editing)}>
							<SelectTrigger className="h-8 text-xs">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								{sites.map((s) => (
									<SelectItem key={s.id} value={s.id}>
										{s.name}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>

					<div className="space-y-1">
						<Label className="text-xs">充值时间</Label>
						<Input
							type="datetime-local"
							value={rechargedAt}
							onChange={(e) => setRechargedAt(e.target.value)}
							className="h-8 text-xs font-mono"
						/>
					</div>

					<div className="space-y-1">
						<Label className="text-xs">到账站内额度（$）</Label>
						<Input
							type="number"
							step="0.01"
							placeholder="例如 50"
							value={siteAmountUSD}
							onChange={(e) => {
								const val = e.target.value;
								setSiteAmountUSD(val);
								if (!editing && val && !paidCNY && selectedSite) {
									const est = Number(val) * selectedSite.cny_per_site_unit;
									if (!Number.isNaN(est) && est > 0) setPaidCNY(est.toFixed(2));
								}
							}}
							className="h-8 text-xs font-mono tabular-nums"
						/>
					</div>

					<div className="space-y-1">
						<Label className="text-xs">实际支付人民币（¥，含手续费）</Label>
						<Input
							type="number"
							step="0.01"
							placeholder="例如 345.00"
							value={paidCNY}
							onChange={(e) => setPaidCNY(e.target.value)}
							className="h-8 text-xs font-mono tabular-nums"
						/>
					</div>

					<div className="space-y-1">
						<Label className="text-xs">支付方式</Label>
						<Select value={paymentMethod} onValueChange={setPaymentMethod}>
							<SelectTrigger className="h-8 text-xs">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="alipay">支付宝</SelectItem>
								<SelectItem value="wechat">微信支付</SelectItem>
								<SelectItem value="card">银行卡 / 网关</SelectItem>
								<SelectItem value="stripe">Stripe</SelectItem>
								<SelectItem value="antom">Antom</SelectItem>
								<SelectItem value="usdt">USDT</SelectItem>
								<SelectItem value="manual">手工记账</SelectItem>
							</SelectContent>
						</Select>
					</div>

					<div className="space-y-1">
						<Label className="text-xs">外部订单号（可选）</Label>
						<Input
							value={externalId}
							onChange={(e) => setExternalId(e.target.value)}
							placeholder="例如 USR..."
							className="h-8 text-xs font-mono"
						/>
					</div>

					<div className="space-y-1 sm:col-span-2">
						<Label className="text-xs">备注（可选）</Label>
						<Input
							value={note}
							onChange={(e) => setNote(e.target.value)}
							placeholder="例如：含网关手续费 $0.85 / 线下对公转账"
							className="h-8 text-xs"
						/>
					</div>
				</div>

				<DialogFooter>
					<Button type="button" variant="outline" size="sm" onClick={() => onOpenChange(false)}>
						取消
					</Button>
					<Button type="button" size="sm" onClick={handleSubmit} disabled={isBusy}>
						{isBusy ? <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" /> : null}
						{editing ? "保存并确认" : "保存记录"}
					</Button>
				</DialogFooter>
			</DialogContent>
		</Dialog>
	);
}

export default function RelayRechargesPage() {
	const [selectedSite, setSelectedSite] = useState<string>("all");
	const [datePreset, setDatePreset] = useState<DatePreset>("all");
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editingRecord, setEditingRecord] = useState<RelayRechargeRecord | null>(null);

	const dateRange = useMemo(() => getDateRangeForPreset(datePreset), [datePreset]);

	// Query summary across all sites for the chosen date range so the 4 site cards stay visible when filtering one site.
	const { data: allSitesData } = useGetRelayRechargesQuery({
		site_id: "all",
		...dateRange,
		limit: 200,
	});

	const { data, isLoading, isFetching } = useGetRelayRechargesQuery({
		site_id: selectedSite,
		...dateRange,
		limit: 500,
	});

	const [syncRecharges, { isLoading: isSyncing }] = useSyncRelayRechargesMutation();
	const [deleteRecharge] = useDeleteRelayRechargeMutation();

	const sites = data?.sites ?? allSitesData?.sites ?? [];
	const items = data?.items ?? [];
	const summary = data?.summary;

	const siteSummaryMap = useMemo(() => {
		const map = new Map<string, RelayRechargeSiteSummary>();
		for (const row of allSitesData?.summary?.by_site ?? summary?.by_site ?? []) {
			map.set(row.site_id, row);
		}
		return map;
	}, [allSitesData?.summary?.by_site, summary?.by_site]);

	const handleSync = async () => {
		try {
			const res = await syncRecharges().unwrap();
			const errSites = res.results.filter((r) => r.error && r.sync_mode !== "balance_delta");
			if (errSites.length > 0) {
				toast.warning(
					`同步完成（新增 ${res.total_inserted} 笔），${errSites.map((e) => `${e.site_name}: ${e.error}`).join("；")}`,
				);
			} else {
				toast.success(`官方充值记录同步完成，本次新增 ${res.total_inserted} 笔`);
			}
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	const handleDelete = async (rec: RelayRechargeRecord) => {
		if (!window.confirm(`确定删除 ${rec.site_name} 的这笔 ${formatCNY(rec.paid_cny)} 充值记录吗？`)) {
			return;
		}
		try {
			await deleteRecharge(rec.id).unwrap();
			toast.success("充值记录已删除");
		} catch (err) {
			toast.error(getErrorMessage(err));
		}
	};

	return (
		<div className="mx-auto w-full max-w-[1480px] space-y-5 pb-8" data-testid="relay-recharges-page">
			{/* Header */}
			<div className="border-border flex flex-col gap-3 border-b pb-4 lg:flex-row lg:items-center lg:justify-between">
				<div className="space-y-1">
					<div className="flex flex-wrap items-center gap-2">
						<WalletCards className="h-5 w-5 text-primary" />
						<h1 className="text-xl font-semibold tracking-tight">上游充值记录与对账</h1>
						{data?.last_sync_at ? (
							<span className="border-border bg-muted/60 text-muted-foreground rounded-full border px-2.5 py-0.5 text-xs">
								最近同步：{formatDateTime(data.last_sync_at)}
							</span>
						) : null}
					</div>
					<p className="text-muted-foreground text-xs">
						StoreAPI / PoloAPI / OpenLux 通过官方只读访问令牌每小时自动拉取并永久落库；B.AI 从今日起由余额巡检自动捕获正向跳增（≥ $5）并支持手工核对含手续费的实付人民币。
					</p>
				</div>

				<div className="flex flex-wrap items-center gap-2">
					<Button
						type="button"
						variant="outline"
						size="sm"
						className="h-8 gap-1.5 text-xs"
						onClick={handleSync}
						disabled={isSyncing || isFetching}
						data-testid="relay-recharges-sync-btn"
					>
						<RefreshCw className={cn("h-3.5 w-3.5", (isSyncing || isFetching) && "animate-spin")} />
						同步官方记录
					</Button>
					<Button
						type="button"
						size="sm"
						className="h-8 gap-1.5 text-xs"
						onClick={() => {
							setEditingRecord(null);
							setDialogOpen(true);
						}}
						data-testid="relay-recharges-add-btn"
					>
						<Plus className="h-3.5 w-3.5" />
						手工记一笔
					</Button>
				</div>
			</div>

			{/* Top 4 KPI Cards */}
			<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
				<div className="border-border bg-card rounded-xl border p-4">
					<div className="text-muted-foreground flex items-center justify-between text-xs">
						<span>当前筛选 · 累计实付人民币</span>
						<Wallet className="h-4 w-4" />
					</div>
					<div className="mt-2 font-mono text-2xl font-bold tabular-nums">
						{isLoading ? "加载中..." : formatCNY(summary?.total_paid_cny ?? 0)}
					</div>
					<div className="text-muted-foreground mt-1 text-xs">按各站真实充值支付金额汇总</div>
				</div>

				<div className="border-border bg-card rounded-xl border p-4">
					<div className="text-muted-foreground text-xs">当前筛选 · 累计到账站内额度</div>
					<div className="mt-2 font-mono text-2xl font-bold tabular-nums">
						{isLoading ? "加载中..." : formatUSD(summary?.total_site_usd ?? 0)}
					</div>
					<div className="text-muted-foreground mt-1 text-xs">4 家中转站合计入账配额（$）</div>
				</div>

				<div className="border-border bg-card rounded-xl border p-4">
					<div className="text-muted-foreground text-xs">充值笔数</div>
					<div className="mt-2 font-mono text-2xl font-bold tabular-nums">
						{isLoading ? "—" : `${summary?.total_count ?? 0} 笔`}
					</div>
					<div className="text-muted-foreground mt-1 text-xs">已自动去重并永久保存在本地账本</div>
				</div>

				<div
					className={cn(
						"rounded-xl border p-4",
						(summary?.pending_count ?? 0) > 0
							? "border-amber-500/40 bg-amber-500/[0.04]"
							: "border-border bg-card",
					)}
				>
					<div className="text-muted-foreground flex items-center justify-between text-xs">
						<span>待核对记录（自动捕获）</span>
						{(summary?.pending_count ?? 0) > 0 ? (
							<AlertCircle className="h-4 w-4 text-amber-500" />
						) : (
							<CheckCircle2 className="h-4 w-4 text-emerald-500" />
						)}
					</div>
					<div className="mt-2 font-mono text-2xl font-bold tabular-nums">
						{isLoading ? "—" : `${summary?.pending_count ?? 0} 笔`}
					</div>
					<div className="text-muted-foreground mt-1 text-xs">
						{(summary?.pending_count ?? 0) > 0
							? "点击下方「编辑」核对含手续费的真实实付"
							: "所有充值记录均已核对确认"}
					</div>
				</div>
			</div>

			{/* 4 Site Summary Cards (click to filter) */}
			<div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
				{sites.map((site) => {
					const stat = siteSummaryMap.get(site.id);
					const active = selectedSite === site.id;
					const syncMeta = getSyncModeLabel(site.sync_mode);
					return (
						<div
							key={site.id}
							role="button"
							tabIndex={0}
							onClick={() => setSelectedSite(active ? "all" : site.id)}
							onKeyDown={(e) => {
								if (e.key === "Enter" || e.key === " ") {
									e.preventDefault();
									setSelectedSite(active ? "all" : site.id);
								}
							}}
							data-testid={`relay-recharge-site-card-${site.id}`}
							className={cn(
								"flex cursor-pointer flex-col justify-between gap-2.5 rounded-xl border p-3.5 text-left transition-all",
								active
									? "border-primary bg-primary/[0.04] ring-1 ring-primary/30"
									: "border-border bg-card hover:border-foreground/25",
							)}
						>
							<div className="flex items-start justify-between gap-2">
								<div className="min-w-0">
									<div className="truncate text-sm font-semibold">{site.name}</div>
									<span
										className={cn(
											"mt-1 inline-block rounded-full border px-2 py-0.5 text-[10px] font-medium",
											syncMeta.className,
										)}
									>
										{syncMeta.label}
									</span>
								</div>
								{site.recharge_url ? (
									<a
										href={site.recharge_url}
										target="_blank"
										rel="noopener noreferrer"
										onClick={(e) => e.stopPropagation()}
										className="border-border bg-background/80 text-muted-foreground hover:text-foreground inline-flex shrink-0 items-center gap-1 rounded-md border px-2 py-0.5 text-[11px] font-medium"
									>
										<span>去充值</span>
										<ExternalLink className="h-3 w-3" />
									</a>
								) : null}
							</div>

							<div>
								<div className="flex items-baseline gap-2">
									<span className="font-mono text-xl font-bold tabular-nums">
										{formatCNY(stat?.total_paid_cny ?? 0)}
									</span>
									<span className="text-muted-foreground font-mono text-xs tabular-nums">
										到账 {formatUSD(stat?.total_site_usd ?? 0)}
									</span>
								</div>
								<div className="text-muted-foreground mt-1 flex items-center justify-between text-[11px] tabular-nums">
									<span>{stat?.record_count ?? 0} 笔记录</span>
									<span>最近：{stat?.last_recharged_at ? formatDateTime(stat.last_recharged_at) : "暂无"}</span>
								</div>
							</div>
						</div>
					);
				})}
			</div>

			{/* Filter Bar */}
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="bg-background flex flex-wrap items-center gap-1 rounded-md border p-0.5">
					<Button
						type="button"
						variant={selectedSite === "all" ? "default" : "ghost"}
						size="sm"
						className="h-7 text-xs"
						onClick={() => setSelectedSite("all")}
					>
						全部站点
					</Button>
					{sites.map((site) => (
						<Button
							key={site.id}
							type="button"
							variant={selectedSite === site.id ? "default" : "ghost"}
							size="sm"
							className="h-7 text-xs"
							onClick={() => setSelectedSite(site.id)}
						>
							{site.name.replace("（云雾）", "")}
						</Button>
					))}
				</div>

				<div className="bg-background flex flex-wrap items-center gap-1 rounded-md border p-0.5">
					{DATE_PRESETS.map((p) => (
						<Button
							key={p.key}
							type="button"
							variant={datePreset === p.key ? "default" : "ghost"}
							size="sm"
							className="h-7 text-xs"
							onClick={() => setDatePreset(p.key)}
						>
							{p.label}
						</Button>
					))}
				</div>
			</div>

			{/* Records Table */}
			<div className="border-border bg-card overflow-hidden rounded-xl border">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead className="w-[155px]">充值时间</TableHead>
							<TableHead className="w-[130px]">平台</TableHead>
							<TableHead className="text-right">到账额度</TableHead>
							<TableHead className="text-right">实付人民币</TableHead>
							<TableHead className="w-[100px]">支付方式</TableHead>
							<TableHead className="w-[135px]">来源 / 状态</TableHead>
							<TableHead>订单号 / 备注</TableHead>
							<TableHead className="w-[100px] text-right">操作</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{isLoading ? (
							<TableRow>
								<TableCell colSpan={8} className="py-10 text-center text-xs text-muted-foreground">
									<Loader2 className="mr-2 inline h-4 w-4 animate-spin" />
									正在加载充值记录...
								</TableCell>
							</TableRow>
						) : items.length === 0 ? (
							<TableRow>
								<TableCell colSpan={8} className="py-10 text-center text-xs text-muted-foreground">
									当前筛选条件下暂无充值记录，可点击右上角「同步官方记录」或「手工记一笔」。
								</TableCell>
							</TableRow>
						) : (
							items.map((rec) => {
								const badge = getSourceBadge(rec.source, rec.status);
								return (
									<TableRow key={rec.id} data-testid={`relay-recharge-row-${rec.id}`}>
										<TableCell className="font-mono text-xs tabular-nums whitespace-nowrap">
											{formatDateTime(rec.recharged_at)}
										</TableCell>
										<TableCell className="text-xs font-medium whitespace-nowrap">{rec.site_name}</TableCell>
										<TableCell className="text-right font-mono text-xs tabular-nums whitespace-nowrap">
											{formatUSD(rec.site_amount_usd)}
										</TableCell>
										<TableCell className="text-right font-mono text-xs tabular-nums whitespace-nowrap">
											<div className="font-semibold text-foreground">{formatCNY(rec.paid_cny)}</div>
											{rec.paid_currency && rec.paid_currency !== "CNY" ? (
												<div className="text-muted-foreground text-[11px]">
													原币 {formatUSD(rec.paid_amount)}
												</div>
											) : null}
										</TableCell>
										<TableCell className="text-xs whitespace-nowrap">
											{formatPaymentMethod(rec.payment_method)}
										</TableCell>
										<TableCell className="whitespace-nowrap">
											<span className={cn("inline-flex rounded-full px-2 py-0.5 text-[11px] font-medium", badge.className)}>
												{badge.label}
											</span>
										</TableCell>
										<TableCell className="max-w-[320px] text-xs text-muted-foreground">
											{rec.external_id ? (
												<div className="truncate font-mono text-[11px] text-foreground/80" title={rec.external_id}>
													{rec.external_id}
												</div>
											) : null}
											{rec.note ? (
												<div className="truncate text-[11px]" title={rec.note}>
													{rec.note}
												</div>
											) : !rec.external_id ? (
												"—"
											) : null}
										</TableCell>
										<TableCell className="text-right whitespace-nowrap">
											<div className="inline-flex items-center gap-1">
												<Button
													type="button"
													variant="ghost"
													size="sm"
													className="h-7 w-7 p-0"
													title={rec.status === "pending_confirm" ? "核对并确认实付" : "编辑记录"}
													onClick={() => {
														setEditingRecord(rec);
														setDialogOpen(true);
													}}
												>
													<Pencil className="h-3.5 w-3.5" />
												</Button>
												<Button
													type="button"
													variant="ghost"
													size="sm"
													className="h-7 w-7 p-0 text-muted-foreground hover:text-destructive"
													title="删除记录"
													onClick={() => handleDelete(rec)}
												>
													<Trash2 className="h-3.5 w-3.5" />
												</Button>
											</div>
										</TableCell>
									</TableRow>
								);
							})
						)}
					</TableBody>
				</Table>
			</div>

			<RechargeRecordDialog
				open={dialogOpen}
				onOpenChange={setDialogOpen}
				sites={sites}
				editing={editingRecord}
			/>
		</div>
	);
}
