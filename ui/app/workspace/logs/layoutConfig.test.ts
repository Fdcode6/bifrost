import { describe, expect, it } from "vitest";

import {
	LOGS_ANALYTICS_DEFAULT_OPEN,
	LOGS_COLLAPSIBLE_EXPANDED_MAX_HEIGHT,
	LOGS_DETAIL_BODY_CLASS,
	LOGS_DETAIL_CODE_MAX_HEIGHT,
	LOGS_DETAIL_SCROLL_AREA_CLASS,
	LOGS_DETAIL_SHEET_CLASS,
	LOGS_FINAL_DISTRIBUTION_LIST_CLASS,
	LOGS_PAGE_CHART_GRID_CLASS,
	LOGS_PAGE_STATS_GRID_CLASS,
	LOGS_VOLUME_CHART_HEIGHT_CLASS,
	LOGS_VOLUME_CHART_LOADING_HEIGHT,
} from "./layoutConfig";

describe("logs layout config", () => {
	it("keeps the top analytics area compact", () => {
		expect(LOGS_ANALYTICS_DEFAULT_OPEN).toBe(false);
		expect(LOGS_PAGE_STATS_GRID_CLASS).toContain("gap-3");
		expect(LOGS_PAGE_CHART_GRID_CLASS).toContain("gap-3");
		expect(LOGS_VOLUME_CHART_HEIGHT_CLASS).toBe("h-28");
		expect(LOGS_VOLUME_CHART_LOADING_HEIGHT).toBeLessThan(131);
		expect(LOGS_FINAL_DISTRIBUTION_LIST_CLASS).toContain("max-h-48");
	});

	it("gives the details panel a larger readable viewport", () => {
		expect(LOGS_DETAIL_SHEET_CLASS).toContain("overflow-y-auto");
		expect(LOGS_DETAIL_SHEET_CLASS).toContain("sm:max-w-[72vw]");
		expect(LOGS_DETAIL_BODY_CLASS).toContain("flex-1");
		expect(LOGS_DETAIL_BODY_CLASS).toContain("overflow-y-auto");
		expect(LOGS_DETAIL_SCROLL_AREA_CLASS).toContain("max-h-[58vh]");
		expect(LOGS_DETAIL_SCROLL_AREA_CLASS).not.toContain("400px");
		expect(LOGS_COLLAPSIBLE_EXPANDED_MAX_HEIGHT).toBeGreaterThan(450);
		expect(LOGS_DETAIL_CODE_MAX_HEIGHT).toBeGreaterThan(450);
	});
});
