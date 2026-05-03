/**
 * Routing Rule Dialog (Sheet)
 * Create/Edit form for routing rules
 */

"use client";

import { useState, useEffect, useCallback } from "react";
import { useForm } from "react-hook-form";
import { RuleGroupType } from "react-querybuilder";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ModelMultiselect } from "@/components/ui/modelMultiselect";
import { X, Save, Plus, Trash2, ChevronDown, ChevronUp } from "lucide-react";
import {
	RoutingRule,
	RoutingRuleFormData,
	RoutingTargetFormData,
	RouteGroupFormData,
	CreateRoutingRuleRequest,
	DEFAULT_ROUTING_RULE_FORM_DATA,
	DEFAULT_ROUTING_TARGET,
	DEFAULT_HEALTH_POLICY,
	DEFAULT_ROUTE_GROUP,
	ROUTING_RULE_SCOPES,
} from "@/lib/types/routingRules";
import { useCreateRoutingRuleMutation, useUpdateRoutingRuleMutation, useGetRoutingRulesQuery } from "@/lib/store/apis/routingRulesApi";
import { useGetVirtualKeysQuery, useGetTeamsQuery, useGetCustomersQuery } from "@/lib/store/apis/governanceApi";
import { useGetProvidersQuery } from "@/lib/store/apis/providersApi";
import { toast } from "sonner";
import dynamic from "next/dynamic";
import { ProviderIconType, RenderProviderIcon } from "@/lib/constants/icons";
import { getProviderLabel } from "@/lib/constants/logs";
import { Separator } from "@/components/ui/separator";
import { getErrorMessage } from "@/lib/store";
import {
	getRouteGroupAvailableKeys,
	shouldShowRouteGroupKeySelector,
	updateRouteGroupTarget as applyRouteGroupTargetPatch,
} from "./routeGroupState";
import { validateRoutingRules, validateRateLimitAndBudgetRules } from "@/lib/utils/celConverterRouting";

interface RoutingRuleDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	editingRule?: RoutingRule | null;
	onSuccess?: () => void;
}

const defaultQuery: RuleGroupType = {
	combinator: "and",
	rules: [],
};

// Dynamically import CEL builder to avoid SSR issues
const CELRuleBuilder = dynamic(
	() =>
		import("@/app/workspace/routing-rules/components/celBuilder/celRuleBuilder").then((mod) => ({
			default: mod.CELRuleBuilder,
		})),
	{
		loading: () => <div className="text-sm text-gray-500">正在加载 CEL 条件编辑器...</div>,
		ssr: false,
	},
);

export function RoutingRuleSheet({ open, onOpenChange, editingRule, onSuccess }: RoutingRuleDialogProps) {
	const { data: rulesData } = useGetRoutingRulesQuery();
	const rules = rulesData?.rules || [];
	const { data: providersData = [] } = useGetProvidersQuery();
	const { data: vksData = { virtual_keys: [] } } = useGetVirtualKeysQuery();
	const { data: teamsData = { teams: [], count: 0, total_count: 0, limit: 0, offset: 0 } } = useGetTeamsQuery();
	const { data: customersData = { customers: [] } } = useGetCustomersQuery();
	const [createRoutingRule, { isLoading: isCreating }] = useCreateRoutingRuleMutation();
	const [updateRoutingRule, { isLoading: isUpdating }] = useUpdateRoutingRuleMutation();

	// State for targets and query (managed outside react-hook-form for complex nested structures)
	const [targets, setTargets] = useState<RoutingTargetFormData[]>([{ ...DEFAULT_ROUTING_TARGET }]);
	const [routeGroups, setRouteGroups] = useState<RouteGroupFormData[]>([]);
	const [query, setQuery] = useState<RuleGroupType>(defaultQuery);
	const [builderKey, setBuilderKey] = useState(0);
	const [healthAdvancedOpen, setHealthAdvancedOpen] = useState(false);

	const {
		register,
		handleSubmit,
		setValue,
		watch,
		reset,
		formState: { errors },
	} = useForm<RoutingRuleFormData>({
		defaultValues: DEFAULT_ROUTING_RULE_FORM_DATA,
	});

	const isEditing = !!editingRule;
	const isLoading = isCreating || isUpdating;
	const enabled = watch("enabled");
	const scope = watch("scope");
	const scopeId = watch("scope_id");
	const fallbacks = watch("fallbacks");
	const groupedEnabled = watch("grouped_routing_enabled");
	const healthPolicy = watch("health_policy");

	// Get available providers from configured providers, plus any provider already
	// referenced by the current targets, existing rules' targets, or rules' fallbacks
	// so edited/removed providers are still visible in the dropdown.
	const availableProviders = Array.from(
		new Set([
			...providersData.map((p) => p.name),
			...(targets.map((t) => t.provider).filter(Boolean) as string[]),
			...(rules.flatMap((r) => r.targets?.map((t) => t.provider).filter(Boolean) ?? []) as string[]),
			...rules.flatMap((r) => (r.fallbacks ?? []).map((f) => f.split("/")[0]?.trim()).filter(Boolean)),
		]),
	);

	// Initialize form data when editing rule changes
	useEffect(() => {
		if (editingRule) {
			setValue("id", editingRule.id);
			setValue("name", editingRule.name);
			setValue("description", editingRule.description);
			setValue("cel_expression", editingRule.cel_expression);
			setValue("fallbacks", editingRule.fallbacks || []);
			setValue("scope", editingRule.scope);
			setValue("scope_id", editingRule.scope_id || "");
			setValue("priority", editingRule.priority);
			setValue("enabled", editingRule.enabled);
			setValue("grouped_routing_enabled", editingRule.grouped_routing_enabled || false);
			setValue("health_policy", { ...DEFAULT_HEALTH_POLICY, ...(editingRule.health_policy || {}) });
			if (editingRule.targets && editingRule.targets.length > 0) {
				setTargets(
					editingRule.targets.map((t) => ({
						...DEFAULT_ROUTING_TARGET,
						provider: t.provider || "",
						model: t.model || "",
						key_id: t.key_id || "",
						weight: t.weight,
					})),
				);
			} else {
				setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
			}
			if (editingRule.route_groups && editingRule.route_groups.length > 0) {
				setRouteGroups(
					editingRule.route_groups.map((g) => ({
						name: g.name,
						retry_limit: g.retry_limit,
						fallback_only: g.fallback_only ?? false,
						targets: g.targets.map((t) => ({
							provider: t.provider || "",
							model: t.model || "",
							key_id: t.key_id || "",
							weight: t.weight,
						})),
					})),
				);
			} else {
				setRouteGroups([]);
			}
			// Restore the query object if it exists, otherwise use default
			if (editingRule.query) {
				setQuery(editingRule.query);
			} else {
				setQuery(defaultQuery);
			}
			setBuilderKey((prev) => prev + 1);
		} else {
			reset();
			setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
			setRouteGroups([]);
			setQuery(defaultQuery);
			setBuilderKey((prev) => prev + 1);
		}
	}, [editingRule, open, setValue, reset]);

	const handleQueryChange = useCallback(
		(expression: string, newQuery: RuleGroupType) => {
			setValue("cel_expression", expression);
			setQuery(newQuery);
		},
		[setValue],
	);

	const addTarget = () => {
		const remaining = 1 - targets.reduce((sum, t) => sum + (t.weight || 0), 0);
		setTargets((prev) => [...prev, { ...DEFAULT_ROUTING_TARGET, weight: Math.max(0, parseFloat(remaining.toFixed(4))) }]);
	};

	const removeTarget = (index: number) => {
		setTargets((prev) => prev.filter((_, i) => i !== index));
	};

	const updateTarget = (index: number, field: keyof RoutingTargetFormData, value: string | number) => {
		setTargets((prev) => prev.map((t, i) => (i === index ? { ...t, [field]: value } : t)));
	};

	const totalWeight = targets.reduce((sum, t) => sum + (t.weight || 0), 0);

	const onSubmit = (data: RoutingRuleFormData) => {
		// Validate scope_id is required when scope is not global
		if (data.scope !== "global" && !data.scope_id?.trim()) {
			toast.error(`请选择 ${data.scope === "team" ? "Team" : data.scope === "customer" ? "Customer" : "Virtual Key"}`);
			return;
		}

		if (data.grouped_routing_enabled) {
			// Grouped routing validation
			if (routeGroups.length === 0) {
				toast.error("至少需要一个路由分组");
				return;
			}
			for (const group of routeGroups) {
				if (!group.name.trim()) {
					toast.error("每个路由分组都需要填写名称");
					return;
				}
				if (group.targets.length === 0) {
					toast.error(`路由分组 “${group.name}” 至少需要一个目标`);
					return;
				}
				const groupWeight = group.targets.reduce((sum, t) => sum + (t.weight || 0), 0);
				if (Math.abs(groupWeight - 1) > 0.001) {
					toast.error(`路由分组 “${group.name}” 的权重总和必须为 1（当前：${groupWeight.toFixed(4)}）`);
					return;
				}
				for (const t of group.targets) {
					if (!t.provider) {
						toast.error(`路由分组 “${group.name}” 中每个目标都必须选择 Provider`);
						return;
					}
					if (!t.model) {
						toast.error(`路由分组 “${group.name}” 中每个目标都必须选择 Model`);
						return;
					}
				}
			}
			if (data.health_policy) {
				if (data.health_policy.failure_threshold < 1) {
					toast.error("失败阈值至少为 1");
					return;
				}
				if (data.health_policy.failure_window_seconds < 1) {
					toast.error("失败窗口至少为 1 秒");
					return;
				}
				if (data.health_policy.cooldown_seconds < 1) {
					toast.error("Cooldown 至少为 1 秒");
					return;
				}
				if (data.health_policy.consecutive_failures < 0) {
					toast.error("连续失败次数必须大于等于 0");
					return;
				}
				if (data.health_policy.slow_ratio_threshold <= 0) {
					toast.error("慢请求占比阈值必须大于 0");
					return;
				}
				if (data.health_policy.slow_recovery_seconds < 0) {
					toast.error("慢请求恢复窗口必须大于等于 0");
					return;
				}
				if (data.health_policy.request_deadline_ms < 0) {
					toast.error("请求 Deadline 必须大于等于 0");
					return;
				}
				if (data.health_policy.soft_cooldown_multiplier < 1 || data.health_policy.cooldown_backoff_factor < 1) {
					toast.error("Cooldown 倍率必须至少为 1");
					return;
				}
			}
		} else {
			// Standard routing validation
			if (targets.length === 0) {
				toast.error("至少需要一个路由目标");
				return;
			}
			for (const t of targets) {
				if (t.weight <= 0) {
					toast.error("每个目标的权重必须大于 0");
					return;
				}
			}
			if (Math.abs(totalWeight - 1) > 0.001) {
				toast.error(`目标权重总和必须为 1，当前总和：${totalWeight.toFixed(4)}`);
				return;
			}
		}

		// Validate regex patterns in routing rules
		const regexErrors = validateRoutingRules(query);
		if (regexErrors.length > 0) {
			toast.error(`正则表达式无效:\n${regexErrors.join("\n")}`);
			return;
		}

		// Validate rate limit and budget rules
		const rateLimitErrors = validateRateLimitAndBudgetRules(query);
		if (rateLimitErrors.length > 0) {
			toast.error(`规则配置无效:\n${rateLimitErrors.join("\n")}`);
			return;
		}

		// Filter out incomplete fallbacks (empty provider)
		const validFallbacks = (data.fallbacks || []).filter((fb) => {
			const provider = fb.split("/")[0]?.trim();
			return provider && provider.length > 0;
		});

		const payload: CreateRoutingRuleRequest = {
			name: data.name,
			description: data.description,
			cel_expression: data.cel_expression,
			targets: data.grouped_routing_enabled
				? []
				: targets.map(({ provider, model, key_id, weight }) => ({
						provider: provider || undefined,
						model: model || undefined,
						key_id: key_id || undefined,
						weight,
					})),
			fallbacks: data.grouped_routing_enabled ? [] : validFallbacks,
			scope: data.scope,
			scope_id: data.scope === "global" ? undefined : data.scope_id || undefined,
			priority: data.priority,
			enabled: data.enabled,
			query: query,
			grouped_routing_enabled: data.grouped_routing_enabled,
			health_policy: data.grouped_routing_enabled ? data.health_policy : undefined,
			route_groups: data.grouped_routing_enabled
				? routeGroups.map((g) => ({
						name: g.name,
						retry_limit: g.retry_limit,
						fallback_only: g.fallback_only,
						targets: g.targets.map(({ provider, model, key_id, weight }) => ({
							provider,
							model,
							key_id: key_id || undefined,
							weight,
						})),
					}))
				: undefined,
		};

		const submitPromise =
			isEditing && editingRule
				? updateRoutingRule({
						id: editingRule.id,
						data: payload,
					}).unwrap()
				: createRoutingRule(payload).unwrap();

		submitPromise
			.then(() => {
				toast.success(isEditing ? "路由规则已更新" : "路由规则已创建");
				reset();
				setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
				setRouteGroups([]);
				setQuery(defaultQuery);
				setBuilderKey((prev) => prev + 1);
				onOpenChange(false);
				onSuccess?.();
			})
			.catch((error: any) => {
				toast.error(getErrorMessage(error));
			});
	};

	const handleCancel = () => {
		reset();
		setTargets([{ ...DEFAULT_ROUTING_TARGET }]);
		setRouteGroups([]);
		setQuery(defaultQuery);
		setBuilderKey((prev) => prev + 1);
		onOpenChange(false);
	};

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="flex w-full min-w-1/2 flex-col gap-4 overflow-x-hidden p-8">
				<SheetHeader className="flex flex-col items-start">
					<SheetTitle>{isEditing ? "编辑路由规则" : "新建路由规则"}</SheetTitle>
					<SheetDescription>{isEditing ? "更新这条路由规则的配置。" : "创建一条基于 CEL 条件的请求分流规则。"}</SheetDescription>
				</SheetHeader>

				<form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
					{/* Rule Name */}
					<div className="space-y-3">
						<Label htmlFor="name">
							规则名称 <span className="text-red-500">*</span>
						</Label>
						<Input id="name" placeholder="例如：把 GPT-4 发到 Azure" {...register("name", { required: "规则名称必填", maxLength: 255 })} />
						{errors.name && <p className="text-destructive text-sm">{errors.name.message}</p>}
					</div>

					{/* Description */}
					<div className="space-y-3">
						<Label htmlFor="description">描述</Label>
						<Textarea id="description" placeholder="说明这条规则的用途..." rows={2} {...register("description")} />
					</div>

					{/* Enabled Switch */}
					<div className="flex items-center justify-between rounded-lg border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="enabled">启用规则</Label>
							<p className="text-muted-foreground text-sm">规则会对匹配请求生效</p>
						</div>
						<Switch id="enabled" checked={enabled} onCheckedChange={(checked) => setValue("enabled", checked)} />
					</div>

					{/* Scope and Priority - Side by Side */}
					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-3">
							<Label htmlFor="scope">作用范围</Label>
							<Select
								value={scope}
								onValueChange={(value) => {
									setValue("scope", value as any);
									// Clear scope_id when scope changes
									setValue("scope_id", "");
								}}
							>
								<SelectTrigger className="w-full">
									<SelectValue placeholder="选择作用范围..." />
								</SelectTrigger>
								<SelectContent>
									{ROUTING_RULE_SCOPES.map((scopeOption) => (
										<SelectItem key={scopeOption.value} value={scopeOption.value}>
											{scopeOption.label}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>

						<div className="space-y-3">
							<Label htmlFor="priority">
								优先级 <span className="text-red-500">*</span>
							</Label>
							<Input
								id="priority"
								type="number"
								min={0}
								max={1000}
								{...register("priority", {
									required: "优先级必填",
									min: { value: 0, message: "优先级必须 ≥ 0" },
									max: { value: 1000, message: "优先级必须 ≤ 1000" },
									valueAsNumber: true,
								})}
							/>
							<p className="text-muted-foreground text-xs">数字越小优先级越高（0 最高）</p>
							{errors.priority && <p className="text-destructive text-sm">{errors.priority.message}</p>}
						</div>
					</div>

					{scope !== "global" && (
						<div className="space-y-2">
							<Label htmlFor="scope_id">
								{scope === "team" ? "Team" : scope === "customer" ? "Customer" : "Virtual Key"} <span className="text-red-500">*</span>
							</Label>
							{scope === "team" && teamsData.teams.length > 0 && (
								<Select value={scopeId || ""} onValueChange={(value) => setValue("scope_id", value)}>
									<SelectTrigger className="w-full">
										<SelectValue placeholder="选择 Team..." />
									</SelectTrigger>
									<SelectContent>
										{teamsData.teams.map((team) => (
											<SelectItem key={team.id} value={team.id}>
												{team.name}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							)}
							{scope === "customer" && customersData.customers.length > 0 && (
								<Select value={scopeId || ""} onValueChange={(value) => setValue("scope_id", value)}>
									<SelectTrigger className="w-full">
										<SelectValue placeholder="选择 Customer..." />
									</SelectTrigger>
									<SelectContent>
										{customersData.customers.map((customer) => (
											<SelectItem key={customer.id} value={customer.id}>
												{customer.name}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							)}
							{scope === "virtual_key" && vksData.virtual_keys.length > 0 && (
								<Select value={scopeId || ""} onValueChange={(value) => setValue("scope_id", value)}>
									<SelectTrigger className="w-full">
										<SelectValue placeholder="选择 Virtual Key..." />
									</SelectTrigger>
									<SelectContent>
										{vksData.virtual_keys.map((vk) => (
											<SelectItem key={vk.id} value={vk.id}>
												{vk.name}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							)}
							{((scope === "team" && teamsData.teams.length === 0) ||
								(scope === "customer" && customersData.customers.length === 0) ||
								(scope === "virtual_key" && vksData.virtual_keys.length === 0)) && (
								<p className="text-muted-foreground text-sm">
									暂无可用的 {scope === "team" ? "Team" : scope === "customer" ? "Customer" : "Virtual Key"}
								</p>
							)}
							{errors.scope_id && <p className="text-destructive text-sm">{errors.scope_id.message}</p>}
						</div>
					)}

					<Separator />

					{/* CEL Rule Builder */}
					<div className="space-y-3">
						<Label>规则条件</Label>
						<p className="text-muted-foreground text-sm">构建这条规则生效的条件；留空则匹配所有请求。</p>
						<CELRuleBuilder
							key={builderKey}
							initialQuery={query}
							onChange={handleQueryChange}
							providers={availableProviders}
							models={[]}
							allowCustomModels={true}
						/>
					</div>

					{/* Note about Token/Request Limits and Budget Configuration */}
					<p className="text-muted-foreground text-xs">
						注意：使用 Token 限制、Request 限制或预算条件前，请先在{" "}
						<strong>Model Providers → Configurations → {"{provider}"} → Governance</strong> (provider-level) 或{" "}
						<strong>Model Providers → Budgets & Limits</strong> (model-level) 中配置对应限制。
					</p>

					<Separator />

					{/* Grouped Health Routing Toggle */}
					<div className="flex items-center justify-between rounded-lg border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="grouped_routing_enabled">分组健康路由</Label>
							<p className="text-muted-foreground text-sm">启用按路由分组和健康状态自动切换的请求分流</p>
						</div>
						<Switch
							id="grouped_routing_enabled"
							checked={groupedEnabled}
							onCheckedChange={(checked) => setValue("grouped_routing_enabled", checked)}
							data-testid="grouped-routing-toggle"
						/>
					</div>

					{groupedEnabled ? (
						<>
							{/* Health Policy */}
							<div className="space-y-3">
								<Label>健康策略</Label>
								<p className="text-muted-foreground text-xs">
									配置目标什么时候进入 Cooldown。窗口失败和连续失败任意一个触发，目标都会从常规路由里暂时降级。
								</p>
								<div className="grid grid-cols-2 gap-3">
									<div className="space-y-1.5">
										<Label htmlFor="hp-threshold" className="text-xs">
											窗口阈值
										</Label>
										<Input
											id="hp-threshold"
											type="number"
											min={1}
											value={healthPolicy?.failure_threshold ?? DEFAULT_HEALTH_POLICY.failure_threshold}
											onChange={(e) =>
												setValue("health_policy", {
													...healthPolicy,
													failure_threshold: parseInt(e.target.value) || 1,
												})
											}
											data-testid="health-policy-threshold"
										/>
										<p className="text-muted-foreground text-[10px]">窗口内失败达到该数量后进入 Cooldown</p>
									</div>
									<div className="space-y-1.5">
										<Label htmlFor="hp-window" className="text-xs">
											失败窗口（秒）
										</Label>
										<Input
											id="hp-window"
											type="number"
											min={1}
											value={healthPolicy?.failure_window_seconds ?? DEFAULT_HEALTH_POLICY.failure_window_seconds}
											onChange={(e) =>
												setValue("health_policy", {
													...healthPolicy,
													failure_window_seconds: parseInt(e.target.value) || 1,
												})
											}
											data-testid="health-policy-window"
										/>
										<p className="text-muted-foreground text-[10px]">统计失败次数的滑动时间窗口</p>
									</div>
									<div className="space-y-1.5">
										<Label htmlFor="hp-consecutive" className="text-xs">
											连续失败
										</Label>
										<Input
											id="hp-consecutive"
											type="number"
											min={0}
											value={healthPolicy?.consecutive_failures ?? DEFAULT_HEALTH_POLICY.consecutive_failures}
											onChange={(e) =>
												setValue("health_policy", {
													...healthPolicy,
													consecutive_failures: Number.isNaN(parseInt(e.target.value)) ? 0 : parseInt(e.target.value),
												})
											}
											data-testid="health-policy-consecutive"
										/>
										<p className="text-muted-foreground text-[10px]">0 表示使用窗口阈值</p>
									</div>
									<div className="space-y-1.5">
										<Label htmlFor="hp-cooldown" className="text-xs">
											Cooldown 冷却（秒）
										</Label>
										<Input
											id="hp-cooldown"
											type="number"
											min={1}
											value={healthPolicy?.cooldown_seconds ?? DEFAULT_HEALTH_POLICY.cooldown_seconds}
											onChange={(e) =>
												setValue("health_policy", {
													...healthPolicy,
													cooldown_seconds: parseInt(e.target.value) || 1,
												})
											}
											data-testid="health-policy-cooldown"
										/>
									</div>
								</div>

								<div className="rounded-md border">
									<button
										type="button"
										className="hover:bg-muted/40 flex w-full items-center justify-between px-3 py-2 text-left text-sm font-medium"
										onClick={() => setHealthAdvancedOpen((open) => !open)}
										data-testid="health-policy-advanced-toggle"
									>
										<span>高级健康信号</span>
										{healthAdvancedOpen ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
									</button>
									{healthAdvancedOpen && (
										<div className="grid grid-cols-2 gap-3 border-t p-3">
											<div className="space-y-1.5">
												<Label htmlFor="hp-slow-threshold" className="text-xs">
													慢请求阈值（毫秒）
												</Label>
												<Input
													id="hp-slow-threshold"
													type="number"
													min={1}
													value={healthPolicy?.slow_threshold_ms ?? DEFAULT_HEALTH_POLICY.slow_threshold_ms}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															slow_threshold_ms: parseInt(e.target.value) || DEFAULT_HEALTH_POLICY.slow_threshold_ms,
														})
													}
													data-testid="health-policy-slow-threshold"
												/>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-slow-window" className="text-xs">
													慢请求窗口大小
												</Label>
												<Input
													id="hp-slow-window"
													type="number"
													min={1}
													value={healthPolicy?.slow_window_size ?? DEFAULT_HEALTH_POLICY.slow_window_size}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															slow_window_size: parseInt(e.target.value) || DEFAULT_HEALTH_POLICY.slow_window_size,
														})
													}
													data-testid="health-policy-slow-window"
												/>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-slow-ratio" className="text-xs">
													慢请求占比阈值
												</Label>
												<Input
													id="hp-slow-ratio"
													type="number"
													min={0.01}
													step={0.01}
													value={healthPolicy?.slow_ratio_threshold ?? DEFAULT_HEALTH_POLICY.slow_ratio_threshold}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															slow_ratio_threshold: parseFloat(e.target.value) || DEFAULT_HEALTH_POLICY.slow_ratio_threshold,
														})
													}
													data-testid="health-policy-slow-ratio"
												/>
												<p className="text-muted-foreground text-[10px]">填 999 可关闭按慢请求占比降级</p>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-slow-recovery" className="text-xs">
													慢请求恢复窗口（秒）
												</Label>
												<Input
													id="hp-slow-recovery"
													type="number"
													min={0}
													value={healthPolicy?.slow_recovery_seconds ?? DEFAULT_HEALTH_POLICY.slow_recovery_seconds}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															slow_recovery_seconds: Number.isNaN(parseInt(e.target.value)) ? 0 : parseInt(e.target.value),
														})
													}
													data-testid="health-policy-slow-recovery"
												/>
												<p className="text-muted-foreground text-[10px]">0 表示关闭最近慢请求降级</p>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-soft-multiplier" className="text-xs">
													软失败 Cooldown 倍率
												</Label>
												<Input
													id="hp-soft-multiplier"
													type="number"
													min={1}
													step={0.1}
													value={healthPolicy?.soft_cooldown_multiplier ?? DEFAULT_HEALTH_POLICY.soft_cooldown_multiplier}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															soft_cooldown_multiplier: parseFloat(e.target.value) || DEFAULT_HEALTH_POLICY.soft_cooldown_multiplier,
														})
													}
													data-testid="health-policy-soft-multiplier"
												/>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-backoff" className="text-xs">
													Cooldown 退避倍率
												</Label>
												<Input
													id="hp-backoff"
													type="number"
													min={1}
													step={0.1}
													value={healthPolicy?.cooldown_backoff_factor ?? DEFAULT_HEALTH_POLICY.cooldown_backoff_factor}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															cooldown_backoff_factor: parseFloat(e.target.value) || DEFAULT_HEALTH_POLICY.cooldown_backoff_factor,
														})
													}
													data-testid="health-policy-backoff"
												/>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-cooldown-max" className="text-xs">
													最大 Cooldown（秒）
												</Label>
												<Input
													id="hp-cooldown-max"
													type="number"
													min={1}
													value={healthPolicy?.cooldown_max_seconds ?? DEFAULT_HEALTH_POLICY.cooldown_max_seconds}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															cooldown_max_seconds: parseInt(e.target.value) || DEFAULT_HEALTH_POLICY.cooldown_max_seconds,
														})
													}
													data-testid="health-policy-cooldown-max"
												/>
											</div>
											<div className="space-y-1.5">
												<Label htmlFor="hp-deadline" className="text-xs">
													请求 Deadline（毫秒）
												</Label>
												<Input
													id="hp-deadline"
													type="number"
													min={0}
													value={healthPolicy?.request_deadline_ms ?? DEFAULT_HEALTH_POLICY.request_deadline_ms}
													onChange={(e) =>
														setValue("health_policy", {
															...healthPolicy,
															request_deadline_ms: Number.isNaN(parseInt(e.target.value)) ? 0 : parseInt(e.target.value),
														})
													}
													data-testid="health-policy-deadline"
												/>
												<p className="text-muted-foreground text-[10px]">当前版本预留该字段；0 表示关闭</p>
											</div>
											<div className="col-span-2 flex items-center justify-between rounded-md border p-3">
												<div className="space-y-0.5">
													<Label htmlFor="hp-half-open" className="text-xs">
														Half-open 恢复探测
													</Label>
													<p className="text-muted-foreground text-[10px]">Cooldown 到期后允许一次恢复探测</p>
												</div>
												<Switch
													id="hp-half-open"
													checked={healthPolicy?.half_open_probe ?? DEFAULT_HEALTH_POLICY.half_open_probe}
													onCheckedChange={(checked) =>
														setValue("health_policy", {
															...healthPolicy,
															half_open_probe: checked,
														})
													}
													data-testid="health-policy-half-open"
												/>
											</div>
										</div>
									)}
								</div>
							</div>

							<Separator />

							{/* Route Groups */}
							<div className="space-y-3">
								<div className="flex items-center justify-between">
									<div>
										<Label>路由分组</Label>
										<p className="text-muted-foreground mt-0.5 text-xs">
											按分组顺序依次尝试；每个分组内按权重选择目标，并按 Retry 次数重试。
										</p>
									</div>
									<Button
										type="button"
										variant="outline"
										size="sm"
										onClick={() =>
											setRouteGroups((prev) => [
												...prev,
												{
													...DEFAULT_ROUTE_GROUP,
													name: `分组 ${prev.length + 1}`,
													targets: [{ provider: "", model: "", key_id: "", weight: 1 }],
												},
											])
										}
										className="shrink-0 gap-2"
										data-testid="route-group-add"
									>
										<Plus className="h-4 w-4" />
										添加分组
									</Button>
								</div>

								{routeGroups.length === 0 && (
									<p className="text-muted-foreground rounded-lg border border-dashed py-4 text-center text-sm">
										还没有配置路由分组，添加一个分组后即可开始配置。
									</p>
								)}

								<div className="space-y-4">
									{routeGroups.map((group, gi) => (
										<RouteGroupEditor
											key={gi}
											group={group}
											groupIndex={gi}
											availableProviders={availableProviders}
											providersData={providersData}
											onUpdate={(updated) => setRouteGroups((prev) => prev.map((g, i) => (i === gi ? updated : g)))}
											onRemove={() => setRouteGroups((prev) => prev.filter((_, i) => i !== gi))}
											onMoveUp={
												gi > 0
													? () =>
															setRouteGroups((prev) => {
																const next = [...prev];
																[next[gi - 1], next[gi]] = [next[gi], next[gi - 1]];
																return next;
															})
													: undefined
											}
											onMoveDown={
												gi < routeGroups.length - 1
													? () =>
															setRouteGroups((prev) => {
																const next = [...prev];
																[next[gi], next[gi + 1]] = [next[gi + 1], next[gi]];
																return next;
															})
													: undefined
											}
										/>
									))}
								</div>
							</div>
						</>
					) : (
						<>
							{/* Routing Targets (standard mode) */}
							<div className="space-y-3">
								<div className="flex items-center justify-between">
									<div>
										<Label>路由目标</Label>
										<p className="text-muted-foreground mt-0.5 text-xs">权重总和必须为 1。Provider 或 Model 留空时使用请求传入的值。</p>
									</div>
									<Button
										type="button"
										variant="outline"
										size="sm"
										onClick={addTarget}
										className="shrink-0 gap-2"
										data-testid="routing-rule-target-add"
									>
										<Plus className="h-4 w-4" />
										添加目标
									</Button>
								</div>

								<div className="space-y-3">
									{targets.map((target, index) => (
										<TargetRow
											key={index}
											target={target}
											index={index}
											availableProviders={availableProviders}
											providersData={providersData}
											showRemove={targets.length > 1}
											onUpdate={updateTarget}
											onRemove={removeTarget}
										/>
									))}
								</div>

								{/* Weight sum indicator */}
								<div
									className={`flex items-center justify-end gap-2 text-xs font-medium ${Math.abs(totalWeight - 1) > 0.001 ? "text-destructive" : "text-muted-foreground"}`}
								>
									总权重: {totalWeight.toFixed(4)}
									{Math.abs(totalWeight - 1) > 0.001 && <span className="text-destructive">（总和必须等于 1）</span>}
								</div>
							</div>

							{/* Fallbacks */}
							<div className="space-y-3">
								<div className="flex items-center justify-between">
									<Label>Fallback 兜底</Label>
									<Button
										type="button"
										variant="outline"
										size="sm"
										onClick={() => setValue("fallbacks", [...(fallbacks || []), ""])}
										className="gap-2"
									>
										<Plus className="h-4 w-4" />
										添加 Fallback
									</Button>
								</div>
								<div className="space-y-2">
									{(fallbacks || []).length === 0 ? (
										<p className="text-muted-foreground text-sm">还没有配置 Fallback</p>
									) : (
										(fallbacks || []).map((fallback, index) => {
											// Parse provider/model from fallback string
											const parts = fallback.split("/");
											const fbProvider = parts[0] || "";
											const fbModel = parts[1] || "";

											const handleProviderChange = (newProvider: string) => {
												const model = fbModel || "";
												const newFallback = `${newProvider}/${model}`;
												const newFallbacks = [...fallbacks];
												newFallbacks[index] = newFallback;
												setValue("fallbacks", newFallbacks);
											};

											const handleModelChange = (newModel: string) => {
												const prov = fbProvider || "";
												const newFallback = `${prov}/${newModel}`;
												const newFallbacks = [...fallbacks];
												newFallbacks[index] = newFallback;
												setValue("fallbacks", newFallbacks);
											};

											const handleRemove = () => {
												const newFallbacks = fallbacks.filter((_: string, i: number) => i !== index);
												setValue("fallbacks", newFallbacks);
											};

											return (
												<div key={index} className="flex items-center gap-2">
													<div className="flex-1">
														<Select value={fbProvider} onValueChange={handleProviderChange}>
															<SelectTrigger className="w-full">
																<SelectValue placeholder="选择 Provider..." />
															</SelectTrigger>
															<SelectContent>
																{availableProviders.map((prov) => (
																	<SelectItem key={prov} value={prov}>
																		<div className="flex items-center gap-2">
																			<RenderProviderIcon provider={prov as ProviderIconType} size="sm" className="h-4 w-4" />
																			<span>{getProviderLabel(prov)}</span>
																		</div>
																	</SelectItem>
																))}
															</SelectContent>
														</Select>
													</div>
													<div className="flex-1">
														<ModelMultiselect
															provider={fbProvider || undefined}
															value={fbModel}
															onChange={handleModelChange}
															placeholder="选择 Model..."
															isSingleSelect
															disabled={!fbProvider}
															className="!h-9 !min-h-9 w-full"
														/>
													</div>
													<Button
														type="button"
														variant="ghost"
														size="sm"
														onClick={handleRemove}
														className="h-9 px-2"
														aria-label={`删除 Fallback ${index + 1}`}
													>
														<Trash2 className="h-4 w-4" />
													</Button>
												</div>
											);
										})
									)}
								</div>
								<p className="text-muted-foreground text-xs">Fallback 会按配置顺序依次使用</p>
							</div>
						</>
					)}

					{/* Action Buttons */}
					<div className="flex justify-end gap-3">
						<Button type="button" variant="outline" onClick={handleCancel} disabled={isLoading}>
							<X className="h-4 w-4" />
							取消
						</Button>
						<Button type="submit" disabled={isLoading}>
							<Save className="h-4 w-4" />
							{isEditing ? "更新规则" : "保存规则"}
						</Button>
					</div>
				</form>
			</SheetContent>
		</Sheet>
	);
}

interface TargetRowProps {
	target: RoutingTargetFormData;
	index: number;
	availableProviders: string[];
	providersData: Array<{ name: string; keys: Array<{ id: string; name: string }> }>;
	showRemove: boolean;
	onUpdate: (index: number, field: keyof RoutingTargetFormData, value: string | number) => void;
	onRemove: (index: number) => void;
}

function TargetRow({ target, index, availableProviders, providersData, showRemove, onUpdate, onRemove }: TargetRowProps) {
	const availableKeys = target.provider ? (providersData.find((p) => p.name === target.provider)?.keys ?? []) : [];

	return (
		<div className="space-y-3 rounded-lg border p-3" data-testid={`routing-target-${index}`}>
			<div className="flex items-center justify-between">
				<span className="text-muted-foreground text-sm font-medium">目标 {index + 1}</span>
				<div className="flex items-center gap-2">
					<div className="flex items-center gap-1.5">
						<Label htmlFor={`routing-target-${index}-weight-input`} className="text-muted-foreground shrink-0 text-xs">
							权重
						</Label>
						<Input
							id={`routing-target-${index}-weight-input`}
							type="number"
							min={0.001}
							max={1}
							step={0.001}
							value={target.weight}
							onChange={(e) => onUpdate(index, "weight", parseFloat(e.target.value) || 0)}
							className="h-8 w-24 text-sm"
							data-testid={`routing-target-${index}-weight-input`}
						/>
					</div>
					{showRemove && (
						<Button
							type="button"
							variant="ghost"
							size="sm"
							onClick={() => onRemove(index)}
							className="h-8 w-8 p-0"
							aria-label={`删除目标 ${index + 1}`}
							data-testid={`routing-target-${index}-remove-button`}
						>
							<Trash2 className="h-3.5 w-3.5" />
						</Button>
					)}
				</div>
			</div>

			<div className="grid grid-cols-2 gap-3">
				<div className="space-y-1.5">
					<Label id={`routing-target-${index}-provider-label`} className="text-xs">
						Provider
					</Label>
					<div className="flex gap-1.5">
						<Select
							value={target.provider}
							onValueChange={(value) => {
								onUpdate(index, "provider", value);
								onUpdate(index, "model", "");
								onUpdate(index, "key_id", "");
							}}
						>
							<SelectTrigger
								id={`routing-target-${index}-provider-select`}
								aria-labelledby={`routing-target-${index}-provider-label`}
								className="h-9 flex-1 text-sm"
								data-testid={`routing-target-${index}-provider-select`}
							>
								<SelectValue placeholder="请求传入（可选）" />
							</SelectTrigger>
							<SelectContent>
								{availableProviders.map((prov) => (
									<SelectItem key={prov} value={prov}>
										<div className="flex items-center gap-2">
											<RenderProviderIcon provider={prov as ProviderIconType} size="sm" className="h-4 w-4" />
											<span>{getProviderLabel(prov)}</span>
										</div>
									</SelectItem>
								))}
							</SelectContent>
						</Select>
						{target.provider && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => {
									onUpdate(index, "provider", "");
									onUpdate(index, "model", "");
									onUpdate(index, "key_id", "");
								}}
								className="h-9 w-9 p-0"
								aria-label={`清空目标 ${index + 1} 的 Provider`}
								data-testid={`routing-target-${index}-provider-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>

				<div className="space-y-1.5">
					<Label id={`routing-target-${index}-model-label`} className="text-xs">
						Model
					</Label>
					<div className="flex gap-1.5">
						<div className="flex-1" data-testid={`routing-target-${index}-model-select`}>
							<ModelMultiselect
								provider={target.provider || undefined}
								value={target.model}
								onChange={(value) => onUpdate(index, "model", value)}
								placeholder="请求传入（可选）"
								isSingleSelect
								loadModelsOnEmptyProvider
								className="!h-9 !min-h-9"
								inputId={`routing-target-${index}-model-input`}
								ariaLabelledBy={`routing-target-${index}-model-label`}
							/>
						</div>
						{target.model && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => onUpdate(index, "model", "")}
								className="h-9 w-9 p-0"
								aria-label={`清空目标 ${index + 1} 的 Model`}
								data-testid={`routing-target-${index}-model-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>
			</div>

			{target.provider && (availableKeys.length > 0 || target.key_id) && (
				<div className="space-y-1.5">
					<Label id={`routing-target-${index}-apikey-label`} className="text-xs">
						API Key <span className="text-muted-foreground">（可选；留空则使用负载均衡选择）</span>
					</Label>
					<div className="flex gap-1.5">
						<Select value={target.key_id || ""} onValueChange={(value) => onUpdate(index, "key_id", value)}>
							<SelectTrigger
								id={`routing-target-${index}-apikey-select`}
								aria-labelledby={`routing-target-${index}-apikey-label`}
								className="h-9 flex-1 text-sm"
								data-testid={`routing-target-${index}-apikey-select`}
							>
								<SelectValue placeholder="选择 API Key（可选）" />
							</SelectTrigger>
							<SelectContent>
								{availableKeys.map((key) => (
									<SelectItem key={key.id} value={key.id}>
										{key.name}
									</SelectItem>
								))}
								{target.key_id && !availableKeys.some((k) => k.id === target.key_id) && (
									<SelectItem key={`pinned-${target.key_id}`} value={target.key_id}>
										（已固定）{target.key_id}
									</SelectItem>
								)}
							</SelectContent>
						</Select>
						{target.key_id && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => onUpdate(index, "key_id", "")}
								className="h-9 w-9 p-0"
								aria-label={`清空目标 ${index + 1} 的 API Key`}
								data-testid={`routing-target-${index}-apikey-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>
			)}
		</div>
	);
}

/* ─── Route Group Editor ────────────────────────────────── */

interface RouteGroupEditorProps {
	group: RouteGroupFormData;
	groupIndex: number;
	availableProviders: string[];
	providersData: Array<{ name: string; keys: Array<{ id: string; name: string }> }>;
	onUpdate: (group: RouteGroupFormData) => void;
	onRemove: () => void;
	onMoveUp?: () => void;
	onMoveDown?: () => void;
}

function RouteGroupEditor({
	group,
	groupIndex,
	availableProviders,
	providersData,
	onUpdate,
	onRemove,
	onMoveUp,
	onMoveDown,
}: RouteGroupEditorProps) {
	const [collapsed, setCollapsed] = useState(false);

	const addGroupTarget = () => {
		const remaining = 1 - group.targets.reduce((sum, t) => sum + (t.weight || 0), 0);
		onUpdate({
			...group,
			targets: [...group.targets, { provider: "", model: "", key_id: "", weight: Math.max(0, parseFloat(remaining.toFixed(4))) }],
		});
	};

	const updateGroupTarget = (index: number, patch: Partial<RoutingTargetFormData>) => {
		onUpdate(applyRouteGroupTargetPatch(group, index, patch));
	};

	const removeGroupTarget = (index: number) => {
		onUpdate({
			...group,
			targets: group.targets.filter((_, i) => i !== index),
		});
	};

	const groupWeight = group.targets.reduce((sum, t) => sum + (t.weight || 0), 0);

	return (
		<div className="space-y-0 rounded-lg border" data-testid={`route-group-${groupIndex}`}>
			{/* Group header */}
			<div className="bg-muted/30 flex items-center justify-between rounded-t-lg p-3">
				<button
					type="button"
					className="hover:text-foreground text-foreground/80 flex items-center gap-2 text-sm font-medium"
					onClick={() => setCollapsed(!collapsed)}
				>
					{collapsed ? <ChevronDown className="h-4 w-4" /> : <ChevronUp className="h-4 w-4" />}
					<span>{group.name || `分组 ${groupIndex + 1}`}</span>
					<span className="text-muted-foreground font-normal">（{group.targets.length} 个目标）</span>
				</button>
				<div className="flex items-center gap-1">
					{onMoveUp && (
						<Button type="button" variant="ghost" size="sm" onClick={onMoveUp} className="h-7 w-7 p-0" aria-label="上移分组">
							<ChevronUp className="h-3.5 w-3.5" />
						</Button>
					)}
					{onMoveDown && (
						<Button type="button" variant="ghost" size="sm" onClick={onMoveDown} className="h-7 w-7 p-0" aria-label="下移分组">
							<ChevronDown className="h-3.5 w-3.5" />
						</Button>
					)}
					<Button
						type="button"
						variant="ghost"
						size="sm"
						onClick={onRemove}
						className="text-destructive hover:text-destructive h-7 w-7 p-0"
						aria-label="删除分组"
						data-testid={`route-group-${groupIndex}-remove`}
					>
						<Trash2 className="h-3.5 w-3.5" />
					</Button>
				</div>
			</div>

			{!collapsed && (
				<div className="space-y-3 p-3">
					{/* Group name & retry limit */}
					<div className="grid grid-cols-2 gap-3">
						<div className="space-y-1.5">
							<Label className="text-xs">分组名称</Label>
							<Input
								value={group.name}
								onChange={(e) => onUpdate({ ...group, name: e.target.value })}
								placeholder="例如：Primary、Fallback"
								className="h-9 text-sm"
								data-testid={`route-group-${groupIndex}-name`}
							/>
						</div>
						<div className="space-y-1.5">
							<Label className="text-xs">Retry 次数</Label>
							<Input
								type="number"
								min={0}
								max={10}
								value={group.retry_limit}
								onChange={(e) => onUpdate({ ...group, retry_limit: parseInt(e.target.value) || 0 })}
								className="h-9 text-sm"
								data-testid={`route-group-${groupIndex}-retry`}
							/>
							<p className="text-muted-foreground text-[10px]">普通分组会尝试其他目标；Fallback 兜底组可重复同一目标</p>
						</div>
					</div>
					<div className="flex items-center justify-between rounded-md border p-3">
						<div className="space-y-0.5">
							<Label className="text-xs">仅作为 Fallback</Label>
							<p className="text-muted-foreground text-[10px]">排在普通分组之后，仅在普通分组不可用时使用</p>
						</div>
						<Switch
							checked={group.fallback_only}
							onCheckedChange={(checked) => onUpdate({ ...group, fallback_only: checked })}
							data-testid={`route-group-${groupIndex}-fallback-only`}
						/>
					</div>

					{/* Group targets */}
					<div className="space-y-2">
						<div className="flex items-center justify-between">
							<Label className="text-xs">目标</Label>
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={addGroupTarget}
								className="h-7 gap-1 text-xs"
								data-testid={`route-group-${groupIndex}-target-add`}
							>
								<Plus className="h-3 w-3" />
								添加
							</Button>
						</div>

						{group.targets.map((target, ti) => (
							<GroupTargetRow
								key={ti}
								target={target}
								groupIndex={groupIndex}
								targetIndex={ti}
								availableProviders={availableProviders}
								providersData={providersData}
								showRemove={group.targets.length > 1}
								onUpdate={updateGroupTarget}
								onRemove={removeGroupTarget}
							/>
						))}

						<div
							className={`flex items-center justify-end gap-2 text-xs font-medium ${Math.abs(groupWeight - 1) > 0.001 ? "text-destructive" : "text-muted-foreground"}`}
						>
							总计: {groupWeight.toFixed(4)}
							{Math.abs(groupWeight - 1) > 0.001 && <span className="text-destructive">（总和必须等于 1）</span>}
						</div>
					</div>
				</div>
			)}
		</div>
	);
}

/* ─── Group Target Row (compact) ────────────────────────── */

interface GroupTargetRowProps {
	target: RoutingTargetFormData;
	groupIndex: number;
	targetIndex: number;
	availableProviders: string[];
	providersData: Array<{ name: string; keys: Array<{ id: string; name: string }> }>;
	showRemove: boolean;
	onUpdate: (index: number, patch: Partial<RoutingTargetFormData>) => void;
	onRemove: (index: number) => void;
}

function GroupTargetRow({
	target,
	groupIndex,
	targetIndex,
	availableProviders,
	providersData,
	showRemove,
	onUpdate,
	onRemove,
}: GroupTargetRowProps) {
	const availableKeys = getRouteGroupAvailableKeys(providersData, target.provider);

	return (
		<div className="space-y-2" data-testid={`route-group-${groupIndex}-target-${targetIndex}`}>
			<div className="flex items-center gap-2">
				<div className="flex-1">
					<Select
						value={target.provider}
						onValueChange={(value) => {
							onUpdate(targetIndex, {
								provider: value,
								model: "",
								key_id: "",
							});
						}}
					>
						<SelectTrigger className="h-9 text-sm">
							<SelectValue placeholder="选择 Provider..." />
						</SelectTrigger>
						<SelectContent>
							{availableProviders.map((prov) => (
								<SelectItem key={prov} value={prov}>
									<div className="flex items-center gap-2">
										<RenderProviderIcon provider={prov as ProviderIconType} size="sm" className="h-4 w-4" />
										<span>{getProviderLabel(prov)}</span>
									</div>
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</div>
				<div className="flex-1">
					<ModelMultiselect
						provider={target.provider || undefined}
						value={target.model}
						onChange={(value) => onUpdate(targetIndex, { model: value })}
						placeholder="选择 Model..."
						isSingleSelect
						disabled={!target.provider}
						className="!h-9 !min-h-9"
					/>
				</div>
				<Input
					type="number"
					min={0.001}
					max={1}
					step={0.001}
					value={target.weight}
					onChange={(e) => onUpdate(targetIndex, { weight: parseFloat(e.target.value) || 0 })}
					className="h-9 w-20 text-sm"
					data-testid={`route-group-${groupIndex}-target-${targetIndex}-weight`}
				/>
				{showRemove && (
					<Button
						type="button"
						variant="ghost"
						size="sm"
						onClick={() => onRemove(targetIndex)}
						className="h-9 w-9 shrink-0 p-0"
						aria-label={`删除分组 ${groupIndex + 1} 中的目标 ${targetIndex + 1}`}
					>
						<Trash2 className="h-3.5 w-3.5" />
					</Button>
				)}
			</div>
			{shouldShowRouteGroupKeySelector(target, availableKeys) && (
				<div className="space-y-1.5 pl-0.5">
					<Label id={`route-group-${groupIndex}-target-${targetIndex}-apikey-label`} className="text-[11px]">
						API Key <span className="text-muted-foreground">（可选；留空则使用负载均衡选择）</span>
					</Label>
					<div className="flex gap-1.5">
						<Select value={target.key_id || ""} onValueChange={(value) => onUpdate(targetIndex, { key_id: value })}>
							<SelectTrigger
								id={`route-group-${groupIndex}-target-${targetIndex}-apikey-select`}
								aria-labelledby={`route-group-${groupIndex}-target-${targetIndex}-apikey-label`}
								className="h-9 flex-1 text-sm"
								data-testid={`route-group-${groupIndex}-target-${targetIndex}-apikey-select`}
							>
								<SelectValue placeholder="选择 API Key（可选）" />
							</SelectTrigger>
							<SelectContent>
								{availableKeys.map((key) => (
									<SelectItem key={key.id} value={key.id}>
										{key.name}
									</SelectItem>
								))}
								{target.key_id && !availableKeys.some((key) => key.id === target.key_id) && (
									<SelectItem key={`pinned-${target.key_id}`} value={target.key_id}>
										（已固定）{target.key_id}
									</SelectItem>
								)}
							</SelectContent>
						</Select>
						{target.key_id && (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => onUpdate(targetIndex, { key_id: "" })}
								className="h-9 w-9 p-0"
								aria-label={`清空分组 ${groupIndex + 1} 中目标 ${targetIndex + 1} 的 API Key`}
								data-testid={`route-group-${groupIndex}-target-${targetIndex}-apikey-clear`}
							>
								<X className="h-3.5 w-3.5" />
							</Button>
						)}
					</div>
				</div>
			)}
		</div>
	);
}
