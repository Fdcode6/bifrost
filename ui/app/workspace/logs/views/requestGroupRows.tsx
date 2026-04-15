"use client";

import { Fragment } from "react";

import { buildPinStyle, PIN_SHADOW_LEFT, PIN_SHADOW_RIGHT } from "@/components/table";
import { Badge } from "@/components/ui/badge";
import { TableCell, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { flexRender, Table as TanstackTable } from "@tanstack/react-table";
import { ChevronRight } from "lucide-react";

import type { LogEntry } from "@/lib/types/logs";

import type { RequestGroupDisplayState } from "../requestGrouping";

interface RequestGroupRowsProps {
	table: TanstackTable<LogEntry>;
	groups: RequestGroupDisplayState[];
	expandedGroupIds: string[];
	selectedGroupId: string | null;
	selectedAttemptId: string | null;
	onToggleGroup: (groupId: string) => void;
	onSelectGroup: (group: RequestGroupDisplayState) => void;
	onSelectAttempt: (group: RequestGroupDisplayState, attempt: LogEntry) => void;
	pinOffsets: Map<string, number>;
	lastLeftPinId?: string;
	firstRightPinId?: string;
}

export function RequestGroupRows({
	table,
	groups,
	expandedGroupIds,
	selectedGroupId,
	selectedAttemptId,
	onToggleGroup,
	onSelectGroup,
	onSelectAttempt,
	pinOffsets,
	lastLeftPinId,
	firstRightPinId,
}: RequestGroupRowsProps) {
	const expandedGroupSet = new Set(expandedGroupIds);
	const rowByID = new Map(table.getRowModel().rows.map((row) => [row.original.id, row]));

	if (groups.length === 0) {
		return (
			<TableRow>
				<TableCell colSpan={table.getVisibleLeafColumns().length} className="text-muted-foreground h-16 text-center">
					No logs found.
				</TableCell>
			</TableRow>
		);
	}

	return (
		<>
			{groups.map((group) => {
				const isExpanded = expandedGroupSet.has(group.groupId);
				const isSelectedGroup = selectedGroupId === group.groupId;
				const visibleColumnsCount = table.getVisibleLeafColumns().length;
				const isMultiAttempt = group.visibleAttemptCount > 1;

				if (!isMultiAttempt) {
					const singleAttempt = group.attempts[0];
					const row = rowByID.get(singleAttempt.id);
					if (!row) {
						return null;
					}

					return (
						<TableRow
							key={group.groupId}
							className={cn(
								"hover:bg-muted/50 group/table-row h-12 cursor-pointer",
								selectedAttemptId === singleAttempt.id && "bg-muted/40",
							)}
						>
							{row.getVisibleCells().map((cell) => {
								const pinned = cell.column.getIsPinned();
								return (
									<TableCell
										key={cell.id}
										onClick={() => onSelectAttempt(group, singleAttempt)}
										style={buildPinStyle(cell.column, pinOffsets)}
										className={cn(
											pinned && "bg-background sticky",
											cell.column.id === lastLeftPinId && PIN_SHADOW_LEFT,
											cell.column.id === firstRightPinId && PIN_SHADOW_RIGHT,
										)}
									>
										{flexRender(cell.column.columnDef.cell, cell.getContext())}
									</TableCell>
								);
							})}
						</TableRow>
					);
				}

				return (
					<Fragment key={group.groupId}>
						<TableRow className={cn("bg-muted/20 hover:bg-muted/35 cursor-pointer border-b", isSelectedGroup && "bg-muted/40")}>
							<TableCell colSpan={visibleColumnsCount} className="py-2">
								<div
									className="flex w-full items-start justify-between gap-4 text-left"
									onClick={() => onSelectGroup(group)}
									data-testid={`logs-request-group-${group.groupId}`}
								>
									<div className="flex min-w-0 flex-1 items-start gap-2.5">
										<button
											type="button"
											className="text-muted-foreground hover:bg-muted mt-0.5 rounded p-1"
											onClick={(event) => {
												event.stopPropagation();
												onToggleGroup(group.groupId);
											}}
											aria-label={isExpanded ? "Collapse request group" : "Expand request group"}
										>
											<ChevronRight className={cn("h-4 w-4 transition-transform", isExpanded && "rotate-90")} />
										</button>
										<div className="min-w-0 flex-1 space-y-1.5">
											<div className="flex flex-wrap items-center gap-1.5">
												<Badge variant={group.finalStatus === "success" ? "default" : "secondary"}>
													{group.finalAttemptVisible ? `Final ${group.finalStatus}` : "Partial"}
												</Badge>
												<Badge variant="outline">{group.visibleAttemptCount} attempts</Badge>
												{group.finalTarget && <Badge variant="outline">{group.finalTarget.layerLabel}</Badge>}
											</div>
											<div className="text-sm leading-5 font-medium">
												{group.finalTarget
													? `${group.finalTarget.provider} / ${group.finalTarget.model}`
													: "Final target unavailable in current page"}
											</div>
											<div className="text-muted-foreground line-clamp-1 text-[11px] leading-4">
												{group.latestAttempt.input_history?.[0]?.content && typeof group.latestAttempt.input_history[0].content === "string"
													? group.latestAttempt.input_history[0].content
													: group.latestAttempt.model}
											</div>
										</div>
									</div>
									<div className="text-muted-foreground shrink-0 text-right text-[11px] leading-4">
										<div>{new Date(group.latestAttempt.timestamp).toLocaleString()}</div>
										<div>
											{group.finalAttemptVisible && group.finalAttempt?.latency != null
												? `${group.finalAttempt.latency.toLocaleString()}ms`
												: "Latency unavailable"}
										</div>
									</div>
								</div>
							</TableCell>
						</TableRow>
						{isExpanded &&
							group.attempts.map((attempt) => {
								const row = rowByID.get(attempt.id);
								if (!row) {
									return null;
								}

								return (
									<TableRow
										key={attempt.id}
										className={cn(
											"hover:bg-muted/40 group/table-row h-12 cursor-pointer",
											selectedAttemptId === attempt.id && "bg-muted/40",
										)}
										data-testid={`logs-request-attempt-${attempt.id}`}
									>
										{row.getVisibleCells().map((cell) => {
											const pinned = cell.column.getIsPinned();
											return (
												<TableCell
													key={cell.id}
													onClick={() => onSelectAttempt(group, attempt)}
													style={buildPinStyle(cell.column, pinOffsets)}
													className={cn(
														"pl-10",
														pinned && "bg-background sticky",
														cell.column.id === lastLeftPinId && PIN_SHADOW_LEFT,
														cell.column.id === firstRightPinId && PIN_SHADOW_RIGHT,
													)}
												>
													{flexRender(cell.column.columnDef.cell, cell.getContext())}
												</TableCell>
											);
										})}
									</TableRow>
								);
							})}
					</Fragment>
				);
			})}
		</>
	);
}
