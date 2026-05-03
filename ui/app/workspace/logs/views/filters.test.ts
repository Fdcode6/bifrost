import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/store", () => ({
	getErrorMessage: (error: unknown) => String(error),
	useRecalculateLogCostsMutation: () => [vi.fn(), { isLoading: false }],
}));

import { getManualRefreshButtonState } from "./filters";

describe("logs filter toolbar manual refresh state", () => {
	it("uses a clear idle label when logs can be refreshed manually", () => {
		expect(getManualRefreshButtonState(false)).toEqual({
			disabled: false,
			iconClassName: "h-4 w-4",
			label: "Refresh logs",
		});
	});

	it("communicates when a manual refresh is already running", () => {
		expect(getManualRefreshButtonState(true)).toEqual({
			disabled: true,
			iconClassName: "h-4 w-4 animate-spin",
			label: "Refreshing logs...",
		});
	});
});
