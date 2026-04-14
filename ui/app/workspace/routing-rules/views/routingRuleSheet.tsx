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
		loading: () => <div className="text-sm text-gray-500">Loading CEL builder...</div>,
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
			setValue("health_policy", editingRule.health_policy || { ...DEFAULT_HEALTH_POLICY });
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
			toast.error(`${data.scope === "team" ? "Team" : data.scope === "customer" ? "Customer" : "Virtual Key"} is required`);
			return;
		}

		if (data.grouped_routing_enabled) {
			// Grouped routing validation
			if (routeGroups.length === 0) {
				toast.error("At least one route group is required");
				return;
			}
			for (const group of routeGroups) {
				if (!group.name.trim()) {
					toast.error("Each route group must have a name");
					return;
				}
				if (group.targets.length === 0) {
					toast.error(`Route group "${group.name}" must have at least one target`);
					return;
				}
				const groupWeight = group.targets.reduce((sum, t) => sum + (t.weight || 0), 0);
				if (Math.abs(groupWeight - 1) > 0.001) {
					toast.error(`Weights in group "${group.name}" must sum to 1 (current: ${groupWeight.toFixed(4)})`);
					return;
				}
				for (const t of group.targets) {
					if (!t.provider) {
						toast.error(`Each target in group "${group.name}" must have a provider`);
						return;
					}
					if (!t.model) {
						toast.error(`Each target in group "${group.name}" must have a model`);
						return;
					}
				}
			}
			if (data.health_policy) {
				if (data.health_policy.failure_threshold < 1) {
					toast.error("Failure threshold must be at least 1");
					return;
				}
				if (data.health_policy.failure_window_seconds < 1) {
					toast.error("Failure window must be at least 1 second");
					return;
				}
				if (data.health_policy.cooldown_seconds < 1) {
					toast.error("Cooldown must be at least 1 second");
					return;
				}
			}
		} else {
			// Standard routing validation
			if (targets.length === 0) {
				toast.error("At least one routing target is required");
				return;
			}
			for (const t of targets) {
				if (t.weight <= 0) {
					toast.error("Each target weight must be greater than 0");
					return;
				}
			}
			if (Math.abs(totalWeight - 1) > 0.001) {
				toast.error(`Target weights must sum to 1, current total: ${totalWeight.toFixed(4)}`);
				return;
			}
		}

		// Validate regex patterns in routing rules
		const regexErrors = validateRoutingRules(query);
		if (regexErrors.length > 0) {
			toast.error(`Invalid regex pattern:\n${regexErrors.join("\n")}`);
			return;
		}

		// Validate rate limit and budget rules
		const rateLimitErrors = validateRateLimitAndBudgetRules(query);
		if (rateLimitErrors.length > 0) {
			toast.error(`Invalid rule configuration:\n${rateLimitErrors.join("\n")}`);
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
				toast.success(isEditing ? "Routing rule updated successfully" : "Routing rule created successfully");
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
					<SheetTitle>{isEditing ? "Edit Routing Rule" : "Create New Routing Rule"}</SheetTitle>
					<SheetDescription>
						{isEditing ? "Update the routing rule configuration" : "Create a new CEL-based routing rule for intelligent request routing"}
					</SheetDescription>
				</SheetHeader>

				<form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
					{/* Rule Name */}
					<div className="space-y-3">
						<Label htmlFor="name">
							Rule Name <span className="text-red-500">*</span>
						</Label>
						<Input
							id="name"
							placeholder="e.g., Route GPT-4 to Azure"
							{...register("name", { required: "Rule name is required", maxLength: 255 })}
						/>
						{errors.name && <p className="text-destructive text-sm">{errors.name.message}</p>}
					</div>

					{/* Description */}
					<div className="space-y-3">
						<Label htmlFor="description">Description</Label>
						<Textarea id="description" placeholder="Describe what this rule does..." rows={2} {...register("description")} />
					</div>

					{/* Enabled Switch */}
					<div className="flex items-center justify-between rounded-lg border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="enabled">Enable Rule</Label>
							<p className="text-muted-foreground text-sm">Rule will be active and applied to matching requests</p>
						</div>
						<Switch id="enabled" checked={enabled} onCheckedChange={(checked) => setValue("enabled", checked)} />
					</div>

					{/* Scope and Priority - Side by Side */}
					<div className="grid grid-cols-2 gap-4">
						<div className="space-y-3">
							<Label htmlFor="scope">Scope</Label>
							<Select
								value={scope}
								onValueChange={(value) => {
									setValue("scope", value as any);
									// Clear scope_id when scope changes
									setValue("scope_id", "");
								}}
							>
								<SelectTrigger className="w-full">
									<SelectValue placeholder="Select scope..." />
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
								Priority <span className="text-red-500">*</span>
							</Label>
							<Input
								id="priority"
								type="number"
								min={0}
								max={1000}
								{...register("priority", {
									required: "Priority is required",
									min: { value: 0, message: "Priority must be ≥ 0" },
									max: { value: 1000, message: "Priority must be ≤ 1000" },
									valueAsNumber: true,
								})}
							/>
							<p className="text-muted-foreground text-xs">Lower numbers = higher priority (0 is highest)</p>
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
										<SelectValue placeholder="Select a team..." />
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
										<SelectValue placeholder="Select a customer..." />
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
										<SelectValue placeholder="Select a virtual key..." />
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
									No {scope === "team" ? "teams" : scope === "customer" ? "customers" : "virtual keys"} available
								</p>
							)}
							{errors.scope_id && <p className="text-destructive text-sm">{errors.scope_id.message}</p>}
						</div>
					)}

					<Separator />

					{/* CEL Rule Builder */}
					<div className="space-y-3">
						<Label>Rule Builder</Label>
						<p className="text-muted-foreground text-sm">
							Build conditions to determine when this rule should apply. Leave empty to apply this rule to all requests.
						</p>
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
						Note: Ensure token limits, request limits, and budget are configured in{" "}
						<strong>Model Providers → Configurations → {"{provider}"} → Governance</strong> (provider-level) or{" "}
						<strong>Model Providers → Budgets & Limits</strong> section (model-level) before using them in routing rules.
					</p>

					<Separator />

					{/* Grouped Health Routing Toggle */}
					<div className="flex items-center justify-between rounded-lg border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="grouped_routing_enabled">Grouped Health Routing</Label>
							<p className="text-muted-foreground text-sm">Enable health-aware routing with route groups and automatic failover</p>
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
								<Label>Health Policy</Label>
								<p className="text-muted-foreground text-xs">
									Configure when a target is considered unhealthy and placed in cooldown. Two triggers (either one activates cooldown):
									window-based (burst failures) and consecutive (persistent failures regardless of time).
								</p>
								<div className="grid grid-cols-2 gap-3">
									<div className="space-y-1.5">
										<Label htmlFor="hp-threshold" className="text-xs">
											Window Threshold
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
										<p className="text-muted-foreground text-[10px]">Failures within window to trigger cooldown</p>
									</div>
									<div className="space-y-1.5">
										<Label htmlFor="hp-window" className="text-xs">
											Failure Window (s)
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
										<p className="text-muted-foreground text-[10px]">Sliding time window for counting failures</p>
									</div>
									<div className="space-y-1.5">
										<Label htmlFor="hp-consecutive" className="text-xs">
											Consecutive Failures
										</Label>
										<Input
											id="hp-consecutive"
											type="number"
											min={1}
											value={
												healthPolicy?.consecutive_failures ??
												DEFAULT_HEALTH_POLICY.consecutive_failures ??
												healthPolicy?.failure_threshold ??
												2
											}
											onChange={(e) =>
												setValue("health_policy", {
													...healthPolicy,
													consecutive_failures: parseInt(e.target.value) || 1,
												})
											}
											data-testid="health-policy-consecutive"
										/>
										<p className="text-muted-foreground text-[10px]">Consecutive failures (any pace) to trigger cooldown</p>
									</div>
									<div className="space-y-1.5">
										<Label htmlFor="hp-cooldown" className="text-xs">
											Cooldown (s)
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
							</div>

							<Separator />

							{/* Route Groups */}
							<div className="space-y-3">
								<div className="flex items-center justify-between">
									<div>
										<Label>Route Groups</Label>
										<p className="text-muted-foreground mt-0.5 text-xs">
											Groups are tried in order. Each group selects targets by weight, with retries within the group.
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
													name: `Group ${prev.length + 1}`,
													targets: [{ provider: "", model: "", key_id: "", weight: 1 }],
												},
											])
										}
										className="shrink-0 gap-2"
										data-testid="route-group-add"
									>
										<Plus className="h-4 w-4" />
										Add Group
									</Button>
								</div>

								{routeGroups.length === 0 && (
									<p className="text-muted-foreground rounded-lg border border-dashed py-4 text-center text-sm">
										No route groups configured. Add a group to get started.
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
										<Label>Routing Targets</Label>
										<p className="text-muted-foreground mt-0.5 text-xs">
											Weights must sum to 1. Leave provider or model empty to use the incoming request value.
										</p>
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
										Add Target
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
									Total weight: {totalWeight.toFixed(4)}
									{Math.abs(totalWeight - 1) > 0.001 && <span className="text-destructive">(must equal 1)</span>}
								</div>
							</div>

							{/* Fallbacks */}
							<div className="space-y-3">
								<div className="flex items-center justify-between">
									<Label>Fallbacks</Label>
									<Button
										type="button"
										variant="outline"
										size="sm"
										onClick={() => setValue("fallbacks", [...(fallbacks || []), ""])}
										className="gap-2"
									>
										<Plus className="h-4 w-4" />
										Add Fallback
									</Button>
								</div>
								<div className="space-y-2">
									{(fallbacks || []).length === 0 ? (
										<p className="text-muted-foreground text-sm">No fallbacks configured</p>
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
																<SelectValue placeholder="Select provider..." />
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
															placeholder="Select model..."
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
														aria-label={`Remove fallback ${index + 1}`}
													>
														<Trash2 className="h-4 w-4" />
													</Button>
												</div>
											);
										})
									)}
								</div>
								<p className="text-muted-foreground text-xs">Fallbacks will be used in the order they are defined</p>
							</div>
						</>
					)}

					{/* Action Buttons */}
					<div className="flex justify-end gap-3">
						<Button type="button" variant="outline" onClick={handleCancel} disabled={isLoading}>
							<X className="h-4 w-4" />
							Cancel
						</Button>
						<Button type="submit" disabled={isLoading}>
							<Save className="h-4 w-4" />
							{isEditing ? "Update Rule" : "Save Rule"}
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
				<span className="text-muted-foreground text-sm font-medium">Target {index + 1}</span>
				<div className="flex items-center gap-2">
					<div className="flex items-center gap-1.5">
						<Label htmlFor={`routing-target-${index}-weight-input`} className="text-muted-foreground shrink-0 text-xs">
							Weight
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
							aria-label={`Remove target ${index + 1}`}
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
								<SelectValue placeholder="Incoming (optional)" />
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
								aria-label={`Clear provider for target ${index + 1}`}
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
								placeholder="Incoming (optional)"
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
								aria-label={`Clear model for target ${index + 1}`}
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
						API Key <span className="text-muted-foreground">(optional — leave unset for load-balanced selection)</span>
					</Label>
					<div className="flex gap-1.5">
						<Select value={target.key_id || ""} onValueChange={(value) => onUpdate(index, "key_id", value)}>
							<SelectTrigger
								id={`routing-target-${index}-apikey-select`}
								aria-labelledby={`routing-target-${index}-apikey-label`}
								className="h-9 flex-1 text-sm"
								data-testid={`routing-target-${index}-apikey-select`}
							>
								<SelectValue placeholder="Select key (optional)" />
							</SelectTrigger>
							<SelectContent>
								{availableKeys.map((key) => (
									<SelectItem key={key.id} value={key.id}>
										{key.name}
									</SelectItem>
								))}
								{target.key_id && !availableKeys.some((k) => k.id === target.key_id) && (
									<SelectItem key={`pinned-${target.key_id}`} value={target.key_id}>
										(pinned) {target.key_id}
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
								aria-label={`Clear API key for target ${index + 1}`}
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
					<span>{group.name || `Group ${groupIndex + 1}`}</span>
					<span className="text-muted-foreground font-normal">
						({group.targets.length} target{group.targets.length !== 1 ? "s" : ""})
					</span>
				</button>
				<div className="flex items-center gap-1">
					{onMoveUp && (
						<Button type="button" variant="ghost" size="sm" onClick={onMoveUp} className="h-7 w-7 p-0" aria-label="Move group up">
							<ChevronUp className="h-3.5 w-3.5" />
						</Button>
					)}
					{onMoveDown && (
						<Button type="button" variant="ghost" size="sm" onClick={onMoveDown} className="h-7 w-7 p-0" aria-label="Move group down">
							<ChevronDown className="h-3.5 w-3.5" />
						</Button>
					)}
					<Button
						type="button"
						variant="ghost"
						size="sm"
						onClick={onRemove}
						className="text-destructive hover:text-destructive h-7 w-7 p-0"
						aria-label="Remove group"
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
							<Label className="text-xs">Group Name</Label>
							<Input
								value={group.name}
								onChange={(e) => onUpdate({ ...group, name: e.target.value })}
								placeholder="e.g., Primary, Fallback"
								className="h-9 text-sm"
								data-testid={`route-group-${groupIndex}-name`}
							/>
						</div>
						<div className="space-y-1.5">
							<Label className="text-xs">Retry Limit</Label>
							<Input
								type="number"
								min={0}
								max={10}
								value={group.retry_limit}
								onChange={(e) => onUpdate({ ...group, retry_limit: parseInt(e.target.value) || 0 })}
								className="h-9 text-sm"
								data-testid={`route-group-${groupIndex}-retry`}
							/>
							<p className="text-muted-foreground text-[10px]">Extra attempts within this group (0 = no retries)</p>
						</div>
					</div>

					{/* Group targets */}
					<div className="space-y-2">
						<div className="flex items-center justify-between">
							<Label className="text-xs">Targets</Label>
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={addGroupTarget}
								className="h-7 gap-1 text-xs"
								data-testid={`route-group-${groupIndex}-target-add`}
							>
								<Plus className="h-3 w-3" />
								Add
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
							Total: {groupWeight.toFixed(4)}
							{Math.abs(groupWeight - 1) > 0.001 && <span className="text-destructive">(must equal 1)</span>}
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
							<SelectValue placeholder="Provider..." />
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
						placeholder="Model..."
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
						aria-label={`Remove target ${targetIndex + 1} from group ${groupIndex + 1}`}
					>
						<Trash2 className="h-3.5 w-3.5" />
					</Button>
				)}
			</div>
			{shouldShowRouteGroupKeySelector(target, availableKeys) && (
				<div className="space-y-1.5 pl-0.5">
					<Label id={`route-group-${groupIndex}-target-${targetIndex}-apikey-label`} className="text-[11px]">
						API Key <span className="text-muted-foreground">(optional — leave unset for load-balanced selection)</span>
					</Label>
					<div className="flex gap-1.5">
						<Select value={target.key_id || ""} onValueChange={(value) => onUpdate(targetIndex, { key_id: value })}>
							<SelectTrigger
								id={`route-group-${groupIndex}-target-${targetIndex}-apikey-select`}
								aria-labelledby={`route-group-${groupIndex}-target-${targetIndex}-apikey-label`}
								className="h-9 flex-1 text-sm"
								data-testid={`route-group-${groupIndex}-target-${targetIndex}-apikey-select`}
							>
								<SelectValue placeholder="Select key (optional)" />
							</SelectTrigger>
							<SelectContent>
								{availableKeys.map((key) => (
									<SelectItem key={key.id} value={key.id}>
										{key.name}
									</SelectItem>
								))}
								{target.key_id && !availableKeys.some((key) => key.id === target.key_id) && (
									<SelectItem key={`pinned-${target.key_id}`} value={target.key_id}>
										(pinned) {target.key_id}
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
								aria-label={`Clear API key for target ${targetIndex + 1} from group ${groupIndex + 1}`}
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
