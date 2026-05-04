import type { ProfitPreset } from "@/lib/types/profit";

export type DisplayCurrency = "CNY" | "USD";

export const USD_TO_CNY_RATE = 6.8;

export function formatUsd(value: number | null | undefined): string {
	return formatMoney(value, "USD");
}

export function formatMoney(value: number | null | undefined, currency: DisplayCurrency): string {
	if (!Number.isFinite(value)) {
		return "—";
	}
	const normalized = currency === "CNY" ? (value as number) * USD_TO_CNY_RATE : (value as number);
	const abs = Math.abs(normalized);
	const formatted = new Intl.NumberFormat(currency === "CNY" ? "zh-CN" : "en-US", {
		style: "currency",
		currency,
		currencyDisplay: "narrowSymbol",
		minimumFractionDigits: 2,
		maximumFractionDigits: 2,
	}).format(abs);
	return normalized < 0 ? `-${formatted}` : formatted;
}

export function formatCompactNumber(value: number | null | undefined): string {
	if (!Number.isFinite(value)) {
		return "—";
	}
	return new Intl.NumberFormat("zh-CN").format(value as number);
}

export function formatMargin(value: number | null | undefined): string {
	if (!Number.isFinite(value)) {
		return "—";
	}
	return `${((value as number) * 100).toFixed(1)}%`;
}

export function getProfitPresetLabel(preset: ProfitPreset): string {
	const labels: Record<ProfitPreset, string> = {
		today: "今日",
		yesterday: "昨日",
		"7d": "最近 7 日",
		all: "全部累计",
	};
	return labels[preset];
}

export function getDisplayCurrencyLabel(currency: DisplayCurrency): string {
	return currency === "CNY" ? "人民币" : "美元";
}
