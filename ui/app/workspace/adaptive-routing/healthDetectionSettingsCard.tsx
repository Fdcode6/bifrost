"use client";

import { useEffect, useRef, useState } from "react";
import isEqual from "lodash.isequal";
import { Activity, ArrowRight, ChevronDown, Info, Loader2, Save, Shield } from "lucide-react";
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
	active_health_probe_interval_seconds: "开启后，系统按这个频率检查已启用的目标。",
	idle_pause_minutes: "目标连续这么久没有真实请求后暂停探测；真实请求回来后自动恢复。",
	active_health_probe_timeout_seconds: "单次轻量存活探测最多等待多久。",
	active_health_probe_max_concurrency: "每轮最多同时探测多少个目标。",
} as const;

export default function HealthDetectionSettingsCard({ config, error, isLoading, isFetching, onRetry }: HealthDetectionSettingsCardProps) {
	const { toast } = useToast();
	const [updateConfig, { isLoading: isSaving }] = useUpdateHealthDetectionConfigMutation();
	const baselineRef = useRef<HealthDetectionFormState | null>(null);
	const [form, setForm] = useState<HealthDetectionFormState | null>(null);
	const [advancedOpen, setAdvancedOpen] = useState(false);

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
			form.idle_pause_minutes,
			form.active_health_probe_interval_seconds,
			form.active_health_probe_timeout_seconds,
			form.active_health_probe_max_concurrency,
		].some((value) => value < 1);

	const saveDisabled = !form || !form.editable || !isDirty || hasInvalidNumbers || isSaving;
	const discardDisabled = !form || !isDirty || isSaving;

	const setNumericField = (
		field:
			| "idle_pause_minutes"
			| "active_health_probe_interval_seconds"
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
				title: "存活探测设置已更新",
			});
		} catch (saveError) {
			toast({
				title: "存活探测设置更新失败",
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
							<Activity className="text-muted-foreground h-4 w-4" />
							<CardTitle>存活探测</CardTitle>
							{form ? (
								<Badge variant="outline" className="text-xs">
									{getDetectionModeLabel(form.mode)}
								</Badge>
							) : null}
						</div>
						<CardDescription>后台探测只确认目标是否还能响应；路由健康、降级和 Cooldown 仍然只由真实请求决定。</CardDescription>
					</div>
					<div className="flex flex-wrap items-center gap-2">
						<Button asChild variant="outline" size="sm">
							<Link href="/workspace/routing-rules">
								打开路由规则
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
						正在加载存活探测设置...
					</div>
				) : error ? (
					<div className="border-destructive/30 bg-destructive/5 rounded-sm border p-4 text-sm">
						<p className="font-medium">无法加载存活探测设置。</p>
						<p className="text-muted-foreground mt-1">{getErrorMessage(error)}</p>
						<Button variant="outline" size="sm" onClick={onRetry} disabled={isFetching || isSaving} className="mt-3">
							重试
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
									<label className="text-sm font-medium">后台存活探测</label>
									<p className="text-muted-foreground text-xs">开启后只用于观察目标是否存活，不会冻结、恢复或调整路由优先级。</p>
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
										<SelectItem value="passive">关闭 - 只看真实请求</SelectItem>
										<SelectItem value="hybrid">开启 - 仅做存活探测</SelectItem>
									</SelectContent>
								</Select>
								<div className="bg-muted/20 rounded-sm border p-3 text-sm">
									<div className="mb-1 flex items-center gap-2 font-medium">
										<Shield className="h-4 w-4" />
										{getDetectionModeLabel(form.mode)}
									</div>
									<p className="text-muted-foreground text-xs">
										{form.mode === "hybrid"
											? "真实请求仍然决定路由健康；探测只更新存活状态和最后探测结果。"
											: "不会发起后台探测请求；路由仍然使用真实请求结果。"}
									</p>
								</div>
							</div>

							<div className="space-y-4">
								<div className="space-y-2">
									<label className="text-sm font-medium">探测频率（秒）</label>
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

								<div className="rounded-sm border">
									<button
										type="button"
										className="hover:bg-muted/40 flex w-full items-center justify-between px-3 py-2 text-left text-sm font-medium"
										onClick={() => setAdvancedOpen((open) => !open)}
										aria-expanded={advancedOpen}
										data-testid="adaptive-routing-probe-advanced-toggle"
									>
										<span>高级探测限制</span>
										<ChevronDown className={`h-4 w-4 transition-transform ${advancedOpen ? "rotate-180" : ""}`} />
									</button>
									{advancedOpen ? (
										<div className="grid gap-4 border-t p-3 sm:grid-cols-2">
											<div className="space-y-2">
												<label className="text-sm font-medium">探测超时（秒）</label>
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
												<label className="text-sm font-medium">最大并发</label>
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
											<div className="space-y-2 sm:col-span-2">
												<label className="text-sm font-medium">空闲暂停（分钟）</label>
												<Input
													type="number"
													min={1}
													value={form.idle_pause_minutes}
													disabled={parametersDisabled || isSaving}
													onChange={(event) => setNumericField("idle_pause_minutes", event.target.value)}
													data-testid="adaptive-routing-idle-pause"
												/>
												<p className="text-muted-foreground text-xs">{fieldDescriptions.idle_pause_minutes}</p>
											</div>
										</div>
									) : null}
								</div>
							</div>
						</div>

						{hasInvalidNumbers ? <p className="text-destructive text-xs">所有数值设置都必须至少为 1。</p> : null}
					</>
				) : null}
			</CardContent>
			<CardFooter className="justify-end gap-2 border-t">
				<Button variant="outline" onClick={handleDiscard} disabled={discardDisabled} dataTestId="adaptive-routing-discard">
					放弃更改
				</Button>
				<Button onClick={handleSave} disabled={saveDisabled} isLoading={isSaving} dataTestId="adaptive-routing-save">
					{!isSaving ? <Save className="h-4 w-4" /> : null}
					保存
				</Button>
			</CardFooter>
		</Card>
	);
}
