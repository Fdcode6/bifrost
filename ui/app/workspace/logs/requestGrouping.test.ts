import { describe, expect, it } from "vitest";

import type { LogEntry, LogStats, Pagination } from "@/lib/types/logs";

import {
	buildGroupDisplayState,
	buildVisibleRequestGroups,
	canUseGroupedRequestView,
	formatAttemptRateSummary,
	formatLayerLabel,
	formatRequestRateSummary,
} from "./requestGrouping";

const createLog = (overrides: Partial<LogEntry> & Record<string, unknown> = {}): LogEntry =>
	({
		id: "log-1",
		object: "chat.completion",
		timestamp: "2026-04-15T08:00:00.000Z",
		provider: "openai",
		model: "gpt-4.1",
		number_of_retries: 0,
		fallback_index: 0,
		selected_key_id: "key-1",
		input_history: [],
		responses_input_history: [],
		status: "success",
		stream: false,
		created_at: "2026-04-15T08:00:00.000Z",
		...overrides,
	}) as LogEntry;

describe("canUseGroupedRequestView", () => {
	it("returns true only for timestamp desc", () => {
		const basePagination: Pagination = {
			limit: 25,
			offset: 0,
			sort_by: "timestamp",
			order: "desc",
		};

		expect(canUseGroupedRequestView(basePagination)).toBe(true);
		expect(canUseGroupedRequestView({ ...basePagination, order: "asc" })).toBe(false);
		expect(canUseGroupedRequestView({ ...basePagination, sort_by: "latency" })).toBe(false);
		expect(canUseGroupedRequestView({ ...basePagination, sort_by: "tokens" })).toBe(false);
		expect(canUseGroupedRequestView({ ...basePagination, sort_by: "cost" })).toBe(false);
	});
});

describe("buildVisibleRequestGroups", () => {
	it("groups visible attempts by request and keeps attempts in sequence order", () => {
		const logs = [
			createLog({
				id: "attempt-2",
				group_id: "group-1",
				parent_request_id: "group-1",
				attempt_sequence: 2,
				fallback_index: 1,
				timestamp: "2026-04-15T08:02:00.000Z",
				status: "error",
			}),
			createLog({
				id: "attempt-1",
				group_id: "group-1",
				attempt_sequence: 1,
				fallback_index: 0,
				timestamp: "2026-04-15T08:01:00.000Z",
				status: "error",
			}),
			createLog({
				id: "attempt-3",
				group_id: "group-1",
				parent_request_id: "group-1",
				attempt_sequence: 3,
				fallback_index: 2,
				timestamp: "2026-04-15T08:03:00.000Z",
				status: "success",
				is_final_attempt: true,
				route_layer_index: 2,
			}),
			createLog({
				id: "solo-attempt",
				group_id: "group-2",
				attempt_sequence: 1,
				fallback_index: 0,
				timestamp: "2026-04-15T07:59:00.000Z",
				status: "success",
				is_final_attempt: true,
			}),
		];

		const groups = buildVisibleRequestGroups(logs);

		expect(groups).toHaveLength(2);
		expect(groups[0].groupId).toBe("group-1");
		expect(groups[0].attempts.map((attempt) => attempt.id)).toEqual(["attempt-1", "attempt-2", "attempt-3"]);
		expect(groups[0].latestAttempt.id).toBe("attempt-3");
		expect(groups[0].finalAttempt?.id).toBe("attempt-3");
		expect(groups[0].finalAttemptVisible).toBe(true);

		expect(groups[1].groupId).toBe("group-2");
		expect(groups[1].attempts.map((attempt) => attempt.id)).toEqual(["solo-attempt"]);
	});
});

describe("buildGroupDisplayState", () => {
	it("does not expose a final result when the final attempt is not visible on the current page", () => {
		const partialGroup = buildVisibleRequestGroups([
			createLog({
				id: "attempt-1",
				group_id: "group-1",
				attempt_sequence: 1,
				fallback_index: 0,
				status: "error",
				timestamp: "2026-04-15T08:01:00.000Z",
			}),
			createLog({
				id: "attempt-2",
				group_id: "group-1",
				attempt_sequence: 2,
				fallback_index: 1,
				status: "error",
				timestamp: "2026-04-15T08:02:00.000Z",
			}),
		])[0];

		const displayState = buildGroupDisplayState(partialGroup);

		expect(displayState.finalAttemptVisible).toBe(false);
		expect(displayState.isPartial).toBe(true);
		expect(displayState.finalAttempt).toBeNull();
		expect(displayState.finalStatus).toBeNull();
		expect(displayState.finalTarget).toBeNull();
		expect(displayState.latestAttempt.id).toBe("attempt-2");
	});
});

describe("rate summary helpers", () => {
	it("formats attempt and request summaries from numerator, denominator and percentage", () => {
		const stats = {
			total_requests: 15,
			success_rate: 68.26,
			average_latency: 123.45,
			total_tokens: 900,
			total_cost: 1.23,
			completed_attempts: 15,
			successful_attempts: 10,
			completed_request_groups: 10,
			successful_request_groups: 9,
			request_success_rate: 90,
			average_final_latency: 98.2,
		} satisfies LogStats;

		expect(formatAttemptRateSummary(stats)).toBe("10 / 15 (68.26%)");
		expect(formatRequestRateSummary(stats)).toBe("9 / 10 (90.00%)");
	});
});

describe("formatLayerLabel", () => {
	it("formats route layers and falls back to Unlayered", () => {
		expect(formatLayerLabel(0)).toBe("Layer 1");
		expect(formatLayerLabel(2)).toBe("Layer 3");
		expect(formatLayerLabel(null)).toBe("Unlayered");
		expect(formatLayerLabel(undefined)).toBe("Unlayered");
	});
});
