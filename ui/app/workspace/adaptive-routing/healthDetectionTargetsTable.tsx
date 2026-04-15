"use client";

import Link from "next/link";
import { AlertTriangle, Loader2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useToast } from "@/hooks/use-toast";
import { getErrorMessage } from "@/lib/store/apis/baseApi";
import { useUpdateHealthDetectionTargetMutation } from "@/lib/store/apis/routingRulesApi";
import type {
	HealthDetectionMode,
	HealthDetectionProbeState,
	HealthDetectionSupportStatus,
	HealthDetectionTarget,
} from "@/lib/types/routingRules";
import { cn } from "@/lib/utils";

import {
	formatHealthDetectionTimestamp,
	getHealthDetectionProbeStateDescription,
	getHealthDetectionProbeStateLabel,
	getHealthDetectionSupportStatusLabel,
	isHealthDetectionTargetEditable,
} from "./healthDetectionTargets";

interface HealthDetectionTargetsTableProps {
	mode: HealthDetectionMode;
	targets?: HealthDetectionTarget[];
	error?: unknown;
	isLoading: boolean;
	isFetching: boolean;
	onRetry: () => void;
}

function getSupportBadgeClass(status: HealthDetectionSupportStatus): string {
	if (status === "supported") {
		return "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400";
	}
	return "bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300";
}

function getProbeStateBadgeClass(state: HealthDetectionProbeState): string {
	switch (state) {
		case "eligible":
			return "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400";
		case "pending_first_probe":
			return "bg-blue-100 text-blue-700 dark:bg-blue-950/40 dark:text-blue-300";
		case "paused_idle":
			return "bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300";
		case "unsupported":
			return "bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300";
		case "off":
		default:
			return "bg-muted text-muted-foreground";
	}
}

export default function HealthDetectionTargetsTable({
	mode,
	targets,
	error,
	isLoading,
	isFetching,
	onRetry,
}: HealthDetectionTargetsTableProps) {
	const { toast } = useToast();
	const [updateTarget] = useUpdateHealthDetectionTargetMutation();
	const rows = targets ?? [];

	return (
		<Card data-testid="adaptive-routing-targets-table">
			<CardHeader className="border-b">
				<div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
					<div className="space-y-1">
						<CardTitle>Unified Detection Targets</CardTitle>
						<CardDescription>
							Each grouped routing target appears once here. Detection switches are shared across every rule that references the same
							target.
						</CardDescription>
					</div>
					<div className="flex items-center gap-2">
						<Badge variant="outline" className="text-xs">
							{rows.length} targets
						</Badge>
						{isFetching && !isLoading ? (
							<span className="text-muted-foreground flex items-center gap-1 text-xs">
								<Loader2 className="h-3.5 w-3.5 animate-spin" />
								Refreshing
							</span>
						) : null}
					</div>
				</div>
			</CardHeader>
			<CardContent className="p-0">
				{isLoading && rows.length === 0 ? (
					<div className="text-muted-foreground flex items-center gap-2 px-6 py-8 text-sm">
						<Loader2 className="h-4 w-4 animate-spin" />
						Loading unified health detection targets…
					</div>
				) : error ? (
					<div className="px-6 py-6">
						<div className="border-destructive/30 bg-destructive/5 rounded-sm border p-4 text-sm">
							<p className="font-medium">Unable to load health detection targets.</p>
							<p className="text-muted-foreground mt-1">{getErrorMessage(error)}</p>
							<Button variant="outline" size="sm" onClick={onRetry} className="mt-3">
								Retry
							</Button>
						</div>
					</div>
				) : rows.length === 0 ? (
					<div className="px-6 py-12 text-center">
						<AlertTriangle className="text-muted-foreground/50 mx-auto mb-3 h-10 w-10" />
						<p className="text-sm font-medium">No eligible targets found.</p>
						<p className="text-muted-foreground mt-1 text-xs">Add grouped health routing targets to manage detection here.</p>
						<Button asChild variant="outline" className="mt-4">
							<Link href="/workspace/routing-rules">Open Routing Rules</Link>
						</Button>
					</div>
				) : (
					<div className="overflow-x-auto">
						<Table className="min-w-[1180px]">
							<TableHeader>
								<TableRow>
									<TableHead>Provider</TableHead>
									<TableHead>Model</TableHead>
									<TableHead>Key ID</TableHead>
									<TableHead>Referenced By</TableHead>
									<TableHead>Support Status</TableHead>
									<TableHead className="w-36">Detection Enabled</TableHead>
									<TableHead>Probe State</TableHead>
									<TableHead>Rule Health Summary</TableHead>
									<TableHead>Last Real Access</TableHead>
									<TableHead>Last Probe</TableHead>
									<TableHead>Last Probe Result</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{rows.map((target) => {
									const editable = isHealthDetectionTargetEditable(target);
									const probeResultLabel = target.last_probe_result ? target.last_probe_result.toUpperCase() : "—";

									return (
										<TableRow key={target.target_id}>
											<TableCell className="font-medium">{target.provider}</TableCell>
											<TableCell className="font-mono text-sm">{target.model}</TableCell>
											<TableCell className="font-mono text-sm">{target.key_id || "—"}</TableCell>
											<TableCell>
												<div className="flex flex-wrap gap-1">
													{target.referenced_rule_names.map((ruleName) => (
														<Badge key={`${target.target_id}-${ruleName}`} variant="outline" className="text-xs">
															{ruleName}
														</Badge>
													))}
												</div>
											</TableCell>
											<TableCell>
												<div className="space-y-1">
													<Badge className={cn("w-fit", getSupportBadgeClass(target.support_status))}>
														{getHealthDetectionSupportStatusLabel(target.support_status)}
													</Badge>
													{target.support_reason ? <p className="text-muted-foreground max-w-56 text-xs">{target.support_reason}</p> : null}
												</div>
											</TableCell>
											<TableCell>
												<div className="space-y-2">
													<Switch
														checked={target.detection_enabled}
														size="md"
														disabled={!editable}
														data-testid={`adaptive-routing-target-toggle-${target.target_id}`}
														onAsyncCheckedChange={async (checked) => {
															await updateTarget({
																targetId: target.target_id,
																data: {
																	detection_enabled: checked,
																},
															})
																.unwrap()
																.catch((mutationError) => {
																	toast({
																		title: "Failed to update health detection target",
																		description: getErrorMessage(mutationError),
																		variant: "destructive",
																	});
																});
														}}
													/>
													<p className="text-muted-foreground text-xs">
														{editable ? (target.detection_enabled ? "Enabled" : "Disabled") : target.support_reason || "Read-only"}
													</p>
												</div>
											</TableCell>
											<TableCell>
												<div className="space-y-1">
													<Badge className={cn("w-fit", getProbeStateBadgeClass(target.probe_state))}>
														{getHealthDetectionProbeStateLabel(target.probe_state)}
													</Badge>
													<p className="text-muted-foreground max-w-64 text-xs">
														{getHealthDetectionProbeStateDescription(target.probe_state)}
													</p>
													{mode === "passive" && target.probe_state !== "unsupported" ? (
														<p className="text-muted-foreground text-xs">
															Background probing is disabled globally until Hybrid mode is turned back on.
														</p>
													) : null}
												</div>
											</TableCell>
											<TableCell>
												<span
													className={cn("text-sm", target.rule_health_summary.cooldown_rule_count > 0 && "text-destructive font-medium")}
												>
													{target.rule_health_summary.cooldown_rule_count}/{target.rule_health_summary.total_rule_count} rules in cooldown
												</span>
											</TableCell>
											<TableCell className="text-muted-foreground text-sm">
												{formatHealthDetectionTimestamp(target.last_real_access_at)}
											</TableCell>
											<TableCell className="text-muted-foreground text-sm">
												{formatHealthDetectionTimestamp(target.last_probe_at)}
											</TableCell>
											<TableCell>
												<div className="space-y-1">
													{target.last_probe_result ? (
														<Badge
															className={cn(
																"w-fit",
																target.last_probe_result === "success"
																	? "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
																	: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
															)}
														>
															{probeResultLabel}
														</Badge>
													) : (
														<span className="text-muted-foreground text-sm">—</span>
													)}
													{target.last_probe_error ? (
														<p className="text-muted-foreground max-w-56 truncate text-xs" title={target.last_probe_error}>
															{target.last_probe_error}
														</p>
													) : null}
												</div>
											</TableCell>
										</TableRow>
									);
								})}
							</TableBody>
						</Table>
					</div>
				)}
			</CardContent>
		</Card>
	);
}
