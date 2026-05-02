import type { ModelProvider, PricingOverrideMatchType, ProviderPricingOverride, RequestType } from "@/lib/types/config";

const TOKEN_PRICING_DENOMINATOR = 1_000_000;

export interface PerMillionTokenPricingInput {
	modelPattern: string;
	matchType: PricingOverrideMatchType;
	requestTypes?: RequestType[];
	inputCostPerMillionTokens: string;
	outputCostPerMillionTokens: string;
}

function parsePerMillionTokenPrice(value: string, fieldName: string): number | undefined {
	const trimmed = value.trim();
	if (!trimmed) {
		return undefined;
	}
	const parsed = Number(trimmed);
	if (!Number.isFinite(parsed)) {
		throw new Error(`${fieldName} must be a valid number.`);
	}
	if (parsed < 0) {
		throw new Error(`${fieldName} must be greater than or equal to 0.`);
	}
	return parsed / TOKEN_PRICING_DENOMINATOR;
}

export function formatPerTokenCost(value: number | undefined): string {
	if (value === undefined) {
		return "";
	}
	return value.toFixed(12).replace(/\.?0+$/, "");
}

export function createTokenPricingOverrideFromPerMillion(input: PerMillionTokenPricingInput): ProviderPricingOverride {
	const modelPattern = input.modelPattern.trim();
	if (!modelPattern) {
		throw new Error("Model pattern is required.");
	}

	const inputCostPerToken = parsePerMillionTokenPrice(input.inputCostPerMillionTokens, "Input price");
	const outputCostPerToken = parsePerMillionTokenPrice(input.outputCostPerMillionTokens, "Output price");
	if (inputCostPerToken === undefined && outputCostPerToken === undefined) {
		throw new Error("Enter at least one input or output price.");
	}

	const override: ProviderPricingOverride = {
		model_pattern: modelPattern,
		match_type: input.matchType,
	};
	const requestTypes = input.requestTypes?.filter(Boolean);
	if (requestTypes && requestTypes.length > 0) {
		override.request_types = requestTypes;
	}
	if (inputCostPerToken !== undefined) {
		override.input_cost_per_token = inputCostPerToken;
	}
	if (outputCostPerToken !== undefined) {
		override.output_cost_per_token = outputCostPerToken;
	}
	return override;
}

export function buildProviderPricingOverridesSavePayload(
	provider: ModelProvider,
	overrides: ProviderPricingOverride[],
): ModelProvider {
	return {
		...provider,
		pricing_overrides: overrides,
	};
}
