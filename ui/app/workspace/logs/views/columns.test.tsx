import { describe, expect, it, vi } from "vitest";

import { createColumns } from "./columns";

function getColumnId(column: ReturnType<typeof createColumns>[number]): string {
	if ("id" in column && column.id) {
		return column.id;
	}
	if ("accessorKey" in column && column.accessorKey) {
		return String(column.accessorKey);
	}
	return "";
}

describe("LLM logs table columns", () => {
	it("keeps operational fields before the wide message column", () => {
		const columnIds = createColumns(vi.fn()).map(getColumnId);

		expect(columnIds.slice(0, 10)).toEqual([
			"status",
			"timestamp",
			"request_type",
			"provider",
			"model",
			"latency",
			"tokens",
			"cost",
			"input",
			"actions",
		]);
	});

	it("defines concrete widths for every default column", () => {
		const columns = createColumns(vi.fn());

		for (const column of columns) {
			expect(column.size, `${getColumnId(column)} size`).toBeGreaterThan(0);
		}
	});

	it("places metadata columns after the message column but before actions", () => {
		const columnIds = createColumns(vi.fn(), true, ["tenant", "trace"]).map(getColumnId);

		expect(columnIds).toEqual([
			"status",
			"timestamp",
			"request_type",
			"provider",
			"model",
			"latency",
			"tokens",
			"cost",
			"input",
			"metadata_tenant",
			"metadata_trace",
			"actions",
		]);
	});
});
