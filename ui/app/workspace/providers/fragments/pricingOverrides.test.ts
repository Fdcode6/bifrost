import { describe, expect, it } from "vitest";

import type { ModelProvider, ProviderPricingOverride } from "@/lib/types/config";

import {
	buildProviderPricingOverridesSavePayload,
	createTokenPricingOverrideFromPerMillion,
	formatPerTokenCost,
} from "./pricingOverrides";

describe("pricing override helpers", () => {
	it("converts per-million token prices to per-token override fields", () => {
		expect(
			createTokenPricingOverrideFromPerMillion({
				modelPattern: "gemini-2.5-pro*",
				matchType: "wildcard",
				requestTypes: ["chat_completion", "chat_completion_stream"],
				inputCostPerMillionTokens: "1.25",
				outputCostPerMillionTokens: "10",
			}),
		).toEqual({
			model_pattern: "gemini-2.5-pro*",
			match_type: "wildcard",
			request_types: ["chat_completion", "chat_completion_stream"],
			input_cost_per_token: 0.00000125,
			output_cost_per_token: 0.00001,
		});
	});

	it("omits empty input or output prices so unspecified values keep datasheet pricing", () => {
		expect(
			createTokenPricingOverrideFromPerMillion({
				modelPattern: "gpt-4o",
				matchType: "exact",
				requestTypes: ["chat_completion"],
				inputCostPerMillionTokens: "2.5",
				outputCostPerMillionTokens: "",
			}),
		).toEqual({
			model_pattern: "gpt-4o",
			match_type: "exact",
			request_types: ["chat_completion"],
			input_cost_per_token: 0.0000025,
		});
	});

	it("builds provider update payload with validated pricing overrides", () => {
		const provider = {
			name: "openai",
			keys: [],
			pricing_overrides: [],
		} as unknown as ModelProvider;
		const overrides: ProviderPricingOverride[] = [
			{
				model_pattern: "gpt-4o",
				match_type: "exact",
				input_cost_per_token: 0.000001,
			},
		];

		expect(buildProviderPricingOverridesSavePayload(provider, overrides)).toEqual({
			...provider,
			pricing_overrides: overrides,
		});
	});

	it("formats converted per-token costs without exponential notation", () => {
		expect(formatPerTokenCost(0.00000125)).toBe("0.00000125");
		expect(formatPerTokenCost(undefined)).toBe("");
	});
});
