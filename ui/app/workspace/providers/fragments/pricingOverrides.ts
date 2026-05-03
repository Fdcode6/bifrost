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
		throw new Error(`${fieldName}必须是有效数字。`);
	}
	if (parsed < 0) {
		throw new Error(`${fieldName}不能小于 0。`);
	}
	return parsed / TOKEN_PRICING_DENOMINATOR;
}

function validateModelPatternForMatchType(modelPattern: string, matchType: PricingOverrideMatchType) {
	if (matchType === "exact" && modelPattern.includes("*")) {
		throw new Error('exact 是精确匹配，模型名里不能包含 "*"。如果要匹配一批模型，请改用 wildcard。');
	}
	if (matchType === "wildcard" && !modelPattern.includes("*")) {
		throw new Error('wildcard 是通配符匹配，模型匹配必须包含 "*"，例如 "gemini-*"。如果只匹配一个完整模型名，请改用 exact。');
	}
	if (matchType === "contains" && modelPattern.includes("*")) {
		throw new Error('contains 是关键词包含匹配，不需要填写 "*"；直接填关键词即可，例如 "gemini"。');
	}
	if (matchType === "regex") {
		try {
			new RegExp(modelPattern);
		} catch {
			throw new Error("regex 正则表达式不合法，请检查括号、转义和特殊字符。");
		}
	}
}

export function formatPerTokenCost(value: number | undefined): string {
	if (value === undefined) {
		return "";
	}
	return value.toFixed(12).replace(/\.?0+$/, "");
}

export function formatPerMillionTokenCost(value: number | undefined): string {
	if (value === undefined) {
		return "";
	}
	return formatPerTokenCost(value * TOKEN_PRICING_DENOMINATOR);
}

export function getFirstTokenPricingOverride(overrides?: ProviderPricingOverride[]): ProviderPricingOverride | undefined {
	return getTokenPricingOverrides(overrides)[0];
}

export function getTokenPricingOverrides(overrides?: ProviderPricingOverride[]): ProviderPricingOverride[] {
	return overrides?.filter((override) => override.input_cost_per_token !== undefined || override.output_cost_per_token !== undefined) ?? [];
}

export function getProviderPricingOverrideKey(override: ProviderPricingOverride): string {
	return `${override.match_type}:${override.model_pattern}:${override.request_types?.join(",") ?? ""}`;
}

export function removeProviderPricingOverrideByKey(overrides: ProviderPricingOverride[], overrideKey: string): ProviderPricingOverride[] {
	return overrides.filter((override) => getProviderPricingOverrideKey(override) !== overrideKey);
}

export function createTokenPricingOverrideFromPerMillion(input: PerMillionTokenPricingInput): ProviderPricingOverride {
	const modelPattern = input.modelPattern.trim();
	if (!modelPattern) {
		throw new Error("模型匹配不能为空。");
	}
	validateModelPatternForMatchType(modelPattern, input.matchType);

	const inputCostPerToken = parsePerMillionTokenPrice(input.inputCostPerMillionTokens, "输入价格");
	const outputCostPerToken = parsePerMillionTokenPrice(input.outputCostPerMillionTokens, "输出价格");
	if (inputCostPerToken === undefined && outputCostPerToken === undefined) {
		throw new Error("输入价格和输出价格至少填写一个。");
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

function sameRequestTypes(left?: RequestType[], right?: RequestType[]): boolean {
	const leftTypes = left ?? [];
	const rightTypes = right ?? [];
	if (leftTypes.length !== rightTypes.length) {
		return false;
	}
	return leftTypes.every((requestType, index) => requestType === rightTypes[index]);
}

export function upsertProviderPricingOverride(
	overrides: ProviderPricingOverride[],
	override: ProviderPricingOverride,
): ProviderPricingOverride[] {
	const index = overrides.findIndex(
		(item) =>
			item.model_pattern === override.model_pattern &&
			item.match_type === override.match_type &&
			sameRequestTypes(item.request_types, override.request_types),
	);
	if (index < 0) {
		return [...overrides, override];
	}
	return overrides.map((item, itemIndex) => (itemIndex === index ? override : item));
}

export function buildProviderPricingOverridesSavePayload(provider: ModelProvider, overrides: ProviderPricingOverride[]): ModelProvider {
	return {
		...provider,
		pricing_overrides: overrides,
	};
}
