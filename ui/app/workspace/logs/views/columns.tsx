"use client";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ProviderIconType, RenderProviderIcon } from "@/lib/constants/icons";
import { ProviderName, RequestTypeColors, RequestTypeLabels, Status, StatusBarColors } from "@/lib/constants/logs";
import { LogEntry, ResponsesMessageContentBlock } from "@/lib/types/logs";
import { ColumnDef } from "@tanstack/react-table";
import { ArrowUpDown, Trash2 } from "lucide-react";
import moment from "moment";

const LOGS_COLUMN_SIZES = {
	status: 40,
	timestamp: 220,
	requestType: 96,
	provider: 128,
	model: 220,
	latency: 120,
	tokens: 150,
	cost: 96,
	input: 360,
	metadata: 150,
	actions: 64,
} as const;

function getMessage(log?: LogEntry) {
	if (log?.object === "list_models") {
		return "N/A";
	}
	if (log?.input_history && log.input_history.length > 0) {
		let userMessageContent = log.input_history[log.input_history.length - 1].content;
		if (userMessageContent == undefined) {
			return "";
		}
		if (typeof userMessageContent === "string") {
			return userMessageContent;
		}
		let lastTextContentBlock = "";
		for (const block of userMessageContent) {
			if (block.type === "text" && block.text) {
				lastTextContentBlock = block.text;
			}
		}
		return lastTextContentBlock;
	} else if (log?.responses_input_history && log.responses_input_history.length > 0) {
		let lastMessage = log.responses_input_history[log.responses_input_history.length - 1];
		let lastMessageContent = lastMessage.content;
		if (typeof lastMessageContent === "string") {
			return lastMessageContent;
		}
		let lastTextContentBlock = "";
		for (const block of (lastMessageContent ?? []) as ResponsesMessageContentBlock[]) {
			if (block.text && block.text !== "") {
				lastTextContentBlock = block.text;
			}
		}
		// If no content found in content field, check output field for Responses API
		if (!lastTextContentBlock && lastMessage.output) {
			// Handle output field - it could be a string, an array of content blocks, or a computer tool call output data
			if (typeof lastMessage.output === "string") {
				return lastMessage.output;
			} else if (Array.isArray(lastMessage.output)) {
				return lastMessage.output.map((block) => block.text).join("\n");
			} else if (lastMessage.output.type && lastMessage.output.type === "computer_screenshot") {
				return lastMessage.output.image_url;
			}
		}
		return lastTextContentBlock ?? "";
	} else if (log?.speech_input) {
		return log.speech_input.input;
	} else if (log?.transcription_input) {
		return "Audio file";
	} else if (log?.image_generation_input?.prompt) {
		return log.image_generation_input.prompt;
	}
	if (log?.content_summary) {
		return log.content_summary;
	}
	const obj = log?.object as string | undefined;
	if (obj === "image_edit" || obj === "image_edit_stream" || obj === "image_variation") {
		return "Image file";
	}
	return "";
}

export const createColumns = (
	onDelete: (log: LogEntry) => void,
	hasDeleteAccess = true,
	metadataKeys: string[] = [],
): ColumnDef<LogEntry>[] => {
	const baseColumns: ColumnDef<LogEntry>[] = [
		{
			accessorKey: "status",
			header: "",
			size: LOGS_COLUMN_SIZES.status,
			minSize: LOGS_COLUMN_SIZES.status,
			maxSize: LOGS_COLUMN_SIZES.status,
			cell: ({ row }) => {
				const status = row.original.status as Status;
				return <div className={`h-full min-h-[24px] w-1 rounded-sm ${StatusBarColors[status]}`} />;
			},
		},
		{
			accessorKey: "timestamp",
			header: ({ column }) => (
				<Button variant="ghost" onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}>
					Time
					<ArrowUpDown className="ml-2 h-4 w-4" />
				</Button>
			),
			size: LOGS_COLUMN_SIZES.timestamp,
			minSize: LOGS_COLUMN_SIZES.timestamp,
			cell: ({ row }) => {
				const timestamp = row.original.timestamp;
				return <div className="text-xs">{moment(timestamp).format("YYYY-MM-DD hh:mm:ss A (Z)")}</div>;
			},
		},
		{
			id: "request_type",
			header: "Type",
			size: LOGS_COLUMN_SIZES.requestType,
			minSize: LOGS_COLUMN_SIZES.requestType,
			cell: ({ row }) => {
				return (
					<Badge variant="outline" className={`${RequestTypeColors[row.original.object as keyof typeof RequestTypeColors]} text-xs`}>
						{RequestTypeLabels[row.original.object as keyof typeof RequestTypeLabels]}
					</Badge>
				);
			},
		},
		{
			accessorKey: "provider",
			header: "Provider",
			size: LOGS_COLUMN_SIZES.provider,
			minSize: LOGS_COLUMN_SIZES.provider,
			cell: ({ row }) => {
				const provider = row.original.provider as ProviderName;
				return (
					<Badge variant="secondary" className={`font-mono text-xs uppercase`}>
						<RenderProviderIcon provider={provider as ProviderIconType} size="sm" />
						{provider}
					</Badge>
				);
			},
		},
		{
			accessorKey: "model",
			header: "Model",
			size: LOGS_COLUMN_SIZES.model,
			minSize: LOGS_COLUMN_SIZES.model,
			cell: ({ row }) => <div className="truncate font-mono text-xs font-normal">{row.original.model || "N/A"}</div>,
		},
		{
			accessorKey: "latency",
			header: ({ column }) => (
				<Button variant="ghost" onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}>
					Latency
					<ArrowUpDown className="ml-2 h-4 w-4" />
				</Button>
			),
			size: LOGS_COLUMN_SIZES.latency,
			minSize: LOGS_COLUMN_SIZES.latency,
			cell: ({ row }) => {
				const latency = row.original.latency;
				return (
					<div className="font-mono text-sm">{latency === undefined || latency === null ? "N/A" : `${latency.toLocaleString()}ms`}</div>
				);
			},
		},
		{
			accessorKey: "tokens",
			header: ({ column }) => (
				<Button variant="ghost" onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}>
					Tokens
					<ArrowUpDown className="ml-2 h-4 w-4" />
				</Button>
			),
			size: LOGS_COLUMN_SIZES.tokens,
			minSize: LOGS_COLUMN_SIZES.tokens,
			cell: ({ row }) => {
				const tokenUsage = row.original.token_usage;
				if (!tokenUsage) {
					return <div className="font-mono text-sm">N/A</div>;
				}

				return (
					<div className="text-sm">
						<div className="font-mono">
							{tokenUsage.total_tokens.toLocaleString()}{" "}
							{tokenUsage.completion_tokens != null && tokenUsage.prompt_tokens != null
								? `(${tokenUsage.prompt_tokens.toLocaleString()}+${tokenUsage.completion_tokens.toLocaleString()})`
								: ""}
						</div>
					</div>
				);
			},
		},
		{
			accessorKey: "cost",
			header: ({ column }) => (
				<Button variant="ghost" onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}>
					Cost
					<ArrowUpDown className="ml-2 h-4 w-4" />
				</Button>
			),
			size: LOGS_COLUMN_SIZES.cost,
			minSize: LOGS_COLUMN_SIZES.cost,
			cell: ({ row }) => {
				if (!row.original.cost) {
					return <div className="font-mono text-xs">N/A</div>;
				}

				return (
					<div className="text-xs">
						<div className="font-mono">{row.original.cost?.toFixed(4)}</div>
					</div>
				);
			},
		},
		{
			accessorKey: "input",
			header: "Message",
			size: LOGS_COLUMN_SIZES.input,
			minSize: LOGS_COLUMN_SIZES.input,
			cell: ({ row }) => {
				const input = getMessage(row.original);
				const isLargePayload = row.original.is_large_payload_request || row.original.is_large_payload_response;
				return (
					<div className="flex min-w-0 items-center gap-1.5">
						{isLargePayload && (
							<span
								className="shrink-0 rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-900/50 dark:text-amber-400"
								title="Large payload - streamed directly to provider"
							>
								LP
							</span>
						)}
						<div className="min-w-0 truncate font-mono text-sm font-normal" title={input || "-"}>
							{input ||
								(isLargePayload
									? `Large payload ${
											row.original.is_large_payload_request && row.original.is_large_payload_response
												? "request & response"
												: row.original.is_large_payload_request
													? "request"
													: "response"
										}`
									: "-")}
						</div>
					</div>
				);
			},
		},
	];

	// Generate dynamic metadata columns
	const metadataColumns: ColumnDef<LogEntry>[] = metadataKeys.map((key) => ({
		id: `metadata_${key}`,
		header: key.charAt(0).toUpperCase() + key.slice(1),
		size: LOGS_COLUMN_SIZES.metadata,
		minSize: LOGS_COLUMN_SIZES.metadata,
		cell: ({ row }) => {
			const value = row.original.metadata?.[key];
			return <div className="truncate font-mono text-xs">{value ?? "-"}</div>;
		},
	}));

	const actionsColumn: ColumnDef<LogEntry> = {
		id: "actions",
		size: LOGS_COLUMN_SIZES.actions,
		minSize: LOGS_COLUMN_SIZES.actions,
		maxSize: LOGS_COLUMN_SIZES.actions,
		cell: ({ row }) => {
			const log = row.original;
			return (
				<Button
					variant="outline"
					size="icon"
					aria-label="Delete log"
					className="text-destructive hover:bg-destructive/10 hover:text-destructive border-destructive/30"
					onClick={() => onDelete(log)}
					disabled={!hasDeleteAccess}
				>
					<Trash2 />
				</Button>
			);
		},
	};

	return [...baseColumns, ...metadataColumns, actionsColumn];
};
