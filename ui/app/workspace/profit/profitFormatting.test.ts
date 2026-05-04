import { describe, expect, it } from "vitest";

import { formatMoney, formatMargin, formatUsd, getDisplayCurrencyLabel, getProfitPresetLabel } from "./profitFormatting";

describe("profitFormatting", () => {
	it("formats USD values with cents and grouping", () => {
		expect(formatUsd(2918.293)).toBe("$2,918.29");
		expect(formatUsd(-0.42)).toBe("-$0.42");
	});

	it("formats money values in display currency from USD source values", () => {
		expect(formatMoney(10, "USD")).toBe("$10.00");
		expect(formatMoney(10, "CNY")).toBe("¥68.00");
		expect(formatMoney(-0.5, "CNY")).toBe("-¥3.40");
	});

	it("formats missing gross margin as dash", () => {
		expect(formatMargin(null)).toBe("—");
		expect(formatMargin(undefined)).toBe("—");
		expect(formatMargin(0.755)).toBe("75.5%");
	});

	it("returns Chinese preset labels", () => {
		expect(getProfitPresetLabel("today")).toBe("今日");
		expect(getProfitPresetLabel("yesterday")).toBe("昨日");
		expect(getProfitPresetLabel("7d")).toBe("最近 7 日");
		expect(getProfitPresetLabel("all")).toBe("全部累计");
	});

	it("returns Chinese currency labels", () => {
		expect(getDisplayCurrencyLabel("CNY")).toBe("人民币");
		expect(getDisplayCurrencyLabel("USD")).toBe("美元");
	});
});
