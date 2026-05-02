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
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { z } from "zod";
import {
	buildProviderPricingOverridesSavePayload,
	createTokenPricingOverrideFromPerMillion,
	formatPerTokenCost,
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
	const [quickMatchType, setQuickMatchType] = useState<PricingOverrideMatchType>("wildcard");
	const [quickInputPerMillion, setQuickInputPerMillion] = useState("");
	const [quickOutputPerMillion, setQuickOutputPerMillion] = useState("");
	const [quickHelperError, setQuickHelperError] = useState("");
	const isDirty = hasUserEdits && overridesJSON !== initialValue;

	useEffect(() => {
		if (isDirty) {
			return;
		}
		setOverridesJSON(initialValue);
		setValidationError("");
	}, [initialValue, isDirty, provider.name]);

	useEffect(() => {
		dispatch(setProviderFormDirtyState(isDirty));
	}, [dispatch, isDirty]);

	const onReset = () => {
		setOverridesJSON(initialValue);
		setValidationError("");
		setQuickHelperError("");
		setHasUserEdits(false);
	};

	const parseOverridesJSON = () => {
		let parsed: unknown;
		try {
			parsed = JSON.parse(overridesJSON);
		} catch {
			setValidationError("Invalid JSON format.");
			return null;
		}

		const validated = pricingOverridesArraySchema.safeParse(parsed);
		if (!validated.success) {
			setValidationError(validated.error.issues[0]?.message || "Invalid pricing overrides configuration.");
			return null;
		}

		setValidationError("");
		return validated.data;
	};

	const onAddQuickOverride = () => {
		const currentOverrides = parseOverridesJSON();
		if (!currentOverrides) {
			return;
		}

		try {
			const override = createTokenPricingOverrideFromPerMillion({
				modelPattern: quickModelPattern,
				matchType: quickMatchType,
				requestTypes: defaultTokenPricingRequestTypes,
				inputCostPerMillionTokens: quickInputPerMillion,
				outputCostPerMillionTokens: quickOutputPerMillion,
			});
			setQuickHelperError("");
			setOverridesJSON(toPrettyJSON([...currentOverrides, override]));
			setHasUserEdits(true);
			toast.success("Converted per-million token prices to per-token JSON");
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
			toast.success("Pricing overrides updated successfully");
			setOverridesJSON(toPrettyJSON(validatedOverrides));
			setHasUserEdits(false);
		} catch (err) {
			toast.error("Failed to update pricing overrides", {
				description: getErrorMessage(err),
			});
		}
	};

	return (
		<div className="space-y-4 px-6 pb-6">
			<div className="space-y-1">
				<p className="text-sm font-medium">Provider Pricing Overrides</p>
				<p className="text-muted-foreground text-xs">
					Use the converter for token pricing, or edit the JSON directly. Match precedence is exact &gt; wildcard &gt; regex.
					Unspecified fields fall back to datasheet pricing.
				</p>
			</div>

			<div className="space-y-3 rounded-sm border p-4">
				<div className="space-y-1">
					<p className="text-sm font-medium">Token Price Converter</p>
					<p className="text-muted-foreground text-xs">
						Enter prices as dollars per 1M tokens. The saved JSON uses dollars per token.
					</p>
				</div>
				<div className="grid gap-3 md:grid-cols-2">
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-model-pattern" className="text-xs">
							Model Pattern
						</Label>
						<Input
							id="pricing-override-model-pattern"
							data-testid="provider-pricing-model-pattern-input"
							value={quickModelPattern}
							onChange={(event) => setQuickModelPattern(event.target.value)}
							placeholder="gemini-2.5-pro*"
							disabled={!hasUpdateProviderAccess}
						/>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-match-type" className="text-xs">
							Match Type
						</Label>
						<Select value={quickMatchType} onValueChange={(value) => setQuickMatchType(value as PricingOverrideMatchType)}>
							<SelectTrigger
								id="pricing-override-match-type"
								data-testid="provider-pricing-match-type-select"
								disabled={!hasUpdateProviderAccess}
							>
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value="exact">Exact</SelectItem>
								<SelectItem value="wildcard">Wildcard</SelectItem>
								<SelectItem value="regex">Regex</SelectItem>
							</SelectContent>
						</Select>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-input-price" className="text-xs">
							Input Price ($/1M input tokens)
						</Label>
						<Input
							id="pricing-override-input-price"
							data-testid="provider-pricing-input-per-million-input"
							type="number"
							min="0"
							step="0.000001"
							value={quickInputPerMillion}
							onChange={(event) => setQuickInputPerMillion(event.target.value)}
							placeholder="1.25"
							disabled={!hasUpdateProviderAccess}
						/>
						<p className="text-muted-foreground text-[11px]">
							Per token:{" "}
							{formatPerTokenCost(quickInputPerMillion ? Number(quickInputPerMillion) / 1_000_000 : undefined) || "—"}
						</p>
					</div>
					<div className="space-y-1.5">
						<Label htmlFor="pricing-override-output-price" className="text-xs">
							Output Price ($/1M output tokens)
						</Label>
						<Input
							id="pricing-override-output-price"
							data-testid="provider-pricing-output-per-million-input"
							type="number"
							min="0"
							step="0.000001"
							value={quickOutputPerMillion}
							onChange={(event) => setQuickOutputPerMillion(event.target.value)}
							placeholder="10"
							disabled={!hasUpdateProviderAccess}
						/>
						<p className="text-muted-foreground text-[11px]">
							Per token:{" "}
							{formatPerTokenCost(quickOutputPerMillion ? Number(quickOutputPerMillion) / 1_000_000 : undefined) || "—"}
						</p>
					</div>
				</div>
				<p className="text-muted-foreground text-xs">
					Request types added by the converter: chat_completion and chat_completion_stream.
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
						Convert and Add to JSON
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

			<div className="flex justify-end gap-2">
				<Button
					type="button"
					variant="outline"
					data-testid="provider-pricing-overrides-normalize-button"
					onClick={onNormalizeJSON}
					disabled={!hasUpdateProviderAccess}
				>
					Normalize JSON
				</Button>
				<Button
					type="button"
					variant="outline"
					data-testid="provider-pricing-overrides-reset-button"
					onClick={onReset}
					disabled={!hasUpdateProviderAccess || !isDirty}
				>
					Reset
				</Button>
				<Button
					type="button"
					data-testid="provider-pricing-overrides-save-button"
					onClick={onSave}
					isLoading={isUpdatingProvider}
					disabled={!hasUpdateProviderAccess || !isDirty}
				>
					Save Pricing Overrides
				</Button>
			</div>
		</div>
	);
}
