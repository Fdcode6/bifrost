"use client";

import { LOGS_FINAL_DISTRIBUTION_ITEM_COUNT, LOGS_FINAL_DISTRIBUTION_LIST_CLASS } from "@/app/workspace/logs/layoutConfig";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

import type { FinalSuccessDistributionDimension, FinalSuccessDistributionResponse } from "@/lib/types/logs";

const DIMENSION_OPTIONS: Array<{
	value: FinalSuccessDistributionDimension;
	label: string;
}> = [
	{ value: "model", label: "By Model" },
	{ value: "provider", label: "By Provider" },
	{ value: "key", label: "By Key" },
	{ value: "layer", label: "By Layer" },
];

interface FinalSuccessDistributionCardProps {
	data: FinalSuccessDistributionResponse | null;
	loading: boolean;
	dimension: FinalSuccessDistributionDimension;
	onDimensionChange: (dimension: FinalSuccessDistributionDimension) => void;
}

export function FinalSuccessDistributionCard({ data, loading, dimension, onDimensionChange }: FinalSuccessDistributionCardProps) {
	return (
		<Card className="shadow-none" data-testid="logs-final-success-distribution-card">
			<CardHeader className="space-y-2 pb-1">
				<div className="flex flex-wrap items-center justify-between gap-3">
					<div>
						<CardTitle className="text-base">Final Success Distribution</CardTitle>
						<div className="text-muted-foreground text-[11px] leading-4">Only counts the final successful target for each request.</div>
					</div>
					<div className="flex flex-wrap gap-2">
						{DIMENSION_OPTIONS.map((option) => (
							<button
								key={option.value}
								type="button"
								data-testid={`logs-final-distribution-dimension-${option.value}`}
								className={cn(
									"rounded-full border px-2.5 py-0.5 text-[11px] transition-colors",
									dimension === option.value
										? "border-foreground bg-foreground text-background"
										: "border-border text-muted-foreground hover:bg-muted",
								)}
								onClick={() => onDimensionChange(option.value)}
							>
								{option.label}
							</button>
						))}
					</div>
				</div>
			</CardHeader>
			<CardContent className="space-y-2 pt-1">
				{loading ? (
					<div className="space-y-2">
						<Skeleton className="h-4 w-32" />
						<Skeleton className="h-12 w-full" />
						<Skeleton className="h-12 w-full" />
						<Skeleton className="h-12 w-full" />
					</div>
				) : data && data.items.length > 0 ? (
					<>
						<div className="text-muted-foreground text-xs">{data.total_success_count.toLocaleString()} successful final requests</div>
						<div className={LOGS_FINAL_DISTRIBUTION_LIST_CLASS}>
							{data.items.slice(0, LOGS_FINAL_DISTRIBUTION_ITEM_COUNT).map((item) => (
								<div
									key={item.value}
									className="flex items-center justify-between gap-3 rounded-lg border px-3 py-1.5"
									data-testid={`logs-final-distribution-item-${item.value}`}
								>
									<div className="min-w-0 flex-1">
										<div className="truncate text-sm font-medium">{item.label}</div>
										<div className="text-muted-foreground text-xs">{item.success_count.toLocaleString()} successes</div>
									</div>
									<div className="text-right font-mono text-sm">{item.success_ratio.toFixed(2)}%</div>
								</div>
							))}
						</div>
					</>
				) : (
					<div className="text-muted-foreground rounded-lg border border-dashed px-4 py-6 text-center text-sm">
						No successful final requests in the current filter range.
					</div>
				)}
			</CardContent>
		</Card>
	);
}
