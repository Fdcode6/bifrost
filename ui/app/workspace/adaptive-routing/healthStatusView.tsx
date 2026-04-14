"use client";

import { useGetHealthStatusQuery } from "@/lib/store/apis/routingRulesApi";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { RefreshCw, Activity, ShieldAlert, ShieldCheck } from "lucide-react";
import type { RuleHealthStatus, HealthSnapshot } from "@/lib/types/routingRules";

export default function HealthStatusView() {
	const { data, isLoading, isFetching, refetch } = useGetHealthStatusQuery(
		undefined,
		{ pollingInterval: 10_000 },
	);

	const rules: RuleHealthStatus[] = data?.rules ?? [];
	const allTargets = rules.flatMap((r) => r.targets);
	const cooldownCount = allTargets.filter((t) => t.status === "cooldown").length;

	return (
		<div className="flex flex-col gap-6 p-6">
			{/* Header */}
			<div className="flex items-center justify-between">
				<div>
					<h2 className="text-2xl font-bold tracking-tight">Health Status</h2>
					<p className="text-muted-foreground text-sm mt-1">
						Real-time health overview of routing targets tracked by grouped health routing rules.
						Status is evaluated using each rule&apos;s actual health policy.
					</p>
					<p className="text-muted-foreground text-xs mt-2">
						Detailed per-request routing decisions remain in Logs under Routing Decision Logs.
					</p>
				</div>
				<Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching} className="gap-2">
					<RefreshCw className={`h-4 w-4 ${isFetching ? "animate-spin" : ""}`} />
					Refresh
				</Button>
			</div>

			{/* Summary cards */}
			<div className="grid grid-cols-3 gap-4">
				<div className="rounded-lg border p-4">
					<div className="flex items-center gap-2 text-muted-foreground text-sm">
						<Activity className="h-4 w-4" />
						Rules with Health Routing
					</div>
					<p className="text-2xl font-semibold mt-1">{rules.length}</p>
				</div>
				<div className="rounded-lg border p-4">
					<div className="flex items-center gap-2 text-sm text-green-600">
						<ShieldCheck className="h-4 w-4" />
						Available Targets
					</div>
					<p className="text-2xl font-semibold mt-1">{allTargets.length - cooldownCount}</p>
				</div>
				<div className="rounded-lg border p-4">
					<div className="flex items-center gap-2 text-sm text-destructive">
						<ShieldAlert className="h-4 w-4" />
						In Cooldown
					</div>
					<p className="text-2xl font-semibold mt-1">{cooldownCount}</p>
				</div>
			</div>

			{/* Per-rule target tables */}
			{isLoading ? (
				<p className="text-muted-foreground text-sm py-8 text-center">Loading health data…</p>
			) : rules.length === 0 ? (
				<div className="text-center py-12 border border-dashed rounded-lg">
					<Activity className="h-10 w-10 mx-auto text-muted-foreground/50 mb-3" />
					<p className="text-muted-foreground text-sm">No grouped health routing rules found</p>
					<p className="text-muted-foreground text-xs mt-1">
						Enable grouped health routing on a routing rule to see health data here
					</p>
				</div>
			) : (
				rules.map((rule) => (
					<div key={rule.rule_id} className="space-y-3">
						<div className="flex items-center justify-between">
							<div>
								<h3 className="text-lg font-semibold">{rule.rule_name}</h3>
								<p className="text-muted-foreground text-xs">
									Policy: threshold={rule.policy.failure_threshold} window={rule.policy.failure_window_seconds}s
									cooldown={rule.policy.cooldown_seconds}s consecutive={rule.policy.consecutive_failures || rule.policy.failure_threshold}
								</p>
							</div>
							<Badge variant="outline" className="text-xs">
								{rule.targets.filter((t) => t.status === "available").length}/{rule.targets.length} available
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
									{rule.targets.map((t) => (
										<TableRow key={t.key}>
											<TableCell className="font-medium font-mono text-sm">{t.key}</TableCell>
												<TableCell>
													{t.status === "cooldown" ? (
														<Badge variant="destructive" className="gap-1">
															<ShieldAlert className="h-3 w-3" />
															Cooldown
													</Badge>
												) : (
													<Badge variant="secondary" className="gap-1 bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400">
														<ShieldCheck className="h-3 w-3" />
														Available
														</Badge>
													)}
												</TableCell>
												<TableCell>
													{t.last_observation_source ? (
														<Badge variant="outline" className="text-xs uppercase">
															{t.last_observation_source}
														</Badge>
													) : (
														"—"
													)}
												</TableCell>
												<TableCell>{t.failure_count}</TableCell>
												<TableCell>{t.consecutive_failures}</TableCell>
												<TableCell className="text-sm text-muted-foreground">
													{t.last_observed_at ? new Date(t.last_observed_at).toLocaleTimeString() : "—"}
												</TableCell>
												<TableCell className="text-sm text-muted-foreground">
													{t.cooldown_until ? new Date(t.cooldown_until).toLocaleTimeString() : "—"}
												</TableCell>
												<TableCell className="text-sm text-muted-foreground max-w-64 truncate" title={t.last_failure_msg}>
												{t.last_failure_msg || "—"}
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
	);
}
