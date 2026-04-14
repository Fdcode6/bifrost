import { describe, expect, it } from "vitest";

import type { RouteGroupFormData, RoutingTargetFormData } from "@/lib/types/routingRules";

import { getRouteGroupAvailableKeys, shouldShowRouteGroupKeySelector, updateRouteGroupTarget } from "./routeGroupState";

describe("routeGroupState helpers", () => {
	it("updates provider/model/key together for grouped targets", () => {
		const group: RouteGroupFormData = {
			name: "Primary",
			retry_limit: 0,
			targets: [
				{
					provider: "openrouter",
					model: "gemma-4-31b-it",
					key_id: "key-1",
					weight: 1,
				},
			],
		};

		const updated = updateRouteGroupTarget(group, 0, {
			provider: "柏拉图",
			model: "",
			key_id: "",
		});

		expect(updated.targets[0]).toEqual({
			provider: "柏拉图",
			model: "",
			key_id: "",
			weight: 1,
		});
	});

	it("only patches the targeted row", () => {
		const group: RouteGroupFormData = {
			name: "Primary",
			retry_limit: 1,
			targets: [
				{
					provider: "openrouter",
					model: "gemma-4-31b-it",
					key_id: "key-1",
					weight: 0.7,
				},
				{
					provider: "柏拉图",
					model: "gemini-3.1-pro-preview-thinking-medium",
					key_id: "",
					weight: 0.3,
				},
			],
		};

		const updated = updateRouteGroupTarget(group, 1, {
			weight: 0.5,
		});

		expect(updated.targets[0]).toEqual(group.targets[0]);
		expect(updated.targets[1]).toEqual({
			provider: "柏拉图",
			model: "gemini-3.1-pro-preview-thinking-medium",
			key_id: "",
			weight: 0.5,
		});
	});

	it("returns keys for the selected grouped target provider", () => {
		expect(
			getRouteGroupAvailableKeys(
				[
					{
						name: "柏拉图",
						keys: [
							{ id: "key-1", name: "bltcy_api" },
							{ id: "key-2", name: "backup_api" },
						],
					},
					{
						name: "openrouter",
						keys: [{ id: "key-3", name: "openrouter_api" }],
					},
				],
				"柏拉图",
			),
		).toEqual([
			{ id: "key-1", name: "bltcy_api" },
			{ id: "key-2", name: "backup_api" },
		]);
	});

	it("shows grouped target key selector when keys or a pinned key exist", () => {
		const target: RoutingTargetFormData = {
			provider: "柏拉图",
			model: "gemini-3.1-pro-preview-thinking-medium",
			key_id: "",
			weight: 1,
		};

		expect(shouldShowRouteGroupKeySelector(target, [{ id: "key-1", name: "bltcy_api" }])).toBe(true);
		expect(shouldShowRouteGroupKeySelector({ ...target, key_id: "key-2" }, [])).toBe(true);
		expect(shouldShowRouteGroupKeySelector({ ...target, provider: "" }, [{ id: "key-1", name: "bltcy_api" }])).toBe(false);
	});
});
