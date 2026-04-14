"use client";

import { useEffect, useRef, useState } from "react";
import isEqual from "lodash.isequal";
import { ArrowRight, Info, Loader2, Save, Settings2, Shield } from "lucide-react";
import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useToast } from "@/hooks/use-toast";
import { getErrorMessage } from "@/lib/store/apis/baseApi";
import { useUpdateHealthDetectionConfigMutation } from "@/lib/store/apis/routingRulesApi";
import type { HealthDetectionConfigResponse } from "@/lib/types/routingRules";

import {
	buildHealthDetectionUpdatePayload,
	createHealthDetectionFormState,
	getDetectionModeLabel,
	type HealthDetectionFormState,
} from "./healthDetectionConfig";

interface HealthDetectionSettingsCardProps {
	config?: HealthDetectionConfigResponse;
	error?: unknown;
	isLoading: boolean;
	isFetching: boolean;
	onRetry: () => void;
}

const fieldDescriptions = {
	active_health_probe_interval_seconds: "How often the background loop checks for stale targets.",
	active_health_probe_passive_freshness_seconds: "If passive traffic touched a target recently, active probing stays quiet.",
	active_health_probe_timeout_seconds: "Maximum time allowed for one lightweight probe request.",
	active_health_probe_max_concurrency: "How many targets can be probed at the same time in one scan.",
} as const;

export default function HealthDetectionSettingsCard({ config, error, isLoading, isFetching, onRetry }: HealthDetectionSettingsCardProps) {
	const { toast } = useToast();
	const [updateConfig, { isLoading: isSaving }] = useUpdateHealthDetectionConfigMutation();
	const baselineRef = useRef<HealthDetectionFormState | null>(null);
	const [form, setForm] = useState<HealthDetectionFormState | null>(null);

	useEffect(() => {
		if (!config) {
			return;
		}
		const next = createHealthDetectionFormState(config);
		setForm((current) => {
			if (current === null || (baselineRef.current !== null && isEqual(current, baselineRef.current))) {
				return next;
			}
			return current;
		});
		baselineRef.current = next;
	}, [config]);

	const isDirty = form !== null && baselineRef.current !== null && !isEqual(form, baselineRef.current);
	const parametersDisabled = !form?.editable || form?.mode === "passive";
	const hasInvalidNumbers =
		form !== null &&
		[
			form.active_health_probe_interval_seconds,
			form.active_health_probe_passive_freshness_seconds,
			form.active_health_probe_timeout_seconds,
			form.active_health_probe_max_concurrency,
		].some((value) => value < 1);

	const saveDisabled = !form || !form.editable || !isDirty || hasInvalidNumbers || isSaving;
	const discardDisabled = !form || !isDirty || isSaving;

	const setNumericField = (
		field:
			| "active_health_probe_interval_seconds"
			| "active_health_probe_passive_freshness_seconds"
			| "active_health_probe_timeout_seconds"
			| "active_health_probe_max_concurrency",
		value: string,
	) => {
		const parsed = Number.parseInt(value, 10);
		setForm((current) =>
			current
				? {
						...current,
						[field]: Number.isNaN(parsed) ? 0 : parsed,
					}
				: current,
		);
	};

	const handleSave = async () => {
		if (!form || saveDisabled) {
			return;
		}
		try {
			const saved = await updateConfig(buildHealthDetectionUpdatePayload(form)).unwrap();
			const next = createHealthDetectionFormState(saved);
			baselineRef.current = next;
			setForm(next);
			toast({
				title: "Health detection updated",
				description: "Adaptive Routing will use the new detection settings immediately.",
			});
		} catch (saveError) {
			toast({
				title: "Failed to update health detection",
				description: getErrorMessage(saveError),
				variant: "destructive",
			});
		}
	};

	const handleDiscard = () => {
		if (!baselineRef.current) {
			return;
		}
		setForm(baselineRef.current);
	};

	return (
		<Card data-testid="adaptive-routing-health-detection-card">
			<CardHeader className="border-b">
				<div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
					<div className="space-y-2">
						<div className="flex items-center gap-2">
							<Settings2 className="text-muted-foreground h-4 w-4" />
							<CardTitle>Health Detection</CardTitle>
							{form ? (
								<Badge variant="outline" className="text-xs">
									{getDetectionModeLabel(form.mode)}
								</Badge>
							) : null}
						</div>
						<CardDescription>
							Global detection mode for Adaptive Routing. Passive signals stay first. Active probes only fill the gap when a target has not
							been observed recently.
						</CardDescription>
					</div>
					<div className="flex flex-wrap items-center gap-2">
						<Button asChild variant="outline" size="sm">
							<Link href="/workspace/routing-rules">
								Open Routing Rules
								<ArrowRight className="h-4 w-4" />
							</Link>
						</Button>
					</div>
				</div>
			</CardHeader>
			<CardContent className="space-y-6">
				{isLoading && !form ? (
					<div className="text-muted-foreground flex items-center gap-2 py-6 text-sm">
						<Loader2 className="h-4 w-4 animate-spin" />
						Loading health detection settings…
					</div>
				) : error ? (
					<div className="border-destructive/30 bg-destructive/5 rounded-sm border p-4 text-sm">
						<p className="font-medium">Unable to load health detection settings.</p>
						<p className="text-muted-foreground mt-1">{getErrorMessage(error)}</p>
						<Button variant="outline" size="sm" onClick={onRetry} disabled={isFetching || isSaving} className="mt-3">
							Retry
						</Button>
					</div>
				) : form ? (
					<>
						{!form.editable ? (
							<div className="rounded-sm border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/20 dark:text-amber-200">
								<div className="flex items-start gap-2">
									<Info className="mt-0.5 h-4 w-4 shrink-0" />
									<p>{form.read_only_reason}</p>
								</div>
							</div>
						) : null}

						<div className="grid gap-6 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1.4fr)]">
							<div className="space-y-3">
								<div className="space-y-1">
									<label className="text-sm font-medium">Detection Mode</label>
									<p className="text-muted-foreground text-xs">
										Choose whether the gateway should rely only on real traffic or also run lightweight probes.
									</p>
								</div>
								<Select
									value={form.mode}
									onValueChange={(value) =>
										setForm((current) =>
											current
												? {
														...current,
														mode: value as HealthDetectionFormState["mode"],
													}
												: current,
										)
									}
									disabled={!form.editable || isSaving}
								>
									<SelectTrigger className="w-full" data-testid="adaptive-routing-detection-mode">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										<SelectItem value="passive">Passive only</SelectItem>
										<SelectItem value="hybrid">Hybrid (Passive + Active)</SelectItem>
									</SelectContent>
								</Select>
								<div className="bg-muted/20 rounded-sm border p-3 text-sm">
									<div className="mb-1 flex items-center gap-2 font-medium">
										<Shield className="h-4 w-4" />
										{getDetectionModeLabel(form.mode)}
									</div>
									<p className="text-muted-foreground text-xs">
										{form.mode === "hybrid"
											? "Use passive signals first. When a target has not been observed recently, run a lightweight active probe."
											: "Use real request outcomes only. No background probes."}
									</p>
								</div>
							</div>

							<div className="grid gap-4 sm:grid-cols-2">
								<div className="space-y-2">
									<label className="text-sm font-medium">Probe interval (seconds)</label>
									<Input
										type="number"
										min={1}
										value={form.active_health_probe_interval_seconds}
										disabled={parametersDisabled || isSaving}
										onChange={(event) => setNumericField("active_health_probe_interval_seconds", event.target.value)}
										data-testid="adaptive-routing-probe-interval"
									/>
									<p className="text-muted-foreground text-xs">{fieldDescriptions.active_health_probe_interval_seconds}</p>
								</div>
								<div className="space-y-2">
									<label className="text-sm font-medium">Passive freshness window (seconds)</label>
									<Input
										type="number"
										min={1}
										value={form.active_health_probe_passive_freshness_seconds}
										disabled={parametersDisabled || isSaving}
										onChange={(event) => setNumericField("active_health_probe_passive_freshness_seconds", event.target.value)}
										data-testid="adaptive-routing-passive-freshness"
									/>
									<p className="text-muted-foreground text-xs">{fieldDescriptions.active_health_probe_passive_freshness_seconds}</p>
								</div>
								<div className="space-y-2">
									<label className="text-sm font-medium">Probe timeout (seconds)</label>
									<Input
										type="number"
										min={1}
										value={form.active_health_probe_timeout_seconds}
										disabled={parametersDisabled || isSaving}
										onChange={(event) => setNumericField("active_health_probe_timeout_seconds", event.target.value)}
										data-testid="adaptive-routing-probe-timeout"
									/>
									<p className="text-muted-foreground text-xs">{fieldDescriptions.active_health_probe_timeout_seconds}</p>
								</div>
								<div className="space-y-2">
									<label className="text-sm font-medium">Max concurrency</label>
									<Input
										type="number"
										min={1}
										value={form.active_health_probe_max_concurrency}
										disabled={parametersDisabled || isSaving}
										onChange={(event) => setNumericField("active_health_probe_max_concurrency", event.target.value)}
										data-testid="adaptive-routing-max-concurrency"
									/>
									<p className="text-muted-foreground text-xs">{fieldDescriptions.active_health_probe_max_concurrency}</p>
								</div>
							</div>
						</div>

						{hasInvalidNumbers ? <p className="text-destructive text-xs">All numeric settings must be at least 1.</p> : null}
					</>
				) : null}
			</CardContent>
			<CardFooter className="justify-end gap-2 border-t">
				<Button variant="outline" onClick={handleDiscard} disabled={discardDisabled} dataTestId="adaptive-routing-discard">
					Discard Changes
				</Button>
				<Button onClick={handleSave} disabled={saveDisabled} isLoading={isSaving} dataTestId="adaptive-routing-save">
					{!isSaving ? <Save className="h-4 w-4" /> : null}
					Save
				</Button>
			</CardFooter>
		</Card>
	);
}
