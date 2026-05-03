"use client";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { getErrorMessage, setProviderFormDirtyState, useAppDispatch } from "@/lib/store";
import { useUpdateProviderMutation } from "@/lib/store/apis/providersApi";
import { ModelProvider, PricingOverrideMatchType, RequestType } from "@/lib/types/config";
import { providerPricingOverrideSchema } from "@/lib/types/schemas";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { z } from "zod";
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

interface PricingOverridesFormFragmentProps {
	provider: ModelProvider;
}

const pricingOverridesArraySchema = z.array(providerPricingOverrideSchema);
const defaultTokenPricingRequestTypes: RequestType[] = ["chat_completion", "chat_completion_stream"];

const toPrettyJSON = (value: unknown) => JSON.stringify(value, null, 2);

export function PricingOverridesFormFragment({ provider }: PricingOverridesFormFragmentProps) {
	const dispatch = useAppDispatch();
	const hasUpdateProviderAccess = useRbac(RbacResource.ModelProvider, RbacOperation.Update);
	const [updateProvider, { isLoading: isUpdatingProvider }] = useUpdateProviderMutation();
	const initialValue = useMemo(() => toPrettyJSON(provider.pricing_overrides ?? []), [provider.pricing_overrides]);
	const [overridesJSON, setOverridesJSON] = useState(initialValue);
	const [validationError, setValidationError] = useState<string>("");
	const [hasUserEdits, setHasUserEdits] = useState(false);
	const [quickModelPattern, setQuickModelPattern] = useState("");
	const [quickMatchType, setQuickMatchType] = useState<PricingOverrideMatchType>("contains");
	const [quickInputPerMillion, setQuickInputPerMillion] = useState("");
	const [quickOutputPerMillion, setQuickOutputPerMillion] = useState("");
	const [quickHelperError, setQuickHelperError] = useState("");
	const [hasQuickEdits, setHasQuickEdits] = useState(false);
	const [selectedOverrideKey, setSelectedOverrideKey] = useState("");
	const isDirty = hasUserEdits && overridesJSON !== initialValue;
	const savedTokenOverrides = useMemo(() => getTokenPricingOverrides(provider.pricing_overrides), [provider.pricing_overrides]);
	const selectedOverrideStorageKey = `provider-pricing-override-selection:${provider.name}`;
	const providerModelExample = useMemo(() => provider.keys.flatMap((key) => key.models ?? []).find(Boolean), [provider.keys]);
	const wildcardModelExample = useMemo(() => {
		if (!providerModelExample) {
			return "gemini-*";
		}
		const prefix = providerModelExample.split("-")[0];
		return prefix ? `${prefix}-*` : `${providerModelExample}*`;
	}, [providerModelExample]);
	const containsModelExample = providerModelExample?.split("-")[0] ?? "gemini";
	const modelPatternPlaceholder =
		quickMatchType === "exact"
			? (providerModelExample ?? "gemini-2.5-pro")
			: quickMatchType === "contains"
				? containsModelExample
				: quickMatchType === "regex"
					? "gemini-.*"
					: wildcardModelExample;

	const hydrateQuickFields = (override: ReturnType<typeof getFirstTokenPricingOverride>) => {
		setQuickModelPattern(override?.model_pattern ?? "");
		setQuickMatchType(override?.match_type ?? "contains");
		setQuickInputPerMillion(formatPerMillionTokenCost(override?.input_cost_per_token));
		setQuickOutputPerMillion(formatPerMillionTokenCost(override?.output_cost_per_token));
		setQuickHelperError("");
	};

	useEffect(() => {
		if (isDirty) {
			return;
		}
		setOverridesJSON(initialValue);
		setValidationError("");
	}, [initialValue, isDirty, provider.name]);

	useEffect(() => {
		if (isDirty || hasQuickEdits) {
			return;
		}
		const storedOverrideKey = typeof window !== "undefined" ? window.localStorage.getItem(selectedOverrideStorageKey) : "";
		const savedTokenOverride =
			savedTokenOverrides.find((override) => getProviderPricingOverrideKey(override) === storedOverrideKey) ??
			getFirstTokenPricingOverride(provider.pricing_overrides);
		const nextOverrideKey = savedTokenOverride ? getProviderPricingOverrideKey(savedTokenOverride) : "";
		setSelectedOverrideKey(nextOverrideKey);
		hydrateQuickFields(savedTokenOverride);
	}, [hasQuickEdits, initialValue, isDirty, provider.pricing_overrides, savedTokenOverrides, selectedOverrideStorageKey]);

	useEffect(() => {
		dispatch(setProviderFormDirtyState(isDirty));
	}, [dispatch, isDirty]);

	const onReset = () => {
		setOverridesJSON(initialValue);
		setValidationError("");
		setQuickHelperError("");
		setHasQuickEdits(false);
		setHasUserEdits(false);
	};

	const onSelectSavedOverride = (overrideKey: string) => {
		const override = savedTokenOverrides.find((item) => getProviderPricingOverrideKey(item) === overrideKey);
		setSelectedOverrideKey(overrideKey);
		if (typeof window !== "undefined") {
			window.localStorage.setItem(selectedOverrideStorageKey, overrideKey);
		}
		hydrateQuickFields(override);
		setHasQuickEdits(false);
	};

	const onRemoveSelectedOverride = () => {
		if (!selectedOverrideKey) {
			return;
		}
		const currentOverrides = parseOverridesJSON();
		if (!currentOverrides) {
			return;
		}

		const nextOverrides = removeProviderPricingOverrideByKey(currentOverrides, selectedOverrideKey);
		if (nextOverrides.length === currentOverrides.length) {
			toast.error("没有找到要删除的价格条目");
			return;
		}

		const nextTokenOverride = getTokenPricingOverrides(nextOverrides)[0];
		const nextOverrideKey = nextTokenOverride ? getProviderPricingOverrideKey(nextTokenOverride) : "";
		setOverridesJSON(toPrettyJSON(nextOverrides));
		setSelectedOverrideKey(nextOverrideKey);
		if (typeof window !== "undefined") {
			if (nextOverrideKey) {
				window.localStorage.setItem(selectedOverrideStorageKey, nextOverrideKey);
			} else {
				window.localStorage.removeItem(selectedOverrideStorageKey);
			}
		}
		hydrateQuickFields(nextTokenOverride);
		setHasUserEdits(true);
		setHasQuickEdits(false);
		toast.success("已从 JSON 删除所选价格条目，保存后生效");
	};

	const parseOverridesJSON = (options?: { fallbackToSavedOnInvalid?: boolean }) => {
		let parsed: unknown;
		try {
			parsed = JSON.parse(overridesJSON);
		} catch {
			if (options?.fallbackToSavedOnInvalid) {
				setValidationError("");
				return provider.pricing_overrides ?? [];
			}
			setValidationError("JSON 格式不正确，请检查括号、逗号和引号。");
			return null;
		}

		const validated = pricingOverridesArraySchema.safeParse(parsed);
		if (!validated.success) {
			if (options?.fallbackToSavedOnInvalid) {
				setValidationError("");
				return provider.pricing_overrides ?? [];
			}
			setValidationError(validated.error.issues[0]?.message || "价格覆盖 JSON 配置无效。");
			return null;
		}

		setValidationError("");
		return validated.data;
	};

	const onAddQuickOverride = () => {
		try {
			const override = createTokenPricingOverrideFromPerMillion({
				modelPattern: quickModelPattern,
				matchType: quickMatchType,
				requestTypes: defaultTokenPricingRequestTypes,
				inputCostPerMillionTokens: quickInputPerMillion,
				outputCostPerMillionTokens: quickOutputPerMillion,
			});
			const currentOverrides = parseOverridesJSON({ fallbackToSavedOnInvalid: true });
			if (!currentOverrides) {
				return;
			}
			setQuickHelperError("");
			setOverridesJSON(toPrettyJSON(upsertProviderPricingOverride(currentOverrides, override)));
			setHasUserEdits(true);
			toast.success("已更新 JSON 草稿，还未保存到当前 Provider");
		} catch (err) {
			setQuickHelperError(getErrorMessage(err));
		}
	};

	const onNormalizeJSON = () => {
		const currentOverrides = parseOverridesJSON();
		if (!currentOverrides) {
			return;
		}
		setOverridesJSON(toPrettyJSON(currentOverrides));
		setHasUserEdits(true);
	};

	const onSave = async () => {
		const validatedOverrides = parseOverridesJSON();
		if (!validatedOverrides) {
			return;
		}

		try {
			await updateProvider(buildProviderPricingOverridesSavePayload(provider, validatedOverrides)).unwrap();
			const nextTokenOverride = getTokenPricingOverrides(validatedOverrides)[0];
			const nextOverrideKey = nextTokenOverride ? getProviderPricingOverrideKey(nextTokenOverride) : "";
			setSelectedOverrideKey(nextOverrideKey);
			if (typeof window !== "undefined") {
				if (nextOverrideKey) {
					window.localStorage.setItem(selectedOverrideStorageKey, nextOverrideKey);
				} else {
					window.localStorage.removeItem(selectedOverrideStorageKey);
				}
			}
			toast.success("价格覆盖配置已保存，可以切换中转站");
			setOverridesJSON(toPrettyJSON(validatedOverrides));
			setHasUserEdits(false);
			setHasQuickEdits(false);
		} catch (err) {
			toast.error("价格覆盖配置保存失败", {
				description: getErrorMessage(err),
			});
		}
	};

	return (
		<div className="space-y-4 px-6 pb-6">
			<div className="space-y-1">
				<p className="text-sm font-medium">Provider 价格覆盖</p>
				<p className="text-muted-foreground text-xs">
					这里用于给当前 Provider 的指定模型手动补价格。推荐先用换算器按“每 100 万 tokens”填写，再自动生成 JSON； 也可以直接编辑
					JSON。匹配优先级：exact &gt; wildcard &gt; contains &gt; regex；没填写的价格字段会继续沿用模型库价格。
				</p>
			</div>

			<div className="space-y-3 rounded-sm border p-4">
				<div className="space-y-1">
					<p className="text-sm font-medium">Token 价格换算器</p>
					<p className="text-muted-foreground text-xs">
						按供应商常见报价填写“美元 / 100 万 tokens”。系统保存时会自动换算成“美元 / 单个 token”，不用手算很多位小数。
					</p>
				</div>
				<div className="grid gap-3 md:grid-cols-2">
					{savedTokenOverrides.length > 0 ? (
						<div className="space-y-1.5 md:col-span-2">
							<Label htmlFor="pricing-override-saved-entry" className="text-xs">
								已保存到当前 Provider 的价格条目
							</Label>
							<div className="flex items-center gap-2">
								<Select value={selectedOverrideKey} onValueChange={onSelectSavedOverride}>
									<SelectTrigger
										id="pricing-override-saved-entry"
										data-testid="provider-pricing-saved-override-select"
										className="flex-1"
										disabled={!hasUpdateProviderAccess}
									>
										<SelectValue placeholder="选择已保存条目" />
									</SelectTrigger>
									<SelectContent>
										{savedTokenOverrides.map((override) => (
											<SelectItem key={getProviderPricingOverrideKey(override)} value={getProviderPricingOverrideKey(override)}>
												{override.model_pattern} · {override.match_type} · 输入{" "}
												{formatPerMillionTokenCost(override.input_cost_per_token) || "—"} / 输出{" "}
												{formatPerMillionTokenCost(override.output_cost_per_token) || "—"}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
								<Button
									type="button"
									variant="outline"
									size="icon"
									data-testid="provider-pricing-remove-selected-override-button"
									onClick={onRemoveSelectedOverride}
									disabled={!hasUpdateProviderAccess || !selectedOverrideKey}
									aria-label="删除所选价格条目"
									title="删除所选价格条目"
								>
									<Trash2 className="h-4 w-4" />
								</Button>
							</div>
							<p className="text-muted-foreground text-[11px]">
								这里仅显示后端已保存的条目；刚换算出来的 JSON 草稿，保存成功后才会出现在这里。
							</p>
						</div>
					) : null}
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-model-pattern" className="text-xs">
							模型匹配
						</Label>
						<Input
							id="pricing-override-model-pattern"
							data-testid="provider-pricing-model-pattern-input"
							value={quickModelPattern}
							onChange={(event) => {
								setQuickModelPattern(event.target.value);
								setHasQuickEdits(true);
							}}
							placeholder={modelPatternPlaceholder}
							disabled={!hasUpdateProviderAccess}
						/>
						<p className="text-muted-foreground text-[11px]">
							{providerModelExample ? `当前 Provider 模型例子：${providerModelExample}。` : null}
							exact 填完整模型名；wildcard 必须带 *，例如 {wildcardModelExample}；contains 直接填关键词，不区分大小写。
						</p>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-match-type" className="text-xs">
							匹配方式
						</Label>
						<Select
							value={quickMatchType}
							onValueChange={(value) => {
								setQuickMatchType(value as PricingOverrideMatchType);
								setHasQuickEdits(true);
							}}
						>
							<SelectTrigger
								id="pricing-override-match-type"
								data-testid="provider-pricing-match-type-select"
								disabled={!hasUpdateProviderAccess}
							>
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="exact">exact（精确匹配）</SelectItem>
								<SelectItem value="wildcard">wildcard（通配符）</SelectItem>
								<SelectItem value="contains">contains（包含关键词，忽略大小写）</SelectItem>
								<SelectItem value="regex">regex（正则）</SelectItem>
							</SelectContent>
						</Select>
						<p className="text-muted-foreground text-[11px]">
							exact 适合单个完整模型名；wildcard 支持 `*`；contains 会按关键词忽略大小写匹配；regex 适合更复杂的正则匹配。
						</p>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-input-price" className="text-xs">
							输入价格（美元 / 100 万 input tokens）
						</Label>
						<Input
							id="pricing-override-input-price"
							data-testid="provider-pricing-input-per-million-input"
							type="number"
							min="0"
							step="0.000001"
							value={quickInputPerMillion}
							onChange={(event) => {
								setQuickInputPerMillion(event.target.value);
								setHasQuickEdits(true);
							}}
							placeholder="1.25"
							disabled={!hasUpdateProviderAccess}
						/>
						<p className="text-muted-foreground text-[11px]">
							保存值（每 token）: {formatPerTokenCost(quickInputPerMillion ? Number(quickInputPerMillion) / 1_000_000 : undefined) || "—"}
						</p>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-output-price" className="text-xs">
							输出价格（美元 / 100 万 output tokens）
						</Label>
						<Input
							id="pricing-override-output-price"
							data-testid="provider-pricing-output-per-million-input"
							type="number"
							min="0"
							step="0.000001"
							value={quickOutputPerMillion}
							onChange={(event) => {
								setQuickOutputPerMillion(event.target.value);
								setHasQuickEdits(true);
							}}
							placeholder="10"
							disabled={!hasUpdateProviderAccess}
						/>
						<p className="text-muted-foreground text-[11px]">
							保存值（每 token）: {formatPerTokenCost(quickOutputPerMillion ? Number(quickOutputPerMillion) / 1_000_000 : undefined) || "—"}
						</p>
					</div>
				</div>
				<p className="text-muted-foreground text-xs">
					换算器会同时写入 request_types：chat_completion 和
					chat_completion_stream。输入价或输出价可以只填一个，空着的那一项继续用模型库价格。换算后只是更新下方 JSON
					草稿，还需要点击底部“保存价格覆盖”才会生效。
				</p>
				{quickHelperError ? <p className="text-destructive text-xs">{quickHelperError}</p> : null}
				<div className="flex justify-end">
					<Button
						type="button"
						variant="outline"
						data-testid="provider-pricing-convert-button"
						onClick={onAddQuickOverride}
						disabled={!hasUpdateProviderAccess}
					>
						换算到 JSON 草稿（未保存）
					</Button>
				</div>
			</div>

			<Textarea
				data-testid="provider-pricing-overrides-json-input"
				value={overridesJSON}
				onChange={(event) => {
					setOverridesJSON(event.target.value);
					setHasUserEdits(true);
				}}
				rows={18}
				className="font-mono text-xs"
				disabled={!hasUpdateProviderAccess}
				placeholder={`[
  {
    "model_pattern": "gpt-4o*",
    "match_type": "wildcard",
    "request_types": ["chat_completion"],
    "input_cost_per_token": 0.000005,
    "output_cost_per_token": 0.000015
  }
]`}
			/>

			{validationError ? <p className="text-destructive text-xs">{validationError}</p> : null}
			{isDirty ? (
				<p className="text-xs text-amber-600 dark:text-amber-400">
					当前 JSON 有未保存改动。切换中转站前请点击“保存价格覆盖”，或者点“重置”放弃改动。
				</p>
			) : null}

			<div className="flex justify-end gap-2">
				<Button
					type="button"
					variant="outline"
					data-testid="provider-pricing-overrides-normalize-button"
					onClick={onNormalizeJSON}
					disabled={!hasUpdateProviderAccess}
				>
					整理 JSON
				</Button>
				<Button
					type="button"
					variant="outline"
					data-testid="provider-pricing-overrides-reset-button"
					onClick={onReset}
					disabled={!hasUpdateProviderAccess || !isDirty}
				>
					重置
				</Button>
				<Button
					type="button"
					data-testid="provider-pricing-overrides-save-button"
					onClick={onSave}
					isLoading={isUpdatingProvider}
					disabled={!hasUpdateProviderAccess || !isDirty}
				>
					保存价格覆盖
				</Button>
			</div>
		</div>
	);
}
