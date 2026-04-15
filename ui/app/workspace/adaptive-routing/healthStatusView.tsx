"use client";

import Link from "next/link";
import { Activity, RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
	useGetHealthDetectionConfigQuery,
	useGetHealthDetectionTargetsQuery,
	useGetHealthStatusQuery,
} from "@/lib/store/apis/routingRulesApi";
import type { RuleHealthStatus } from "@/lib/types/routingRules";

import { getDetectionModeLabel } from "./healthDetectionConfig";
import HealthDetectionSettingsCard from "./healthDetectionSettingsCard";
import HealthDetectionTargetsTable from "./healthDetectionTargetsTable";

export default function HealthStatusView() {
	const {
		data: healthData,
		isLoading: isHealthLoading,
		isFetching: isHealthFetching,
		refetch: refetchHealth,
	} = useGetHealthStatusQuery(undefined, {
		pollingInterval: 10_000,
	});
	const {
		data: configData,
		error: configError,
		isLoading: isConfigLoading,
		isFetching: isConfigFetching,
		refetch: refetchConfig,
	} = useGetHealthDetectionConfigQuery();
	const {
		data: targetsData,
		error: targetsError,
		isLoading: isTargetsLoading,
		isFetching: isTargetsFetching,
		refetch: refetchTargets,
	} = useGetHealthDetectionTargetsQuery(undefined, {
		pollingInterval: 10_000,
	});

	const rules: RuleHealthStatus[] = healthData?.rules ?? [];
	const targets = targetsData?.targets ?? [];
	const enabledTargetCount = targets.filter((target) => target.detection_enabled).length;
	const cooldownTargetCount = targets.filter((target) => target.rule_health_summary.cooldown_rule_count > 0).length;
	const detectionModeLabel = configData ? getDetectionModeLabel(configData.mode) : "Unavailable";
	const isRefreshing = isHealthFetching || isConfigFetching || isTargetsFetching;

	const handleRefresh = () => {
		void Promise.all([refetchHealth(), refetchConfig(), refetchTargets()]);
	};

	return (
		<div className="flex flex-col gap-6 p-6">
			<div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
				<div>
					<h2 className="text-2xl font-bold tracking-tight">Adaptive Routing</h2>
					<p className="text-muted-foreground mt-1 text-sm">
						Unified health detection controls for grouped routing targets, plus the rule-level health table that still decides routing.
					</p>
					<p className="text-muted-foreground mt-2 text-xs">
						Detailed per-request routing decisions remain in Logs under Routing Decision Logs.
					</p>
				</div>
				<Button variant="outline" size="sm" onClick={handleRefresh} disabled={isRefreshing} className="gap-2">
					<RefreshCw className={`h-4 w-4 ${isRefreshing ? "animate-spin" : ""}`} />
					Refresh
				</Button>
			</div>

			<HealthDetectionSettingsCard
				config={configData}
				error={configError}
				isLoading={isConfigLoading}
				isFetching={isConfigFetching}
				onRetry={handleRefresh}
			/>

			<div className="grid gap-4 md:grid-cols-3">
				<div className="rounded-lg border p-4">
					<div className="text-muted-foreground flex items-center gap-2 text-sm">
						<Activity className="h-4 w-4" />
						Rules with Health Routing
					</div>
					<p className="mt-1 text-2xl font-semibold">{rules.length}</p>
				</div>
				<div className="rounded-lg border p-4">
					<div className="text-muted-foreground flex items-center gap-2 text-sm">
						<ShieldCheck className="h-4 w-4" />
						Targets in Unified List
					</div>
					<p className="mt-1 text-2xl font-semibold">{targets.length}</p>
				</div>
				<div className="rounded-lg border p-4">
					<div className="text-muted-foreground flex items-center gap-2 text-sm">
						<ShieldAlert className="h-4 w-4" />
						Detection Enabled
					</div>
					<p className="mt-1 text-2xl font-semibold">{enabledTargetCount}</p>
					<p className="text-muted-foreground mt-1 text-xs">{cooldownTargetCount} referenced by cooldown rules</p>
				</div>
			</div>

			<div className="grid gap-3 lg:grid-cols-2">
				<div className="bg-muted/20 rounded-md border p-4">
					<p className="text-sm font-medium">Probe State is target-level activity, not rule health.</p>
					<p className="text-muted-foreground mt-1 text-xs">
						Use the table below to manage per-target probing, but use the rule table further down as the routing authority.
					</p>
				</div>
				<div className="bg-muted/20 rounded-md border p-4">
					<p className="text-sm font-medium">Runtime activity reflects the current gateway node only.</p>
					<p className="text-muted-foreground mt-1 text-xs">
						Recent probes and recent real access are local runtime signals, not a cluster-wide view.
					</p>
				</div>
				{configData?.mode === "passive" ? (
					<div className="rounded-md border border-amber-200 bg-amber-50 p-4 lg:col-span-2 dark:border-amber-900/60 dark:bg-amber-950/20">
						<p className="text-sm font-medium text-amber-900 dark:text-amber-200">
							Background probing is disabled globally. Only real request outcomes update rule health.
						</p>
					</div>
				) : null}
			</div>

			<HealthDetectionTargetsTable
				mode={configData?.mode ?? "passive"}
				targets={targetsData?.targets}
				error={targetsError}
				isLoading={isTargetsLoading}
				isFetching={isTargetsFetching}
				onRetry={handleRefresh}
			/>

			<div className="space-y-4">
				<div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
					<div>
						<h3 className="text-lg font-semibold">Rule Health by Routing Rule</h3>
						<p className="text-muted-foreground text-sm">
							Rule-specific cooldown, failure counts, and last failures remain the final routing health view.
						</p>
					</div>
					<Badge variant="outline" className="w-fit text-xs">
						Detection Mode: {detectionModeLabel}
					</Badge>
				</div>

				{isHealthLoading ? (
					<p className="text-muted-foreground py-8 text-center text-sm">Loading health data…</p>
				) : rules.length === 0 ? (
					<div className="rounded-lg border border-dashed py-12 text-center">
						<Activity className="text-muted-foreground/50 mx-auto mb-3 h-10 w-10" />
						<p className="text-muted-foreground text-sm">No grouped health routing rules found</p>
						<p className="text-muted-foreground mt-1 text-xs">Enable grouped health routing on a routing rule to see rule health here.</p>
						<Button asChild variant="outline" className="mt-4">
							<Link href="/workspace/routing-rules">Open Routing Rules</Link>
						</Button>
					</div>
				) : (
					rules.map((rule) => (
						<div key={rule.rule_id} className="space-y-3">
							<div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
								<div>
									<h4 className="text-lg font-semibold">{rule.rule_name}</h4>
									<p className="text-muted-foreground text-xs">
										Policy: threshold={rule.policy.failure_threshold} window={rule.policy.failure_window_seconds}s cooldown=
										{rule.policy.cooldown_seconds}s consecutive=
										{rule.policy.consecutive_failures || rule.policy.failure_threshold}
									</p>
								</div>
								<Badge variant="outline" className="w-fit text-xs">
									{rule.targets.filter((target) => target.status === "available").length}/{rule.targets.length} available
								</Badge>
							</div>
							<div className="rounded-md border">
								<Table>
									<TableHeader>
										<TableRow>
											<TableHead>Target</TableHead>
											<TableHead className="w-28">Status</TableHead>
											<TableHead className="w-28">Source</TableHead>
											<TableHead className="w-28">Window Fail</TableHead>
											<TableHead className="w-32">Consecutive</TableHead>
											<TableHead>Last Observed</TableHead>
											<TableHead>Cooldown Until</TableHead>
											<TableHead>Last Failure</TableHead>
										</TableRow>
									</TableHeader>
									<TableBody>
										{rule.targets.map((target) => (
											<TableRow key={target.key}>
												<TableCell className="font-mono text-sm font-medium">{target.key}</TableCell>
												<TableCell>
													{target.status === "cooldown" ? (
														<Badge variant="destructive" className="gap-1">
															<ShieldAlert className="h-3 w-3" />
															Cooldown
														</Badge>
													) : (
														<Badge
															variant="secondary"
															className="gap-1 bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
														>
															<ShieldCheck className="h-3 w-3" />
															Available
														</Badge>
													)}
												</TableCell>
												<TableCell>
													{target.last_observation_source ? (
														<Badge variant="outline" className="text-xs uppercase">
															{target.last_observation_source}
														</Badge>
													) : (
														"—"
													)}
												</TableCell>
												<TableCell>{target.failure_count}</TableCell>
												<TableCell>{target.consecutive_failures}</TableCell>
												<TableCell className="text-muted-foreground text-sm">
													{target.last_observed_at ? new Date(target.last_observed_at).toLocaleTimeString() : "—"}
												</TableCell>
												<TableCell className="text-muted-foreground text-sm">
													{target.cooldown_until ? new Date(target.cooldown_until).toLocaleTimeString() : "—"}
												</TableCell>
												<TableCell className="text-muted-foreground max-w-64 truncate text-sm" title={target.last_failure_msg}>
													{target.last_failure_msg || "—"}
												</TableCell>
											</TableRow>
										))}
									</TableBody>
								</Table>
							</div>
						</div>
					))
				)}
			</div>
		</div>
	);
}
