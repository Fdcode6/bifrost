import { describe, expect, it } from "vitest";

import type { ModelProvider, ProviderPricingOverride } from "@/lib/types/config";

import {
	buildProviderPricingOverridesSavePayload,
	createTokenPricingOverrideFromPerMillion,
	formatPerMillionTokenCost,
	formatPerTokenCost,
	getFirstTokenPricingOverride,
	getProviderPricingOverrideKey,
	getTokenPricingOverrides,
	removeProviderPricingOverrideByKey,
	upsertProviderPricingOverride,
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

	it("rejects wildcard model patterns without an asterisk before generating JSON", () => {
		expect(() =>
			createTokenPricingOverrideFromPerMillion({
				modelPattern: "Gemini",
				matchType: "wildcard",
				requestTypes: ["chat_completion", "chat_completion_stream"],
				inputCostPerMillionTokens: "2",
				outputCostPerMillionTokens: "14",
			}),
		).toThrow('wildcard 是通配符匹配，模型匹配必须包含 "*"');
	});

	it("rejects exact model patterns that contain wildcard syntax", () => {
		expect(() =>
			createTokenPricingOverrideFromPerMillion({
				modelPattern: "gemini-*",
				matchType: "exact",
				requestTypes: ["chat_completion"],
				inputCostPerMillionTokens: "2",
				outputCostPerMillionTokens: "14",
			}),
		).toThrow('exact 是精确匹配，模型名里不能包含 "*"');
	});

	it("creates contains overrides for case-insensitive keyword matching", () => {
		expect(
			createTokenPricingOverrideFromPerMillion({
				modelPattern: "Gemini",
				matchType: "contains",
				requestTypes: ["chat_completion", "chat_completion_stream"],
				inputCostPerMillionTokens: "2",
				outputCostPerMillionTokens: "14",
			}),
		).toEqual({
			model_pattern: "Gemini",
			match_type: "contains",
			request_types: ["chat_completion", "chat_completion_stream"],
			input_cost_per_token: 0.000002,
			output_cost_per_token: 0.000014,
		});
	});

	it("rejects contains model patterns that still include wildcard syntax", () => {
		expect(() =>
			createTokenPricingOverrideFromPerMillion({
				modelPattern: "gemini-*",
				matchType: "contains",
				requestTypes: ["chat_completion"],
				inputCostPerMillionTokens: "2",
				outputCostPerMillionTokens: "14",
			}),
		).toThrow('contains 是关键词包含匹配，不需要填写 "*"');
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
		expect(formatPerMillionTokenCost(0.00000125)).toBe("1.25");
		expect(formatPerTokenCost(undefined)).toBe("");
	});

	it("selects the first saved token pricing override for converter hydration", () => {
		const overrides: ProviderPricingOverride[] = [
			{
				model_pattern: "audio-*",
				match_type: "wildcard",
				input_cost_per_audio_per_second: 0.001,
			},
			{
				model_pattern: "gemini-2.5-pro*",
				match_type: "wildcard",
				input_cost_per_token: 0.00000125,
			},
		];

		expect(getTokenPricingOverrides(overrides)).toHaveLength(1);
		expect(getFirstTokenPricingOverride(overrides)).toMatchObject({
			model_pattern: "gemini-2.5-pro*",
		});
	});

	it("builds stable keys for saved pricing override selection", () => {
		expect(
			getProviderPricingOverrideKey({
				model_pattern: "gemini-2.5-pro*",
				match_type: "wildcard",
				request_types: ["chat_completion", "chat_completion_stream"],
				input_cost_per_token: 0.00000125,
			}),
		).toBe("wildcard:gemini-2.5-pro*:chat_completion,chat_completion_stream");
	});

	it("updates an existing matching override instead of appending duplicates", () => {
		const existing: ProviderPricingOverride[] = [
			{
				model_pattern: "gemini-2.5-pro*",
				match_type: "wildcard",
				request_types: ["chat_completion", "chat_completion_stream"],
				input_cost_per_token: 0.000001,
			},
		];
		const updated = upsertProviderPricingOverride(existing, {
			model_pattern: "gemini-2.5-pro*",
			match_type: "wildcard",
			request_types: ["chat_completion", "chat_completion_stream"],
			input_cost_per_token: 0.00000125,
			output_cost_per_token: 0.00001,
		});

		expect(updated).toHaveLength(1);
		expect(updated[0]).toMatchObject({
			input_cost_per_token: 0.00000125,
			output_cost_per_token: 0.00001,
		});
	});

	it("removes a saved pricing override by its stable selection key", () => {
		const existing: ProviderPricingOverride[] = [
			{
				model_pattern: "codex-price-test-exact",
				match_type: "exact",
				request_types: ["chat_completion", "chat_completion_stream"],
				input_cost_per_token: 4.2e-7,
			},
			{
				model_pattern: "gemini-2.5-pro*",
				match_type: "wildcard",
				request_types: ["chat_completion", "chat_completion_stream"],
				input_cost_per_token: 0.00000125,
				output_cost_per_token: 0.00001,
			},
		];

		const next = removeProviderPricingOverrideByKey(existing, "wildcard:gemini-2.5-pro*:chat_completion,chat_completion_stream");

		expect(next).toEqual([
			{
				model_pattern: "codex-price-test-exact",
				match_type: "exact",
				request_types: ["chat_completion", "chat_completion_stream"],
				input_cost_per_token: 4.2e-7,
			},
		]);
	});
});
